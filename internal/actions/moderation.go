package actions

import (
	"context"
	"fmt"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/che1/bot/internal/worker"
)

// modLog mirrors the Dashboard's ModLog payload. Action is one of
// kick|ban|unban|mute|unmute|warn for the unified `moderation.action`
// kind; for split kinds it's redundant.
type modLog struct {
	GuildID       string `json:"guild_id"`
	ModeratorID   string `json:"moderator_id"`
	ModeratorName string `json:"moderator_name"`
	TargetID      string `json:"target_id"`
	TargetName    string `json:"target_name"`
	Action        string `json:"action"`
	Reason        string `json:"reason"`
	DurationSec   int    `json:"duration_sec"`
	NotifyChannel string `json:"notify_channel_id"`
}

func (h *Handlers) moderationAction(ctx context.Context, t worker.Task) (map[string]any, error) {
	var m modLog
	if err := decode(t.Input, &m); err != nil {
		return nil, err
	}
	return h.applyMod(ctx, m, m.Action)
}

func (h *Handlers) moderationKick(ctx context.Context, t worker.Task) (map[string]any, error) {
	var m modLog
	if err := decode(t.Input, &m); err != nil {
		return nil, err
	}
	return h.applyMod(ctx, m, "kick")
}

func (h *Handlers) moderationBan(ctx context.Context, t worker.Task) (map[string]any, error) {
	var m modLog
	if err := decode(t.Input, &m); err != nil {
		return nil, err
	}
	return h.applyMod(ctx, m, "ban")
}

func (h *Handlers) moderationUnban(ctx context.Context, t worker.Task) (map[string]any, error) {
	var m modLog
	if err := decode(t.Input, &m); err != nil {
		return nil, err
	}
	return h.applyMod(ctx, m, "unban")
}

func (h *Handlers) moderationMute(ctx context.Context, t worker.Task) (map[string]any, error) {
	var m modLog
	if err := decode(t.Input, &m); err != nil {
		return nil, err
	}
	return h.applyMod(ctx, m, "mute")
}

func (h *Handlers) moderationUnmute(ctx context.Context, t worker.Task) (map[string]any, error) {
	var m modLog
	if err := decode(t.Input, &m); err != nil {
		return nil, err
	}
	return h.applyMod(ctx, m, "unmute")
}

func (h *Handlers) moderationWarn(ctx context.Context, t worker.Task) (map[string]any, error) {
	var m modLog
	if err := decode(t.Input, &m); err != nil {
		return nil, err
	}
	return h.applyMod(ctx, m, "warn")
}

func (h *Handlers) applyMod(_ context.Context, m modLog, action string) (map[string]any, error) {
	if m.GuildID == "" || m.TargetID == "" {
		return nil, fmt.Errorf("moderation: guild_id and target_id required")
	}

	var actionErr error
	switch action {
	case "kick":
		actionErr = h.Session.GuildMemberDeleteWithReason(m.GuildID, m.TargetID, m.Reason)
	case "ban":
		actionErr = h.Session.GuildBanCreateWithReason(m.GuildID, m.TargetID, m.Reason, 0)
	case "unban":
		actionErr = h.Session.GuildBanDelete(m.GuildID, m.TargetID)
	case "mute":
		until := time.Now().Add(time.Duration(maxInt(m.DurationSec, 60)) * time.Second)
		actionErr = h.Session.GuildMemberTimeout(m.GuildID, m.TargetID, &until)
	case "unmute":
		actionErr = h.Session.GuildMemberTimeout(m.GuildID, m.TargetID, nil)
	case "warn":
		// No Discord-side action; the Dashboard records the warning. Optionally
		// DM the user if a channel/DM mechanism is configured.
	default:
		return nil, fmt.Errorf("moderation: unknown action %q", action)
	}
	if actionErr != nil {
		return nil, fmt.Errorf("moderation %s: %w", action, actionErr)
	}

	if m.NotifyChannel != "" {
		_, _ = h.Session.ChannelMessageSendComplex(m.NotifyChannel, &discordgo.MessageSend{
			Embeds: []*discordgo.MessageEmbed{{
				Title:       "Moderation: " + action,
				Description: fmt.Sprintf("**Target:** <@%s>\n**Moderator:** %s\n**Reason:** %s", m.TargetID, fallback(m.ModeratorName, m.ModeratorID), fallback(m.Reason, "—")),
				Color:       0xED4245,
			}},
		})
	}

	return map[string]any{"action": action, "target_id": m.TargetID}, nil
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func fallback(s, alt string) string {
	if s == "" {
		return alt
	}
	return s
}
