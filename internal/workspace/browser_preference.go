package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/felinics/memoh/internal/db"
	dbsqlc "github.com/felinics/memoh/internal/db/postgres/sqlc"
)

// Per-bot override keys nested under workspace.browser in a bot's metadata,
// mirroring workspace.image/workspace.gpu in image_preference.go. There is
// no "backend" override here: ant-chrome is the only browser this workspace
// image ships, so per-bot overrides only ever tune its fingerprint/proxy.
const (
	workspaceBrowserMetadataKey             = "browser"
	workspaceBrowserFingerprintPlatformKey  = "fingerprint_platform"
	workspaceBrowserFingerprintSeedKey      = "fingerprint_seed"
	workspaceBrowserExtraFingerprintArgsKey = "extra_fingerprint_args"
	workspaceBrowserProxyModeKey            = "proxy_mode"
	workspaceBrowserProxyServerURLKey       = "proxy_server_url"
)

// WorkspaceBrowserConfig is a bot's resolved (global default + per-bot
// override) antmemo browser preference.
type WorkspaceBrowserConfig struct {
	FingerprintPlatform  string
	FingerprintSeed      string
	ExtraFingerprintArgs []string
	ProxyMode            string
	ProxyServerURL       string
}

func workspaceBrowserFromMetadata(metadata map[string]any) (WorkspaceBrowserConfig, bool) {
	section := workspaceSection(metadata)
	raw, ok := section[workspaceBrowserMetadataKey]
	if !ok {
		return WorkspaceBrowserConfig{}, false
	}
	browserSection, ok := raw.(map[string]any)
	if !ok {
		return WorkspaceBrowserConfig{}, true
	}

	cfg := WorkspaceBrowserConfig{}
	cfg.FingerprintPlatform, _ = browserSection[workspaceBrowserFingerprintPlatformKey].(string)
	cfg.ProxyMode, _ = browserSection[workspaceBrowserProxyModeKey].(string)
	cfg.ProxyServerURL, _ = browserSection[workspaceBrowserProxyServerURLKey].(string)

	switch seed := browserSection[workspaceBrowserFingerprintSeedKey].(type) {
	case string:
		cfg.FingerprintSeed = strings.TrimSpace(seed)
	case float64:
		cfg.FingerprintSeed = strconv.FormatInt(int64(seed), 10)
	}

	switch typed := browserSection[workspaceBrowserExtraFingerprintArgsKey].(type) {
	case []string:
		cfg.ExtraFingerprintArgs = append(cfg.ExtraFingerprintArgs, typed...)
	case []any:
		for _, item := range typed {
			if arg, ok := item.(string); ok {
				cfg.ExtraFingerprintArgs = append(cfg.ExtraFingerprintArgs, arg)
			}
		}
	}

	return cfg, true
}

func withWorkspaceBrowserPreference(metadata map[string]any, cfg WorkspaceBrowserConfig) map[string]any {
	next := cloneAnyMap(metadata)
	section := workspaceSection(next)
	browserSection := map[string]any{}
	if platform := strings.TrimSpace(cfg.FingerprintPlatform); platform != "" {
		browserSection[workspaceBrowserFingerprintPlatformKey] = platform
	}
	if seed := strings.TrimSpace(cfg.FingerprintSeed); seed != "" {
		browserSection[workspaceBrowserFingerprintSeedKey] = seed
	}
	if len(cfg.ExtraFingerprintArgs) > 0 {
		browserSection[workspaceBrowserExtraFingerprintArgsKey] = append([]string(nil), cfg.ExtraFingerprintArgs...)
	}
	if mode := strings.TrimSpace(cfg.ProxyMode); mode != "" {
		browserSection[workspaceBrowserProxyModeKey] = mode
	}
	if url := strings.TrimSpace(cfg.ProxyServerURL); url != "" {
		browserSection[workspaceBrowserProxyServerURLKey] = url
	}
	section[workspaceBrowserMetadataKey] = browserSection
	next[workspaceMetadataKey] = section
	return next
}

func withoutWorkspaceBrowserPreference(metadata map[string]any) map[string]any {
	next := cloneAnyMap(metadata)
	section := workspaceSection(next)
	delete(section, workspaceBrowserMetadataKey)
	if len(section) == 0 {
		delete(next, workspaceMetadataKey)
		return next
	}
	next[workspaceMetadataKey] = section
	return next
}

func (m *Manager) updateBotWorkspaceBrowserPreference(ctx context.Context, botID string, cfg WorkspaceBrowserConfig, clearPreference bool) error {
	if m.queries == nil {
		return nil
	}
	botUUID, err := db.ParseUUID(botID)
	if err != nil {
		return err
	}
	row, err := m.queries.GetBotByID(ctx, botUUID)
	if err != nil {
		return err
	}
	metadata, err := decodeBotMetadata(row.Metadata)
	if err != nil {
		return err
	}
	if clearPreference {
		metadata = withoutWorkspaceBrowserPreference(metadata)
	} else {
		metadata = withWorkspaceBrowserPreference(metadata, cfg)
	}
	payload, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	_, err = m.queries.UpdateBotProfile(ctx, dbsqlc.UpdateBotProfileParams{
		ID:          botUUID,
		Name:        row.Name,
		DisplayName: row.DisplayName,
		AvatarUrl:   row.AvatarUrl,
		Timezone:    row.Timezone,
		IsActive:    row.IsActive,
		Metadata:    payload,
	})
	return err
}

// RememberWorkspaceBrowser persists a per-bot antmemo browser override.
func (m *Manager) RememberWorkspaceBrowser(ctx context.Context, botID string, cfg WorkspaceBrowserConfig) error {
	return m.updateBotWorkspaceBrowserPreference(ctx, botID, cfg, false)
}

// ClearWorkspaceBrowserPreference removes a bot's antmemo browser override,
// falling back to the global [browser] config on next resolution.
func (m *Manager) ClearWorkspaceBrowserPreference(ctx context.Context, botID string) error {
	return m.updateBotWorkspaceBrowserPreference(ctx, botID, WorkspaceBrowserConfig{}, true)
}

// ResolveWorkspaceBrowser returns the effective antmemo browser config for a
// bot: global [browser] defaults overridden by any workspace.browser.*
// metadata the bot has set.
func (m *Manager) ResolveWorkspaceBrowser(ctx context.Context, botID string) (WorkspaceBrowserConfig, error) {
	pref, hasPref, err := m.botWorkspaceBrowserPreference(ctx, botID)
	if err != nil {
		return WorkspaceBrowserConfig{}, err
	}
	cfg := WorkspaceBrowserConfig{
		FingerprintPlatform: m.browser.DefaultFingerprintPlatformOrDefault(),
		ProxyMode:           m.browser.DefaultProxyModeOrDefault(),
	}
	if !hasPref {
		return cfg, nil
	}
	if strings.TrimSpace(pref.FingerprintPlatform) != "" {
		cfg.FingerprintPlatform = strings.TrimSpace(pref.FingerprintPlatform)
	}
	if strings.TrimSpace(pref.ProxyMode) != "" {
		cfg.ProxyMode = strings.TrimSpace(pref.ProxyMode)
	}
	cfg.FingerprintSeed = pref.FingerprintSeed
	cfg.ExtraFingerprintArgs = pref.ExtraFingerprintArgs
	cfg.ProxyServerURL = strings.TrimSpace(pref.ProxyServerURL)
	return cfg, nil
}

func (m *Manager) botWorkspaceBrowserPreference(ctx context.Context, botID string) (WorkspaceBrowserConfig, bool, error) {
	if m.queries == nil {
		return WorkspaceBrowserConfig{}, false, nil
	}
	botUUID, err := db.ParseUUID(botID)
	if err != nil {
		return WorkspaceBrowserConfig{}, false, err
	}
	row, err := m.queries.GetBotByID(ctx, botUUID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return WorkspaceBrowserConfig{}, false, nil
		}
		return WorkspaceBrowserConfig{}, false, err
	}
	metadata, err := decodeBotMetadata(row.Metadata)
	if err != nil {
		return WorkspaceBrowserConfig{}, false, err
	}
	cfg, ok := workspaceBrowserFromMetadata(metadata)
	return cfg, ok, nil
}

// resolveWorkspaceBrowserProvisionJSON merges the global [browser] config
// with this bot's workspace.browser.* metadata override (if any) and
// returns a JSON object matching the override-only subset of
// cmd/bridge/browser.go's browserProvisionRequest fields. An empty string
// means "no overrides" — the container falls back entirely to
// defaultBrowserProvisionRequest() inside the workspace.
func (m *Manager) resolveWorkspaceBrowserProvisionJSON(ctx context.Context, botID string) (string, error) {
	cfg, err := m.ResolveWorkspaceBrowser(ctx, botID)
	if err != nil {
		return "", err
	}

	fingerprintArgs := make([]string, 0, len(cfg.ExtraFingerprintArgs)+2)
	fingerprintArgs = append(fingerprintArgs, "--fingerprint-platform="+cfg.FingerprintPlatform)
	if cfg.FingerprintSeed != "" {
		fingerprintArgs = append(fingerprintArgs, "--fingerprint="+cfg.FingerprintSeed)
	}
	fingerprintArgs = append(fingerprintArgs, cfg.ExtraFingerprintArgs...)

	override := map[string]any{"fingerprintArgs": fingerprintArgs}
	if cfg.ProxyMode != "direct" && cfg.ProxyServerURL != "" {
		override["proxyConfig"] = cfg.ProxyServerURL
	}

	payload, err := json.Marshal(override)
	if err != nil {
		return "", err
	}
	return string(payload), nil
}
