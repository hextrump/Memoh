package tools

import (
	"context"
	"strings"
	"testing"

	sdk "github.com/felinics/twilight/sdk"
)

type fakeBotDesignerBackend struct {
	designer bool
	created  []BotDesignerCreateInput
}

func (f *fakeBotDesignerBackend) IsBotDesigner(context.Context, string) (bool, error) {
	return f.designer, nil
}

func (*fakeBotDesignerBackend) ListOwnedBots(context.Context, string) ([]BotDesignerBot, error) {
	return []BotDesignerBot{{ID: "b1", Name: "one"}}, nil
}

func (f *fakeBotDesignerBackend) CreateBot(_ context.Context, input BotDesignerCreateInput) (BotDesignerBot, error) {
	f.created = append(f.created, input)
	return BotDesignerBot{ID: "new", Name: "new-bot", DisplayName: input.DisplayName}, nil
}

func (*fakeBotDesignerBackend) UpdatePersona(_ context.Context, _, botID, _ string) (BotDesignerBot, error) {
	return BotDesignerBot{ID: botID}, nil
}

func findTool(t *testing.T, items []sdk.Tool, name ToolName) sdk.Tool {
	t.Helper()
	for _, item := range items {
		if item.Name == name.String() {
			return item
		}
	}
	t.Fatalf("tool %s not registered", name.String())
	return sdk.Tool{}
}

func TestBotDesignerToolsGated(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	plain := NewBotDesignerProvider(nil, &fakeBotDesignerBackend{})
	if items, _ := plain.Tools(ctx, SessionContext{BotID: "bot", UserID: "u"}); len(items) != 0 {
		t.Fatalf("non-designer bot got %d tools", len(items))
	}
	designer := NewBotDesignerProvider(nil, &fakeBotDesignerBackend{designer: true})
	if items, _ := designer.Tools(ctx, SessionContext{BotID: "bot", UserID: "u", IsSubagent: true}); len(items) != 0 {
		t.Fatalf("subagent session got %d tools", len(items))
	}
	if items, _ := designer.Tools(ctx, SessionContext{BotID: "bot", UserID: "u"}); len(items) != 3 {
		t.Fatalf("designer bot got %d tools, want 3", len(items))
	}
}

func TestBotDesignerCreateRequiresConfirmation(t *testing.T) {
	t.Parallel()

	backend := &fakeBotDesignerBackend{designer: true}
	provider := NewBotDesignerProvider(nil, backend)
	items, err := provider.Tools(context.Background(), SessionContext{BotID: "bot", UserID: "u", CurrentModelUUID: "model-1"})
	if err != nil {
		t.Fatal(err)
	}
	create := findTool(t, items, ToolBotDesignerCreate())
	execCtx := &sdk.ToolExecContext{Context: context.Background()}

	_, err = create.Execute(execCtx, map[string]any{"display_name": "Coin", "persona": "# AGENTS.md", "confirmed": false})
	if err == nil || !strings.Contains(err.Error(), "confirm") {
		t.Fatalf("unconfirmed create error = %v", err)
	}
	if len(backend.created) != 0 {
		t.Fatal("unconfirmed create reached the backend")
	}

	if _, err := create.Execute(execCtx, map[string]any{"display_name": "Coin", "persona": "# AGENTS.md", "confirmed": true}); err != nil {
		t.Fatal(err)
	}
	if len(backend.created) != 1 {
		t.Fatalf("created %d bots, want 1", len(backend.created))
	}
	got := backend.created[0]
	if got.OwnerUserID != "u" || got.ChatModelID != "model-1" || got.DisplayName != "Coin" {
		t.Fatalf("unexpected create input: %+v", got)
	}
}
