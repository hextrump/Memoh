package bots

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/felinics/memoh/internal/db"
)

// activityPreviewMaxRunes caps the last-message preview shown in the bot list.
const activityPreviewMaxRunes = 120

// BotActivity is the per-bot last-activity summary used by the messenger-style bot list.
type BotActivity struct {
	BotID          string    `json:"bot_id"`
	SessionID      string    `json:"session_id,omitempty"`
	Role           string    `json:"role,omitempty"`
	PreviewText    string    `json:"preview_text,omitempty"`
	LastActivityAt time.Time `json:"last_activity_at"`
}

// ListBotActivityResponse wraps bot activity summaries, most recent first.
type ListBotActivityResponse struct {
	Items []BotActivity `json:"items"`
}

// ListActivity returns the latest visible message per bot. Bots without messages
// fall back to their updated_at timestamp. Items are sorted by recency.
func (s *Service) ListActivity(ctx context.Context, items []Bot) ([]BotActivity, error) {
	if s.queries == nil {
		return nil, errors.New("bot queries not configured")
	}
	ids := make([]pgtype.UUID, 0, len(items))
	for _, bot := range items {
		id, err := db.ParseUUID(bot.ID)
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	latest := map[string]BotActivity{}
	if len(ids) > 0 {
		rows, err := s.queries.ListBotLastMessages(ctx, ids)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			botID := uuid.UUID(row.BotID.Bytes).String()
			activity := BotActivity{
				BotID:          botID,
				Role:           row.Role,
				PreviewText:    messagePreview(db.TextToString(row.DisplayText), row.Content),
				LastActivityAt: db.TimeFromPg(row.CreatedAt),
			}
			if row.SessionID.Valid {
				activity.SessionID = uuid.UUID(row.SessionID.Bytes).String()
			}
			latest[botID] = activity
		}
	}
	out := make([]BotActivity, 0, len(items))
	for _, bot := range items {
		if activity, ok := latest[bot.ID]; ok {
			out = append(out, activity)
			continue
		}
		out = append(out, BotActivity{BotID: bot.ID, LastActivityAt: bot.UpdatedAt})
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].LastActivityAt.After(out[j].LastActivityAt)
	})
	return out, nil
}

// messagePreview prefers the stored display text, then the visible text parts
// of the model message content. Reasoning and tool parts are skipped.
func messagePreview(displayText string, content []byte) string {
	text := strings.TrimSpace(displayText)
	if text == "" && len(content) > 0 {
		var msg struct {
			Content json.RawMessage `json:"content"`
		}
		if err := json.Unmarshal(content, &msg); err == nil {
			var plain string
			if err := json.Unmarshal(msg.Content, &plain); err == nil {
				text = plain
			} else {
				var parts []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				}
				if err := json.Unmarshal(msg.Content, &parts); err == nil {
					texts := make([]string, 0, len(parts))
					for _, part := range parts {
						if part.Type == "text" && strings.TrimSpace(part.Text) != "" {
							texts = append(texts, strings.TrimSpace(part.Text))
						}
					}
					text = strings.Join(texts, " ")
				}
			}
		}
	}
	text = strings.Join(strings.Fields(text), " ")
	runes := []rune(text)
	if len(runes) > activityPreviewMaxRunes {
		return string(runes[:activityPreviewMaxRunes]) + "…"
	}
	return text
}
