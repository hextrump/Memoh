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
	antChromeToolkitPath = "/opt/memoh/toolkit/browser/bin/ant-chrome"
	antChromeCoreDir     = "/opt/memoh/toolkit/browser/chrome"
	antChromeCoreName    = "fingerprint-chromium"
	antChromeProfileDir  = "/data/.memoh/browser-profile"
	antChromeProfileName = "memoh-workspace"
	// `bridge browser new` rotates to a fresh profile under antChromeProfilesDir
	// and records its name here so later ensures keep using it.
	antChromeProfilesDir    = "/data/.memoh/browser-profiles"
	antChromeCurrentFile    = "/data/.memoh/browser-current"
	antChromeLogFile        = "/tmp/memoh-ant-chrome.log"
	antChromeProxyFile      = "/data/.memoh/browser-proxy"
	antChromeXrayPath       = "/opt/memoh/toolkit/browser/xray/xray"
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
	_, profileDir := currentBrowserProfile()
	if err := os.MkdirAll(profileDir, 0o750); err != nil {
		return fmt.Errorf("create ant-chrome profile dir: %w", err)
	}
	clearStaleProfileLock(ctx, profileDir)
	if !displayProcessRunning(ctx, antChromeProcessName) {
		// Detached from ctx: `bridge browser ensure` exits right after provisioning,
		// and cancelling its ctx would kill the manager it just started.
		// Its output goes to a file: an inherited stdout pipe would keep a
		// caller reading `bridge browser ensure` output waiting for EOF.
		out, err := os.OpenFile(antChromeLogFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return fmt.Errorf("open ant-chrome log: %w", err)
		}
		startDisplayCommandWithOutput(context.WithoutCancel(ctx), antChromeProcessName, []string{"UBUNTU_MENUPROXY=0", "ANT_BROWSER_HIDE_WINDOW=1", "XRAY_BINARY_PATH=" + antChromeXrayPath, antChromeLocaleEnv}, out, binPath)
		_ = out.Close()
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

// browserProvisionRequestWithOverrides applies the server's per-bot
// MEMOH_BROWSER_PROVISION_JSON to the default request.
const legacyDefaultBrowserProvisionJSON = `{"fingerprintArgs":["--fingerprint-platform=windows"]}`

func browserProvisionRequestWithOverrides() (*browserProvisionRequest, error) {
	req := defaultBrowserProvisionRequest()
	raw := strings.TrimSpace(os.Getenv(antBrowserProvisionJSON))
	// Servers before the Japanese template sent this whenever nothing was
	// configured, and a workspace container keeps the env it was created with,
	// so treat it as no override rather than pinning old workspaces to Windows.
	if raw == "" || raw == legacyDefaultBrowserProvisionJSON {
		return req, nil
	}
	// Unmarshal would reuse the template's backing array; start from nil.
	template := req.FingerprintArgs
	req.FingerprintArgs = nil
	if err := json.Unmarshal([]byte(raw), req); err != nil {
		return nil, fmt.Errorf("parse %s: %w", antBrowserProvisionJSON, err)
	}
	req.FingerprintArgs = mergeFingerprintArgs(template, req.FingerprintArgs)
	return req, nil
}

func provisionAntBrowser(ctx context.Context) error {
	req, err := browserProvisionRequestWithOverrides()
	if err != nil {
		return err
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
	name, dir := currentBrowserProfile()
	return &browserProvisionRequest{
		ProfileName:        name,
		CoreName:           antChromeCoreName,
		CorePath:           antChromeCoreDir,
		UserDataDir:        dir,
		RestoreLastSession: "off",
		FingerprintArgs:    append([]string(nil), browserFingerprintTemplate...),
		// The workspace container always runs fingerprint-chromium as root,
		// which it refuses to do without --no-sandbox. Workspace containers
		// don't get an enlarged /dev/shm, so also fall back to /tmp for
		// shared memory rather than crashing on the default 64MB tmpfs. The
		// window fills the desktop and --test-type drops the --no-sandbox infobar.
		// There is no GPU, so WebGL runs on SwiftShader; without it WebGL is
		// missing altogether, which no real Mac does.
		LaunchArgs: []string{
			"--no-sandbox", "--disable-dev-shm-usage", "--start-maximized", "--test-type", "--hide-crash-restore-bubble",
			"--use-angle=swiftshader", "--enable-unsafe-swiftshader",
		},
		ProxyConfig:   currentBrowserProxy(),
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

// currentBrowserProfile returns the profile name and user data dir the
// workspace browser runs on: the original one until `bridge browser new`
// rotates it.
func currentBrowserProfile() (string, string) {
	raw, err := os.ReadFile(antChromeCurrentFile)
	name := strings.TrimSpace(string(raw))
	if err != nil || !validRotatedProfileName(name) {
		return antChromeProfileName, antChromeProfileDir
	}
	return name, filepath.Join(antChromeProfilesDir, name)
}

func validRotatedProfileName(name string) bool {
	rest, ok := strings.CutPrefix(name, antChromeProfileName+"-")
	if !ok || rest == "" {
		return false
	}
	_, err := strconv.ParseInt(rest, 10, 64)
	return err == nil
}

// runBrowserNewSubcommand implements `bridge browser new`: it throws away the
// current browser instance (process, Ant-Browser profile, user data) and
// provisions a fresh profile. Ant-Browser derives the fingerprint seed from
// the profile ID, so the new instance gets a new fingerprint and empty
// cookies/storage — used when a site blocks the current identity or the
// profile keeps crashing.
func runBrowserNewSubcommand() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 2*antBrowserLaunchAPIWait+antBrowserProvisionWait)
	defer cancel()
	if err := rotateWorkspaceBrowser(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		return 1
	}
	return 0
}

func rotateWorkspaceBrowser(ctx context.Context) error {
	// Make sure the manager is up so the old profile can be stopped cleanly.
	if err := startWorkspaceBrowser(ctx); err != nil {
		logger.FromContext(ctx).Warn("current browser failed to start before rotation", slog.Any("error", err))
		if err := waitAntBrowserLaunchAPI(ctx, antBrowserLaunchAPIWait); err != nil {
			return err
		}
	}
	oldName, oldDir := currentBrowserProfile()
	if id, err := findAntBrowserProfileID(ctx, oldName); err != nil {
		return err
	} else if id != "" {
		// stop is best effort (it may already be dead); delete must succeed.
		_, _ = antBrowserAPI(ctx, http.MethodPost, "/api/profiles/"+id+"/stop")
		if _, err := antBrowserAPI(ctx, http.MethodDelete, "/api/profiles/"+id); err != nil {
			return fmt.Errorf("delete old browser profile: %w", err)
		}
	}
	// Chrome processes orphaned by an earlier manager restart are invisible to stop.
	killProfileProcesses(ctx, oldDir)
	if err := os.RemoveAll(oldDir); err != nil {
		logger.FromContext(ctx).Warn("remove old browser profile dir", slog.String("dir", oldDir), slog.Any("error", err))
	}

	name := fmt.Sprintf("%s-%d", antChromeProfileName, time.Now().Unix())
	if err := os.MkdirAll(antChromeProfilesDir, 0o750); err != nil {
		return err
	}
	if err := os.WriteFile(antChromeCurrentFile, []byte(name+"\n"), 0o600); err != nil {
		return fmt.Errorf("record new browser profile: %w", err)
	}
	if err := startWorkspaceBrowser(ctx); err != nil {
		return err
	}
	fmt.Println(name)
	return nil
}

func findAntBrowserProfileID(ctx context.Context, name string) (string, error) {
	body, err := antBrowserAPI(ctx, http.MethodGet, "/api/profiles")
	if err != nil {
		return "", fmt.Errorf("list browser profiles: %w", err)
	}
	var list struct {
		Items []struct {
			ProfileID   string `json:"profileId"`
			ProfileName string `json:"profileName"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		return "", fmt.Errorf("decode browser profiles: %w", err)
	}
	for _, item := range list.Items {
		if item.ProfileName == name {
			return item.ProfileID, nil
		}
	}
	return "", nil
}

func antBrowserAPI(ctx context.Context, method, path string) ([]byte, error) {
	reqCtx, cancel := context.WithTimeout(ctx, antBrowserProvisionWait)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, method, antBrowserLaunchAPIBase+path, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req) //nolint:gosec // G704: fixed local antmemo Launch API on 127.0.0.1.
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%s %s returned %d: %s", method, path, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return body, nil
}

// killProfileProcesses SIGKILLs every process started with
// --user-data-dir=<dir> and waits briefly for them to go away.
func killProfileProcesses(ctx context.Context, dir string) {
	flag := []byte("--user-data-dir=" + dir + "\x00")
	entries, _ := os.ReadDir("/proc")
	var pids []int
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid == os.Getpid() {
			continue
		}
		cmdline, err := os.ReadFile(filepath.Join("/proc", e.Name(), "cmdline"))
		if err != nil || !bytes.Contains(cmdline, flag) {
			continue
		}
		if syscall.Kill(pid, syscall.SIGKILL) == nil {
			pids = append(pids, pid)
		}
	}
	for i := 0; i < 20 && len(pids) > 0; i++ {
		alive := pids[:0]
		for _, pid := range pids {
			if _, err := os.Stat(fmt.Sprintf("/proc/%d", pid)); err == nil {
				alive = append(alive, pid)
			}
		}
		pids = alive
		time.Sleep(100 * time.Millisecond)
	}
	if len(pids) > 0 {
		logger.FromContext(ctx).Warn("browser processes survived SIGKILL", slog.Any("pids", pids))
	}
}

// currentBrowserProxy returns the proxy set by `bridge browser proxy`, or
// direct. Authenticated http/socks5 and vmess/ss/... go through Ant-Browser's
// xray bridge, since Chromium's --proxy-server cannot carry credentials.
// browserFingerprintTemplate is Ant Browser's Japanese user template
// ("macOS / Chrome / 日本用户" preset with the "日本 macOS Chrome 轻办公" persona's
// OS version). The seed is left out so Ant Browser derives it from the profile
// ID and every new instance still gets its own fingerprint; the window size is
// left to --start-maximized so it matches the desktop.
var browserFingerprintTemplate = []string{
	"--fingerprint-brand=Chrome",
	"--fingerprint-platform=macos",
	"--fingerprint-platform-version=15.2.0",
	"--lang=ja-JP",
	"--accept-lang=ja-JP,ja",
	"--timezone=Asia/Tokyo",
	"--fingerprint-hardware-concurrency=8",
	"--disable-non-proxied-udp",
	"--fingerprinting-canvas-image-data-noise",
	"--fingerprinting-client-rects-noise",
}

// mergeFingerprintArgs applies the server's per-bot fingerprint overrides on
// top of the template: an override takes the place of the template arg with
// the same flag, anything else is appended. A platform other than the
// template's also drops the template's macOS version.
func mergeFingerprintArgs(template, overrides []string) []string {
	flag := func(arg string) string {
		name, _, _ := strings.Cut(strings.TrimSpace(arg), "=")
		return name
	}
	byFlag := map[string]string{}
	for _, arg := range overrides {
		byFlag[flag(arg)] = arg
	}
	if platform, ok := byFlag["--fingerprint-platform"]; ok && !strings.Contains(strings.ToLower(platform), "=mac") {
		if _, ok := byFlag["--fingerprint-platform-version"]; !ok {
			byFlag["--fingerprint-platform-version"] = ""
		}
	}
	out := make([]string, 0, len(template)+len(overrides))
	used := map[string]bool{}
	for _, arg := range template {
		override, ok := byFlag[flag(arg)]
		switch {
		case !ok:
			out = append(out, arg)
		case override != "":
			out = append(out, override)
		}
		used[flag(arg)] = true
	}
	for _, arg := range overrides {
		if !used[flag(arg)] {
			out = append(out, arg)
			used[flag(arg)] = true
		}
	}
	return out
}

// antChromeLocaleEnv gives Chromium (which inherits ant-chrome's environment)
// the template's Japanese locale: on Linux the UI and Intl locale come from
// LANGUAGE, not --lang.
const antChromeLocaleEnv = "LANGUAGE=ja_JP:ja"

func currentBrowserProxy() string {
	raw, err := os.ReadFile(antChromeProxyFile)
	if v := strings.TrimSpace(string(raw)); err == nil && v != "" {
		return v
	}
	return "direct://"
}

var browserProxySchemes = []string{"http://", "https://", "socks5://", "vmess://", "vless://", "trojan://", "ss://", "hysteria2://", "tuic://"}

func validBrowserProxy(v string) bool {
	if strings.ContainsAny(v, " \t\r\n") {
		return false
	}
	l := strings.ToLower(v)
	for _, scheme := range browserProxySchemes {
		if strings.HasPrefix(l, scheme) && len(v) > len(scheme) {
			return true
		}
	}
	return false
}

// runBrowserProxySubcommand implements `bridge browser proxy`: it reads a
// proxy URL (or "direct") from stdin, keeps it for future starts, and
// restarts the current browser instance on it. The instance (fingerprint,
// cookies) stays the same; only the exit IP changes.
func runBrowserProxySubcommand() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 3*antBrowserLaunchAPIWait+antBrowserProvisionWait)
	defer cancel()
	raw, err := io.ReadAll(io.LimitReader(os.Stdin, 8192))
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error()) //nolint:gosec // G705: stderr of a CLI subcommand, not HTML
		return 1
	}
	if err := setWorkspaceBrowserProxy(ctx, strings.TrimSpace(string(raw))); err != nil {
		fmt.Fprintln(os.Stderr, err.Error()) //nolint:gosec // G705: stderr of a CLI subcommand, not HTML
		return 1
	}
	return 0
}

func setWorkspaceBrowserProxy(ctx context.Context, proxy string) error {
	direct := proxy == "" || strings.EqualFold(proxy, "direct") || strings.EqualFold(proxy, "direct://")
	if !direct && !validBrowserProxy(proxy) {
		return fmt.Errorf("unsupported proxy %q: use scheme://[user:pass@]host:port with one of %s, or direct", redactProxy(proxy), strings.Join(browserProxySchemes, " "))
	}
	if err := os.MkdirAll(filepath.Dir(antChromeProxyFile), 0o750); err != nil {
		return err
	}
	if direct {
		if err := os.Remove(antChromeProxyFile); err != nil && !os.IsNotExist(err) {
			return err
		}
	} else if err := os.WriteFile(antChromeProxyFile, []byte(proxy+"\n"), 0o600); err != nil { //nolint:gosec // G703: fixed path; only the content is user input
		return fmt.Errorf("record browser proxy: %w", err)
	}
	err := restartWorkspaceBrowser(ctx)
	if err == nil || direct {
		return err
	}
	// Never leave the bot without a browser: fall back to direct.
	_ = os.Remove(antChromeProxyFile)
	if restoreErr := restartWorkspaceBrowser(ctx); restoreErr != nil {
		return fmt.Errorf("browser failed to start with the proxy (%w) and again without it: %w", err, restoreErr)
	}
	return fmt.Errorf("browser failed to start with the proxy, switched back to a direct connection: %w", err)
}

// restartWorkspaceBrowser stops the current profile so the next provision
// relaunches it with the updated settings.
func restartWorkspaceBrowser(ctx context.Context) error {
	if err := waitAntBrowserLaunchAPI(ctx, 0); err != nil {
		// Manager not running yet: a plain start already uses the new settings.
		return startWorkspaceBrowser(ctx)
	}
	name, dir := currentBrowserProfile()
	if id, err := findAntBrowserProfileID(ctx, name); err != nil {
		return err
	} else if id != "" {
		_, _ = antBrowserAPI(ctx, http.MethodPost, "/api/profiles/"+id+"/stop")
	}
	killProfileProcesses(ctx, dir)
	return startWorkspaceBrowser(ctx)
}

// redactProxy hides the password in a proxy URL for messages and logs.
func redactProxy(v string) string {
	at := strings.LastIndexByte(v, '@')
	scheme := strings.Index(v, "://")
	if at < 0 || scheme < 0 || at < scheme {
		return v
	}
	userinfo := v[scheme+3 : at]
	if user, _, ok := strings.Cut(userinfo, ":"); ok {
		return v[:scheme+3] + user + ":***" + v[at:]
	}
	return v[:scheme+3] + "***" + v[at:]
}
