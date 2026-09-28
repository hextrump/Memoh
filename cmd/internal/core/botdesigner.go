package core

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path"
	"strings"

	agenttools "github.com/felinics/memoh/internal/agent/tool"
	"github.com/felinics/memoh/internal/bots"
	"github.com/felinics/memoh/internal/config"
	dbstore "github.com/felinics/memoh/internal/db/store"
	"github.com/felinics/memoh/internal/models"
	"github.com/felinics/memoh/internal/settings"
	"github.com/felinics/memoh/internal/workspace"
	"github.com/felinics/memoh/templates"
)

// botDesignerBackend adapts the bots, settings, and workspace services onto
// the bot designer tool's local backend interface.
type botDesignerBackend struct {
	bots     *bots.Service
	settings *settings.Service
	manager  *workspace.Manager
}

func newBotDesignerBackend(botService *bots.Service, settingsService *settings.Service, manager *workspace.Manager) agenttools.BotDesignerBackend {
	if botService == nil || settingsService == nil || manager == nil {
		return nil
	}
	return &botDesignerBackend{bots: botService, settings: settingsService, manager: manager}
}

func (b *botDesignerBackend) IsBotDesigner(ctx context.Context, botID string) (bool, error) {
	bot, err := b.bots.Get(ctx, botID)
	if err != nil {
		return false, err
	}
	return isBotDesigner(bot), nil
}

func (b *botDesignerBackend) ListOwnedBots(ctx context.Context, ownerUserID string) ([]agenttools.BotDesignerBot, error) {
	items, err := b.bots.ListByOwner(ctx, ownerUserID)
	if err != nil {
		return nil, err
	}
	out := make([]agenttools.BotDesignerBot, 0, len(items))
	for _, item := range items {
		out = append(out, toBotDesignerBot(item, ""))
	}
	return out, nil
}

func (b *botDesignerBackend) CreateBot(ctx context.Context, input agenttools.BotDesignerCreateInput) (agenttools.BotDesignerBot, error) {
	req := bots.CreateBotRequest{
		Name:         input.Name,
		DisplayName:  input.DisplayName,
		AvatarURL:    input.AvatarURL,
		Metadata:     map[string]any{"created_by": "bot_designer"},
		WaitForReady: true,
	}
	if tz := strings.TrimSpace(input.Timezone); tz != "" {
		req.Timezone = &tz
	}
	bot, err := b.bots.Create(ctx, input.OwnerUserID, req)
	if err != nil {
		return agenttools.BotDesignerBot{}, err
	}
	var errs []error
	if modelID := strings.TrimSpace(input.ChatModelID); modelID != "" {
		if _, err := b.settings.UpsertBot(ctx, bot.ID, settings.UpsertRequest{ChatModelID: &modelID}); err != nil {
			errs = append(errs, fmt.Errorf("set chat model: %w", err))
		}
	}
	if err := b.writePersona(ctx, bot.ID, input.Persona); err != nil {
		errs = append(errs, err)
	}
	if len(errs) > 0 {
		// The bot exists; report partial failure so the designer can retry
		// the persona or model step instead of creating a duplicate.
		return toBotDesignerBot(bot, input.ChatModelID), fmt.Errorf("bot %s created, but setup is incomplete: %w", bot.Name, errors.Join(errs...))
	}
	return toBotDesignerBot(bot, input.ChatModelID), nil
}

func (b *botDesignerBackend) UpdatePersona(ctx context.Context, ownerUserID, botID, persona string) (agenttools.BotDesignerBot, error) {
	bot, err := b.bots.AuthorizeAccessWithPermission(ctx, ownerUserID, botID, false, bots.PermissionManage)
	if err != nil {
		return agenttools.BotDesignerBot{}, err
	}
	if err := b.writePersona(ctx, bot.ID, persona); err != nil {
		return agenttools.BotDesignerBot{}, err
	}
	return toBotDesignerBot(bot, ""), nil
}

func (b *botDesignerBackend) writePersona(ctx context.Context, botID, persona string) error {
	return writeBotPersona(ctx, b.manager, botID, persona)
}

// writeBotPersona overwrites the bot's workspace AGENTS.md, which the agent
// runtime loads as the bot's instructions on every turn.
func writeBotPersona(ctx context.Context, manager *workspace.Manager, botID, persona string) error {
	client, err := manager.MCPClient(ctx, botID)
	if err != nil {
		return fmt.Errorf("connect workspace: %w", err)
	}
	content := strings.TrimSpace(persona) + "\n"
	if err := client.WriteFile(ctx, path.Join(config.DefaultDataMount, "AGENTS.md"), []byte(content)); err != nil {
		return fmt.Errorf("write AGENTS.md: %w", err)
	}
	return nil
}

func toBotDesignerBot(bot bots.Bot, chatModelID string) agenttools.BotDesignerBot {
	return agenttools.BotDesignerBot{
		ID:          bot.ID,
		Name:        bot.Name,
		DisplayName: bot.DisplayName,
		AvatarURL:   bot.AvatarURL,
		Status:      bot.Status,
		ChatModelID: chatModelID,
	}
}

func isBotDesigner(bot bots.Bot) bool {
	enabled, _ := bot.Metadata[agenttools.BotDesignerMetadataKey].(bool)
	return enabled
}

const (
	botDesignerName        = "bot-designer"
	botDesignerDisplayName = "Bot 设计师"
)

// EnsureBotDesigner seeds the built-in Bot Designer bot for the configured
// admin. It is idempotent: an admin-owned bot flagged as designer (or named
// bot-designer) short-circuits. Missing chat models skip seeding until the
// next start.
func EnsureBotDesigner(ctx context.Context, log *slog.Logger, cfg config.Config, accountStore dbstore.AccountStore, botService *bots.Service, settingsService *settings.Service, modelsService *models.Service, manager *workspace.Manager) error {
	if !cfg.BotDesigner.IsEnabled() {
		return nil
	}
	if accountStore == nil || botService == nil || settingsService == nil || modelsService == nil || manager == nil {
		return errors.New("bot designer seed dependencies not configured")
	}
	username := strings.TrimSpace(cfg.Admin.Username)
	if username == "" {
		return nil
	}
	admin, err := accountStore.GetByIdentity(ctx, username)
	if err != nil {
		return fmt.Errorf("find admin account: %w", err)
	}
	owned, err := botService.ListByOwner(ctx, admin.ID)
	if err != nil {
		return fmt.Errorf("list admin bots: %w", err)
	}
	for _, bot := range owned {
		if isBotDesigner(bot) || bot.Name == botDesignerName {
			return nil
		}
	}
	modelID, err := resolveBotDesignerModel(ctx, modelsService, cfg.BotDesigner.ModelID)
	if err != nil {
		return err
	}
	if modelID == "" {
		log.Info("bot designer seed skipped: no enabled chat model yet")
		return nil
	}
	bot, err := botService.Create(ctx, admin.ID, bots.CreateBotRequest{
		Name:         botDesignerName,
		DisplayName:  botDesignerDisplayName,
		Metadata:     map[string]any{agenttools.BotDesignerMetadataKey: true},
		WaitForReady: true,
	})
	if err != nil {
		return fmt.Errorf("create bot designer: %w", err)
	}
	if _, err := settingsService.UpsertBot(ctx, bot.ID, settings.UpsertRequest{ChatModelID: &modelID}); err != nil {
		return fmt.Errorf("set bot designer model: %w", err)
	}
	if err := writeBotPersona(ctx, manager, bot.ID, templates.BotDesignerPersona); err != nil {
		return err
	}
	log.Info("bot designer seeded", slog.String("bot_id", bot.ID), slog.String("model_id", modelID))
	return nil
}

// resolveBotDesignerModel returns the model UUID for the configured id, or the
// first enabled chat model when none is configured.
func resolveBotDesignerModel(ctx context.Context, modelsService *models.Service, configured string) (string, error) {
	items, err := modelsService.ListEnabledByType(ctx, models.ModelTypeChat)
	if err != nil {
		return "", fmt.Errorf("list chat models: %w", err)
	}
	configured = strings.TrimSpace(configured)
	for _, item := range items {
		if configured == "" || item.ID == configured || item.ModelID == configured {
			return item.ID, nil
		}
	}
	if configured != "" {
		return "", fmt.Errorf("bot_designer.model_id %q is not an enabled chat model", configured)
	}
	return "", nil
}
