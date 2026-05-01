package actions

import (
	"context"
	"fmt"

	"github.com/bwmarrin/discordgo"

	"github.com/che1/bot/internal/worker"
)

// ticket mirrors the Dashboard's Ticket type. We unmarshal what we use
// and ignore unknown fields.
type ticket struct {
	ID            string `json:"id"`
	GuildID       string `json:"guild_id"`
	ChannelID     string `json:"channel_id"`
	UserID        string `json:"user_id"`
	Username      string `json:"username"`
	Subject       string `json:"subject"`
	Status        string `json:"status"` // open|closed
	CategoryID    string `json:"category_id"`
	CategoryName  string `json:"category_name"`
	ClaimedBy     string `json:"claimed_by"`
	ClaimedByName string `json:"claimed_by_name"`
	NamingPattern string `json:"naming_pattern"`
}

func (h *Handlers) ticketsCreate(_ context.Context, t worker.Task) (map[string]any, error) {
	var tk ticket
	if err := decode(t.Input, &tk); err != nil {
		return nil, err
	}
	if tk.GuildID == "" || tk.UserID == "" {
		return nil, fmt.Errorf("tickets.create: guild_id and user_id required")
	}

	name := tk.NamingPattern
	if name == "" {
		uid := tk.UserID
		if len(uid) > 6 {
			uid = uid[:6]
		}
		name = "ticket-" + uid
	}

	ch, err := h.Session.GuildChannelCreateComplex(tk.GuildID, discordgo.GuildChannelCreateData{
		Name: name,
		Type: discordgo.ChannelTypeGuildText,
		PermissionOverwrites: []*discordgo.PermissionOverwrite{
			{ID: tk.GuildID, Type: discordgo.PermissionOverwriteTypeRole, Deny: discordgo.PermissionViewChannel},
			{ID: tk.UserID, Type: discordgo.PermissionOverwriteTypeMember, Allow: discordgo.PermissionViewChannel | discordgo.PermissionSendMessages},
		},
	})
	if err != nil {
		return nil, err
	}

	greeting := "Hello <@" + tk.UserID + ">, a staff member will be with you shortly."
	if tk.Subject != "" {
		greeting += "\n\n**Subject:** " + tk.Subject
	}
	_, _ = h.Session.ChannelMessageSend(ch.ID, greeting)

	return map[string]any{"channel_id": ch.ID, "ticket_id": tk.ID}, nil
}

func (h *Handlers) ticketsUpdate(_ context.Context, t worker.Task) (map[string]any, error) {
	var tk ticket
	if err := decode(t.Input, &tk); err != nil {
		return nil, err
	}
	if tk.ChannelID == "" {
		return nil, fmt.Errorf("tickets.update: channel_id required")
	}

	switch tk.Status {
	case "closed":
		if _, err := h.Session.ChannelDelete(tk.ChannelID); err != nil {
			return nil, fmt.Errorf("close ticket channel: %w", err)
		}
		return map[string]any{"closed": true}, nil
	case "open", "":
		// Channel rename if a new name is provided.
		if tk.NamingPattern != "" {
			if _, err := h.Session.ChannelEdit(tk.ChannelID, &discordgo.ChannelEdit{Name: tk.NamingPattern}); err != nil {
				return nil, err
			}
		}
		return map[string]any{"updated": true}, nil
	}
	return map[string]any{"noop": true}, nil
}

func (h *Handlers) ticketsClaim(_ context.Context, t worker.Task) (map[string]any, error) {
	var tk ticket
	if err := decode(t.Input, &tk); err != nil {
		return nil, err
	}
	if tk.ChannelID == "" {
		return nil, fmt.Errorf("tickets.claim: channel_id required")
	}
	staff := tk.ClaimedByName
	if staff == "" && tk.ClaimedBy != "" {
		staff = "<@" + tk.ClaimedBy + ">"
	}
	if staff == "" {
		staff = "a staff member"
	}
	msg, err := h.Session.ChannelMessageSend(tk.ChannelID, "🎫 Ticket claimed by "+staff+".")
	if err != nil {
		return nil, err
	}
	return map[string]any{"message_id": msg.ID}, nil
}

func (h *Handlers) ticketsUnclaim(_ context.Context, t worker.Task) (map[string]any, error) {
	var tk ticket
	if err := decode(t.Input, &tk); err != nil {
		return nil, err
	}
	if tk.ChannelID == "" {
		return nil, fmt.Errorf("tickets.unclaim: channel_id required")
	}
	msg, err := h.Session.ChannelMessageSend(tk.ChannelID, "🎫 Ticket released back to the queue.")
	if err != nil {
		return nil, err
	}
	return map[string]any{"message_id": msg.ID}, nil
}
