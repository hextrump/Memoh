package launchcode

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"ant-chrome/backend/internal/browser"
)

const (
	defaultProvisionProfileName = "memoh-default"
	defaultProvisionCorePath    = "/opt/memoh/antmemo/chrome"
	defaultProvisionCoreName    = "antmemo-fingerprint-chromium"
	defaultProvisionUserDataDir = "/data/.memoh/ant-browser"
	defaultProvisionStartURL    = "about:blank"
)

// ProvisionRequest POST /api/provision 的请求体。
// 用于 Memoh 桥接进程在容器启动时幂等地声明"这个 bot 应该有哪个 profile/core"，
// 而不必手工调用 /api/profiles + /api/launch 两步。
type ProvisionRequest struct {
	ProfileName        string   `json:"profileName"`
	CoreName           string   `json:"coreName"`
	CorePath           string   `json:"corePath"`
	UserDataDir        string   `json:"userDataDir"`
	RestoreLastSession string   `json:"restoreLastSession"`
	FingerprintArgs    []string `json:"fingerprintArgs"`
	ProxyId            string   `json:"proxyId"`
	ProxyConfig        string   `json:"proxyConfig"`
	LaunchArgs         []string `json:"launchArgs"`
	StartURL           string   `json:"startUrl"`
	Autostart          bool     `json:"autostart"`
	ForceRecreate      bool     `json:"forceRecreate"`
	TimeoutMs          int      `json:"timeoutMs"`
}

// ProvisionResponse POST /api/provision 的响应体。
type ProvisionResponse struct {
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

func decodeProvisionRequest(r *http.Request) (ProvisionRequest, int, string) {
	if r.Method != http.MethodPost {
		return ProvisionRequest{}, http.StatusMethodNotAllowed, "method not allowed"
	}

	var req ProvisionRequest
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		return ProvisionRequest{}, http.StatusBadRequest, "invalid request body"
	}
	return req, http.StatusOK, ""
}

func normalizeProvisionRequest(req ProvisionRequest) ProvisionRequest {
	req.ProfileName = strings.TrimSpace(req.ProfileName)
	if req.ProfileName == "" {
		req.ProfileName = defaultProvisionProfileName
	}
	req.CorePath = strings.TrimSpace(req.CorePath)
	if req.CorePath == "" {
		req.CorePath = defaultProvisionCorePath
	}
	req.CoreName = strings.TrimSpace(req.CoreName)
	if req.CoreName == "" {
		req.CoreName = defaultProvisionCoreName
	}
	req.UserDataDir = strings.TrimSpace(req.UserDataDir)
	if req.UserDataDir == "" {
		req.UserDataDir = defaultProvisionUserDataDir + "/" + req.ProfileName
	}
	req.StartURL = strings.TrimSpace(req.StartURL)
	if req.StartURL == "" {
		req.StartURL = defaultProvisionStartURL
	}
	req.FingerprintArgs = normalizeStringSlice(req.FingerprintArgs)
	req.LaunchArgs = normalizeStringSlice(req.LaunchArgs)
	req.ProxyId = strings.TrimSpace(req.ProxyId)
	req.ProxyConfig = strings.TrimSpace(req.ProxyConfig)
	req.RestoreLastSession = browser.NormalizeRestoreLastSessionMode(req.RestoreLastSession)
	return req
}

// handleProvision POST /api/provision
//
// 幂等地按 ProfileName upsert 一个 profile（以及必要时按 CorePath upsert 一个 core），
// 可选地立即启动并等待 CDP 就绪。供 Memoh bridge 进程在容器/桥接启动时调用，
// 取代手工的 /api/profiles + /api/launch 两步调用。
func (s *LaunchServer) handleProvision(w http.ResponseWriter, r *http.Request) {
	startAt := time.Now()
	clientIP := remoteIP(r.RemoteAddr)

	rawReq, status, errMsg := decodeProvisionRequest(r)
	if errMsg != "" {
		writeJSON(w, status, ProvisionResponse{OK: false, Error: errMsg})
		s.appendLaunchLog(r.Method, r.URL.Path, clientIP, "", LaunchSelector{}, LaunchRequestParams{}, false, status, errMsg, "", "", startAt)
		return
	}
	req := normalizeProvisionRequest(rawReq)

	if s.browserMgr == nil {
		errMsg = "profile catalog is not available"
		writeJSON(w, http.StatusServiceUnavailable, ProvisionResponse{OK: false, Error: errMsg})
		s.appendLaunchLog(r.Method, r.URL.Path, clientIP, "", LaunchSelector{}, LaunchRequestParams{}, false, http.StatusServiceUnavailable, errMsg, "", "", startAt)
		return
	}

	core, err := s.upsertCoreByPath(req.CoreName, req.CorePath)
	if err != nil {
		errMsg = fmt.Sprintf("failed to upsert browser core: %v", err)
		writeJSON(w, http.StatusInternalServerError, ProvisionResponse{OK: false, Error: errMsg})
		s.appendLaunchLog(r.Method, r.URL.Path, clientIP, "", LaunchSelector{}, LaunchRequestParams{}, false, http.StatusInternalServerError, errMsg, "", "", startAt)
		return
	}

	input := browser.ProfileInput{
		ProfileName:        req.ProfileName,
		UserDataDir:        req.UserDataDir,
		CoreId:             core.CoreId,
		RestoreLastSession: req.RestoreLastSession,
		FingerprintArgs:    req.FingerprintArgs,
		ProxyId:            req.ProxyId,
		ProxyConfig:        req.ProxyConfig,
		LaunchArgs:         req.LaunchArgs,
	}

	existing, existingErr := s.findProfileByName(req.ProfileName)
	if existingErr != nil {
		errMsg = existingErr.Error()
		writeJSON(w, mapProfileWriteErrorStatus(existingErr), ProvisionResponse{OK: false, Error: errMsg})
		s.appendLaunchLog(r.Method, r.URL.Path, clientIP, "", LaunchSelector{}, LaunchRequestParams{}, false, mapProfileWriteErrorStatus(existingErr), errMsg, "", "", startAt)
		return
	}

	var (
		profile    *browser.Profile
		launchCode string
		created    bool
		updated    bool
	)
	if existing != nil && !req.ForceRecreate {
		profile, launchCode, status, errMsg = s.updateProfile(existing.ProfileId, input, "", existing)
		updated = true
	} else if existing != nil && req.ForceRecreate {
		if delErr := s.deleteCreatedProfile(existing.ProfileId); delErr != nil {
			errMsg = fmt.Sprintf("failed to recreate profile: %v", delErr)
			writeJSON(w, http.StatusInternalServerError, ProvisionResponse{OK: false, Error: errMsg})
			s.appendLaunchLog(r.Method, r.URL.Path, clientIP, "", LaunchSelector{}, LaunchRequestParams{}, false, http.StatusInternalServerError, errMsg, "", "", startAt)
			return
		}
		s.ClearActiveProfile(existing.ProfileId)
		profile, launchCode, status, errMsg = s.createProfile(input, "")
		created = true
	} else {
		profile, launchCode, status, errMsg = s.createProfile(input, "")
		created = true
	}
	if errMsg != "" {
		writeJSON(w, status, ProvisionResponse{OK: false, Error: errMsg, Created: created, Updated: updated})
		s.appendLaunchLog(r.Method, r.URL.Path, clientIP, launchCode, LaunchSelector{}, LaunchRequestParams{}, false, status, errMsg, "", "", startAt)
		return
	}
	if profile == nil {
		errMsg = "provision did not return a profile"
		writeJSON(w, http.StatusInternalServerError, ProvisionResponse{OK: false, Error: errMsg, Created: created, Updated: updated})
		s.appendLaunchLog(r.Method, r.URL.Path, clientIP, launchCode, LaunchSelector{}, LaunchRequestParams{}, false, http.StatusInternalServerError, errMsg, "", "", startAt)
		return
	}

	resp := ProvisionResponse{
		OK:         true,
		ProfileID:  profile.ProfileId,
		CoreID:     core.CoreId,
		LaunchCode: launchCode,
		Created:    created,
		Updated:    updated,
	}

	if req.Autostart {
		params := LaunchRequestParams{
			LaunchArgs:  req.LaunchArgs,
			StartURLs:   []string{req.StartURL},
			ProxyId:     req.ProxyId,
			ProxyConfig: req.ProxyConfig,
		}
		launched, launchErr := s.launchProfile(profile.ProfileId, params)
		if launchErr != nil {
			resp.Error = fmt.Sprintf("provisioned but failed to launch: %v", launchErr)
			writeJSON(w, http.StatusOK, resp)
			s.appendLaunchLog(r.Method, r.URL.Path, clientIP, launchCode, LaunchSelector{}, params, false, http.StatusOK, resp.Error, profile.ProfileId, profile.ProfileName, startAt)
			return
		}

		timeout := normalizeRuntimeSessionTimeout(req.TimeoutMs)
		ready := false
		if launched != nil {
			settled, isReady, waitErr := s.prepareRuntimeSession(launched, timeout)
			if waitErr != nil {
				resp.Error = fmt.Sprintf("provisioned and launched but failed while waiting for CDP: %v", waitErr)
			} else if settled != nil {
				ready = isReady
				resp.Running = settled.Running
				resp.DebugReady = settled.DebugReady
				resp.DebugPort = settled.DebugPort
				if settled.DebugReady && settled.DebugPort > 0 {
					resp.CDPURL = fmt.Sprintf("http://127.0.0.1:%d", settled.DebugPort)
				}
				if settled.LaunchCode != "" {
					resp.LaunchCode = settled.LaunchCode
				}
			}
		}

		writeJSON(w, http.StatusOK, resp)
		s.appendLaunchLog(r.Method, r.URL.Path, clientIP, resp.LaunchCode, LaunchSelector{}, params, ready, http.StatusOK, resp.Error, profile.ProfileId, profile.ProfileName, startAt)
		return
	}

	writeJSON(w, http.StatusOK, resp)
	s.appendLaunchLog(r.Method, r.URL.Path, clientIP, resp.LaunchCode, LaunchSelector{}, LaunchRequestParams{}, true, http.StatusOK, "", profile.ProfileId, profile.ProfileName, startAt)
}

// findProfileByName 按名称查找 profile（大小写不敏感），用于 /api/provision 的幂等 upsert。
// 若存在多个同名 profile 视为异常状态，返回错误而不是随机挑一个，避免 provisioning 悄悄改错实例。
func (s *LaunchServer) findProfileByName(name string) (*browser.Profile, error) {
	name = strings.TrimSpace(name)
	if name == "" || s.browserMgr == nil {
		return nil, nil
	}

	var match *browser.Profile
	for _, item := range s.browserMgr.List() {
		if strings.EqualFold(strings.TrimSpace(item.ProfileName), name) {
			if match != nil {
				return nil, fmt.Errorf("multiple profiles named %q already exist; provisioning cannot disambiguate", name)
			}
			p := item
			match = &p
		}
	}
	return match, nil
}

// upsertCoreByPath 按 CorePath 幂等 upsert 一个 browser core。
// browser.Manager.SaveCore 在 CoreId 为空时总是生成一个新的随机 UUID，
// 所以要做到幂等，必须先按路径查找已存在的 core 并复用其 CoreId，
// 而不是每次都传空 CoreId（那样会不断插入重复的 core 行）。
func (s *LaunchServer) upsertCoreByPath(coreName, corePath string) (browser.Core, error) {
	corePath = strings.TrimSpace(corePath)
	if corePath == "" {
		return browser.Core{}, fmt.Errorf("corePath is required")
	}

	for _, c := range s.browserMgr.ListCores() {
		if strings.EqualFold(strings.TrimSpace(c.CorePath), corePath) {
			if coreName != "" && c.CoreName != coreName {
				if err := s.browserMgr.SaveCore(browser.CoreInput{CoreId: c.CoreId, CoreName: coreName, CorePath: corePath, IsDefault: c.IsDefault}); err != nil {
					return browser.Core{}, err
				}
				c.CoreName = coreName
			}
			return c, nil
		}
	}

	if err := s.browserMgr.SaveCore(browser.CoreInput{CoreName: coreName, CorePath: corePath}); err != nil {
		return browser.Core{}, err
	}
	for _, c := range s.browserMgr.ListCores() {
		if strings.EqualFold(strings.TrimSpace(c.CorePath), corePath) {
			return c, nil
		}
	}
	return browser.Core{}, fmt.Errorf("core upsert for path %q succeeded but core was not found afterward", corePath)
}
