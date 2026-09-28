package tools

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	sdk "github.com/felinics/twilight/sdk"
)

// BotDesignerMetadataKey marks a bot as a "bot designer": a bot that may
// create and edit other bots for its owner through conversation.
const BotDesignerMetadataKey = "bot_designer"

// maxBotDesignerPersonaBytes caps the AGENTS.md persona a designer can write.
const maxBotDesignerPersonaBytes = 32 * 1024

// BotDesignerBot is the tool-facing projection of a bot.
type BotDesignerBot struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	AvatarURL   string `json:"avatar_url,omitempty"`
	Status      string `json:"status,omitempty"`
	ChatModelID string `json:"chat_model_id,omitempty"`
}

// BotDesignerCreateInput is the validated input for creating a bot.
type BotDesignerCreateInput struct {
	OwnerUserID string
	DisplayName string
	Name        string
	AvatarURL   string
	Timezone    string
	Persona     string
	ChatModelID string
}

// BotDesignerBackend is implemented in the composition root so this package
// does not depend on the bots, settings, or workspace packages.
type BotDesignerBackend interface {
	IsBotDesigner(ctx context.Context, botID string) (bool, error)
	ListOwnedBots(ctx context.Context, ownerUserID string) ([]BotDesignerBot, error)
	CreateBot(ctx context.Context, input BotDesignerCreateInput) (BotDesignerBot, error)
	UpdatePersona(ctx context.Context, ownerUserID, botID, persona string) (BotDesignerBot, error)
}

type BotDesignerProvider struct {
	backend BotDesignerBackend
	logger  *slog.Logger
}

func NewBotDesignerProvider(log *slog.Logger, backend BotDesignerBackend) *BotDesignerProvider {
	if log == nil {
		log = slog.Default()
	}
	return &BotDesignerProvider{
		backend: backend,
		logger:  log.With(slog.String("tool", "bot_designer")),
	}
}

func (*BotDesignerProvider) Usage(_ context.Context, session SessionContext, available AvailableTools) string {
	createRef, ok := available.Ref(ToolBotDesignerCreate())
	if !ok {
		return ""
	}
	parts := []string{
		"You can design and create new bots for the user. Interview the user first: purpose, audience, tone, language, and any hard rules.",
		"Draft the new bot's AGENTS.md persona (sections: Role, Identity and Voice, Behavior, Workspace, Communication) and show a short summary before creating it.",
		"Only call " + createRef + " with `confirmed: true` after the user explicitly agreed to the final name and persona.",
	}
	if askRef, ok := available.Ref(ToolAskUser()); ok && session.CanAskUser() {
		parts = append(parts, "Use "+askRef+" for the final yes/no confirmation.")
	}
	if listRef, ok := available.Ref(ToolBotDesignerList()); ok {
		parts = append(parts, "Use "+listRef+" to find existing bots before editing one.")
	}
	if updateRef, ok := available.Ref(ToolBotDesignerUpdatePersona()); ok {
		parts = append(parts, "Use "+updateRef+" to replace the persona of a bot the user owns; it overwrites the whole AGENTS.md.")
	}
	return usageSection("Bot Designer", parts)
}

func (p *BotDesignerProvider) Tools(ctx context.Context, session SessionContext) ([]sdk.Tool, error) {
	if p == nil || p.backend == nil || session.IsSubagent {
		return nil, nil
	}
	botID := strings.TrimSpace(session.BotID)
	if botID == "" {
		return nil, nil
	}
	enabled, err := p.backend.IsBotDesigner(ctx, botID)
	if err != nil {
		p.logger.Warn("bot designer check failed", slog.String("bot_id", botID), slog.Any("error", err))
		return nil, nil
	}
	if !enabled {
		return nil, nil
	}
	sess := session
	return []sdk.Tool{
		{
			Name:        ToolBotDesignerList().String(),
			Description: "List the bots owned by the current user (id, name, display name, status).",
			Parameters: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
				"required":   []string{},
			},
			Execute: func(ctx *sdk.ToolExecContext, _ any) (any, error) {
				owner, err := botDesignerOwner(sess)
				if err != nil {
					return nil, err
				}
				items, err := p.backend.ListOwnedBots(ctx.Context, owner)
				if err != nil {
					return nil, err
				}
				return map[string]any{"ok": true, "count": len(items), "bots": items}, nil
			},
		},
		{
			Name:        ToolBotDesignerCreate().String(),
			Description: "Create a new bot owned by the current user, with its own workspace and the given AGENTS.md persona. Requires explicit user confirmation first.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"display_name": map[string]any{
						"type":        "string",
						"description": "Human-readable bot name shown in the bot list.",
					},
					"name": map[string]any{
						"type":        "string",
						"description": "Optional URL slug (lowercase letters, digits, dashes). Derived from display_name when omitted.",
					},
					"persona": map[string]any{
						"type":        "string",
						"description": "Full markdown content of the new bot's /data/AGENTS.md.",
					},
					"avatar_url": map[string]any{
						"type":        "string",
						"description": "Optional avatar image URL.",
					},
					"timezone": map[string]any{
						"type":        "string",
						"description": "Optional IANA timezone, e.g. Asia/Tokyo.",
					},
					"chat_model_id": map[string]any{
						"type":        "string",
						"description": "Optional chat model UUID. Defaults to the model used in this conversation.",
					},
					"confirmed": map[string]any{
						"type":        "boolean",
						"description": "Must be true, and only after the user explicitly approved creating this bot.",
					},
				},
				"required": []string{"display_name", "persona", "confirmed"},
			},
			Execute: func(ctx *sdk.ToolExecContext, input any) (any, error) {
				args := inputAsMap(input)
				confirmed, _, err := BoolArg(args, "confirmed")
				if err != nil {
					return nil, err
				}
				if !confirmed {
					return nil, errors.New("ask the user to confirm the bot name and persona, then call again with confirmed=true")
				}
				owner, err := botDesignerOwner(sess)
				if err != nil {
					return nil, err
				}
				createInput := BotDesignerCreateInput{
					OwnerUserID: owner,
					DisplayName: StringArg(args, "display_name"),
					Name:        StringArg(args, "name"),
					AvatarURL:   StringArg(args, "avatar_url"),
					Timezone:    StringArg(args, "timezone"),
					Persona:     StringArg(args, "persona"),
					ChatModelID: FirstStringArg(args, "chat_model_id"),
				}
				if createInput.ChatModelID == "" {
					createInput.ChatModelID = strings.TrimSpace(sess.CurrentModelUUID)
				}
				if err := validateBotDesignerPersona(createInput.Persona); err != nil {
					return nil, err
				}
				if createInput.DisplayName == "" {
					return nil, errors.New("display_name is required")
				}
				bot, err := p.backend.CreateBot(ctx.Context, createInput)
				if err != nil {
					return nil, err
				}
				return map[string]any{"ok": true, "bot": bot}, nil
			},
		},
		{
			Name:        ToolBotDesignerUpdatePersona().String(),
			Description: "Replace the /data/AGENTS.md persona of a bot owned by the current user.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"bot_id": map[string]any{
						"type":        "string",
						"description": "Target bot id or name, from " + ToolBotDesignerList().String() + ".",
					},
					"persona": map[string]any{
						"type":        "string",
						"description": "Full new markdown content of AGENTS.md.",
					},
				},
				"required": []string{"bot_id", "persona"},
			},
			Execute: func(ctx *sdk.ToolExecContext, input any) (any, error) {
				args := inputAsMap(input)
				owner, err := botDesignerOwner(sess)
				if err != nil {
					return nil, err
				}
				target := StringArg(args, "bot_id")
				if target == "" {
					return nil, errors.New("bot_id is required")
				}
				persona := StringArg(args, "persona")
				if err := validateBotDesignerPersona(persona); err != nil {
					return nil, err
				}
				bot, err := p.backend.UpdatePersona(ctx.Context, owner, target, persona)
				if err != nil {
					return nil, err
				}
				return map[string]any{"ok": true, "bot": bot}, nil
			},
		},
	}, nil
}

func botDesignerOwner(session SessionContext) (string, error) {
	owner := strings.TrimSpace(session.UserID)
	if owner == "" {
		owner = strings.TrimSpace(session.ChannelIdentityID)
	}
	if owner == "" {
		return "", errors.New("bot designer requires a signed-in user")
	}
	return owner, nil
}

func validateBotDesignerPersona(persona string) error {
	if strings.TrimSpace(persona) == "" {
		return errors.New("persona is required")
	}
	if len(persona) > maxBotDesignerPersonaBytes {
		return errors.New("persona is too long (max 32KB)")
	}
	return nil
}
