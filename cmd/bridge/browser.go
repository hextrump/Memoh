package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/felinics/memoh/internal/logger"
)

// antmemo replaces the workspace's built-in Chrome with a bundled fork of
// Ant-Browser (binary name "ant-chrome") driving a fingerprint-chromium
// core. Ant-Browser has no headless mode, so it runs on the same Xvnc :99
// display the old Chrome used to. Its Launch API exposes a unified CDP
// reverse proxy, so Memoh always dials a single fixed port regardless of
// which real debug port the underlying fingerprint-chromium process picked.
const (
	antChromeToolkitPath    = "/opt/memoh/toolkit/browser/bin/ant-chrome"
	antChromeCoreDir        = "/opt/memoh/toolkit/browser/chrome"
	antChromeCoreName       = "fingerprint-chromium"
	antChromeProfileDir     = "/data/.memoh/browser-profile"
	antChromeProfileName    = "memoh-workspace"
	antChromeProcessName    = "ant-chrome"
	antBrowserLaunchAPIPort = "19876"
	antBrowserLaunchAPIBase = "http://127.0.0.1:" + antBrowserLaunchAPIPort
	antBrowserProvisionJSON = "MEMOH_BROWSER_PROVISION_JSON"
	antBrowserStartURLEnv   = "MEMOH_DISPLAY_BROWSER_URL"
	antBrowserLaunchAPIWait = 45 * time.Second
	antBrowserProvisionWait = 45 * time.Second
	antBrowserPollInterval  = 300 * time.Millisecond
)

// browserProvisionRequest/browserProvisionResponse mirror
// third_party/ant-browser/backend/internal/launchcode/provision_api.go's
// ProvisionRequest/ProvisionResponse exactly (field names and JSON tags),
// since that fork is vendored as our own submodule rather than a
// stability-guaranteed public API.
type browserProvisionRequest struct {
	ProfileName        string   `json:"profileName"`
	CoreName           string   `json:"coreName"`
	CorePath           string   `json:"corePath"`
	UserDataDir        string   `json:"userDataDir"`
	RestoreLastSession string   `json:"restoreLastSession"`
	FingerprintArgs    []string `json:"fingerprintArgs,omitempty"`
	ProxyId            string   `json:"proxyId,omitempty"`
	ProxyConfig        string   `json:"proxyConfig,omitempty"`
	LaunchArgs         []string `json:"launchArgs,omitempty"`
	StartURL           string   `json:"startUrl"`
	Autostart          bool     `json:"autostart"`
	ForceRecreate      bool     `json:"forceRecreate"`
	TimeoutMs          int      `json:"timeoutMs,omitempty"`
}

type browserProvisionResponse struct {
	OK         bool   `json:"ok"`
	ProfileID  string `json:"profileId"`
	CoreID     string `json:"coreId"`
	LaunchCode string `json:"launchCode"`
	Running    bool   `json:"running"`
	DebugReady bool   `json:"debugReady"`
	DebugPort  int    `json:"debugPort"`
	CDPURL     string `json:"cdpUrl,omitempty"`
	Created    bool   `json:"created"`
	Updated    bool   `json:"updated"`
	Error      string `json:"error,omitempty"`
}

// startWorkspaceBrowser is the single implementation shared by the display
// supervisor, `bridge browser ensure`, and (via that subcommand)
// scripts/display-prepare.sh and internal/agent/tool/browser.go's ensureCDP.
// It replaces what used to be independent, duplicated "launch a browser"
// code paths that each hardcoded Chrome's --remote-debugging-port=9222.
func startWorkspaceBrowser(ctx context.Context) error {
	if _, err := os.Stat(x11SocketDir + "/X99"); err != nil {
		return fmt.Errorf("workspace display socket is not ready: %w", err)
	}
	binPath := resolveDisplayCommand(antChromeToolkitPath)
	if binPath == "" {
		return fmt.Errorf("ant-chrome binary is unavailable at %s", antChromeToolkitPath)
	}
	if err := os.MkdirAll(antChromeProfileDir, 0o750); err != nil {
		return fmt.Errorf("create ant-chrome profile dir: %w", err)
	}
	clearStaleProfileLock(ctx, antChromeProfileDir)
	if !displayProcessRunning(ctx, antChromeProcessName) {
		startDisplayCommandWithEnv(ctx, antChromeProcessName, []string{"UBUNTU_MENUPROXY=0", "ANT_BROWSER_HIDE_WINDOW=1"}, binPath)
	}
	if err := waitAntBrowserLaunchAPI(ctx, antBrowserLaunchAPIWait); err != nil {
		return err
	}
	return provisionAntBrowser(ctx)
}

func waitAntBrowserLaunchAPI(ctx context.Context, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	dialer := net.Dialer{Timeout: 300 * time.Millisecond}
	for {
		conn, err := dialer.DialContext(ctx, "tcp", "127.0.0.1:"+antBrowserLaunchAPIPort)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("ant-chrome Launch API did not become reachable on port %s: %w", antBrowserLaunchAPIPort, err)
		}
		timer := time.NewTimer(antBrowserPollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func provisionAntBrowser(ctx context.Context) error {
	req := defaultBrowserProvisionRequest()
	if raw := strings.TrimSpace(os.Getenv(antBrowserProvisionJSON)); raw != "" {
		if err := json.Unmarshal([]byte(raw), req); err != nil {
			return fmt.Errorf("parse %s: %w", antBrowserProvisionJSON, err)
		}
	}
	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("encode provision request: %w", err)
	}

	reqCtx, cancel := context.WithTimeout(ctx, antBrowserProvisionWait)
	defer cancel()
	httpReq, err := http.NewRequestWithContext(reqCtx, http.MethodPost, antBrowserLaunchAPIBase+"/api/provision", bytes.NewReader(body))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(httpReq) //nolint:gosec // G704: target is the fixed local antmemo Launch API on 127.0.0.1, not user input.
	if err != nil {
		return fmt.Errorf("call ant-chrome provision API: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if resp.StatusCode >= 300 {
		return fmt.Errorf("ant-chrome provision API returned %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	var provisionResp browserProvisionResponse
	if err := json.Unmarshal(respBody, &provisionResp); err != nil {
		return fmt.Errorf("decode provision response: %w", err)
	}
	if !provisionResp.OK {
		return fmt.Errorf("ant-chrome provision API rejected request: %s", provisionResp.Error)
	}
	if provisionResp.Error != "" {
		return fmt.Errorf("ant-chrome provisioned but failed to become ready: %s", provisionResp.Error)
	}
	logger.FromContext(ctx).Info("ant-chrome browser provisioned",
		slog.String("profile_id", provisionResp.ProfileID),
		slog.String("core_id", provisionResp.CoreID),
		slog.Bool("debug_ready", provisionResp.DebugReady),
		slog.Int("debug_port", provisionResp.DebugPort),
	)
	return nil
}

func defaultBrowserProvisionRequest() *browserProvisionRequest {
	startURL := strings.TrimSpace(os.Getenv(antBrowserStartURLEnv))
	if startURL == "" {
		startURL = "about:blank"
	}
	return &browserProvisionRequest{
		ProfileName:        antChromeProfileName,
		CoreName:           antChromeCoreName,
		CorePath:           antChromeCoreDir,
		UserDataDir:        antChromeProfileDir,
		RestoreLastSession: "off",
		// The workspace container always runs fingerprint-chromium as root,
		// which it refuses to do without --no-sandbox. Workspace containers
		// don't get an enlarged /dev/shm, so also fall back to /tmp for
		// shared memory rather than crashing on the default 64MB tmpfs. The
		// window fills the desktop and --test-type drops the --no-sandbox infobar.
		LaunchArgs:    []string{"--no-sandbox", "--disable-dev-shm-usage", "--start-maximized", "--test-type", "--hide-crash-restore-bubble"},
		StartURL:      startURL,
		Autostart:     true,
		ForceRecreate: false,
		TimeoutMs:     int(antBrowserProvisionWait / time.Millisecond),
	}
}

// clearStaleProfileLock removes Chromium's Singleton* files when the
// SingletonLock ("<hostname>-<pid>") belongs to a previous workspace container
// or a dead process. The profile lives in /data, which outlives the container,
// so after a restart Chromium would otherwise refuse the profile as "in use by
// another computer".
func clearStaleProfileLock(ctx context.Context, dir string) {
	target, err := os.Readlink(filepath.Join(dir, "SingletonLock"))
	if err != nil {
		return
	}
	i := strings.LastIndexByte(target, '-')
	if i > 0 {
		host, _ := os.Hostname()
		pid, err := strconv.Atoi(target[i+1:])
		if err == nil && target[:i] == host {
			if _, err := os.Stat(fmt.Sprintf("/proc/%d", pid)); err == nil {
				return
			}
		}
	}
	for _, name := range []string{"SingletonLock", "SingletonSocket", "SingletonCookie"} {
		_ = os.Remove(filepath.Join(dir, name))
	}
	logger.FromContext(ctx).Info("removed stale browser profile lock", slog.String("lock", target))
}

// runBrowserEnsureCommand implements the `bridge browser ensure` one-shot
// subcommand: it runs startWorkspaceBrowser once and exits, instead of
// running as a long-lived supervisor. This is what
// internal/agent/tool/browser.go's ensureCDP and scripts/display-prepare.sh
// invoke via a plain shell exec.
func runBrowserEnsureSubcommand() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return runBrowserEnsureCommand(ctx)
}

func runBrowserEnsureCommand(ctx context.Context) int {
	ctx, cancel := context.WithTimeout(ctx, antBrowserLaunchAPIWait+antBrowserProvisionWait)
	defer cancel()
	if err := startWorkspaceBrowser(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		return 1
	}
	return 0
}
