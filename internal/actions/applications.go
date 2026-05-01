package actions

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/che1/bot/internal/worker"
)

// applicationDecision matches the Dashboard catalog payload:
// {application_id, user_id, form_id, reviewer, reason, dm, grant_role_id}.
type applicationDecision struct {
	ApplicationID string `json:"application_id"`
	UserID        string `json:"user_id"`
	GuildID       string `json:"guild_id"`
	FormID        string `json:"form_id"`
	Reviewer      string `json:"reviewer"`
	Reason        string `json:"reason"`
	DM            string `json:"dm"`
	GrantRoleID   string `json:"grant_role_id"`
}

func (h *Handlers) applicationsAccepted(_ context.Context, t worker.Task) (map[string]any, error) {
	var d applicationDecision
	if err := decode(t.Input, &d); err != nil {
		return nil, err
	}
	if d.UserID == "" {
		return nil, fmt.Errorf("applications.accepted: user_id required")
	}

	dm := d.DM
	if dm == "" {
		dm = "Your application has been accepted. Welcome aboard!"
	}
	if err := h.dmUser(d.UserID, dm); err != nil {
		slog.Warn("application accept DM failed", "user_id", d.UserID, "err", err)
	}

	if d.GuildID != "" && d.GrantRoleID != "" {
		if err := h.Session.GuildMemberRoleAdd(d.GuildID, d.UserID, d.GrantRoleID); err != nil {
			return nil, fmt.Errorf("grant role: %w", err)
		}
	}
	return map[string]any{"application_id": d.ApplicationID, "user_id": d.UserID}, nil
}

func (h *Handlers) applicationsRejected(_ context.Context, t worker.Task) (map[string]any, error) {
	var d applicationDecision
	if err := decode(t.Input, &d); err != nil {
		return nil, err
	}
	if d.UserID == "" {
		return nil, fmt.Errorf("applications.rejected: user_id required")
	}

	dm := d.DM
	if dm == "" {
		dm = "Unfortunately your application was not accepted at this time."
		if d.Reason != "" {
			dm += "\n\n**Reason:** " + d.Reason
		}
	}
	if err := h.dmUser(d.UserID, dm); err != nil {
		slog.Warn("application reject DM failed", "user_id", d.UserID, "err", err)
	}
	return map[string]any{"application_id": d.ApplicationID, "user_id": d.UserID}, nil
}

func (h *Handlers) dmUser(userID, content string) error {
	ch, err := h.Session.UserChannelCreate(userID)
	if err != nil {
		return err
	}
	_, err = h.Session.ChannelMessageSend(ch.ID, content)
	return err
}
