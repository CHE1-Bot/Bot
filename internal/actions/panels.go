package actions

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/bwmarrin/discordgo"

	"github.com/che1/bot/internal/worker"
)

func (h *Handlers) sendMessage(_ context.Context, t worker.Task) (map[string]any, error) {
	var p struct {
		ChannelID string `json:"channel_id"`
		Content   string `json:"content"`
	}
	if err := decode(t.Input, &p); err != nil {
		return nil, err
	}
	if p.ChannelID == "" || p.Content == "" {
		return nil, fmt.Errorf("send_message: channel_id and content required")
	}
	msg, err := h.Session.ChannelMessageSend(p.ChannelID, p.Content)
	if err != nil {
		return nil, err
	}
	return map[string]any{"message_id": msg.ID, "channel_id": msg.ChannelID}, nil
}

func (h *Handlers) sendApplicationPanel(_ context.Context, t worker.Task) (map[string]any, error) {
	var p struct {
		ChannelID   string `json:"channel_id"`
		Title       string `json:"title"`
		Description string `json:"description"`
		FormID      string `json:"form_id"`
		Role        string `json:"role"`
		ButtonLabel string `json:"button_label"`
	}
	if err := decode(t.Input, &p); err != nil {
		return nil, err
	}
	if p.ButtonLabel == "" {
		p.ButtonLabel = "Apply"
	}
	customID := "apply:" + p.Role
	if p.FormID != "" {
		customID = "apply:form:" + p.FormID
	}
	msg, err := h.Session.ChannelMessageSendComplex(p.ChannelID, &discordgo.MessageSend{
		Embeds: []*discordgo.MessageEmbed{{
			Title:       p.Title,
			Description: p.Description,
			Color:       0x5865F2,
		}},
		Components: []discordgo.MessageComponent{
			discordgo.ActionsRow{Components: []discordgo.MessageComponent{
				discordgo.Button{
					Label:    p.ButtonLabel,
					Style:    discordgo.PrimaryButton,
					CustomID: customID,
				},
			}},
		},
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"message_id": msg.ID}, nil
}

func (h *Handlers) sendTicketPanel(_ context.Context, t worker.Task) (map[string]any, error) {
	var p struct {
		ChannelID   string `json:"channel_id"`
		Title       string `json:"title"`
		Description string `json:"description"`
		ButtonLabel string `json:"button_label"`
		PanelID     string `json:"panel_id"`
		CategoryID  string `json:"category_id"`
	}
	if err := decode(t.Input, &p); err != nil {
		return nil, err
	}
	if p.ButtonLabel == "" {
		p.ButtonLabel = "Open ticket"
	}
	customID := "ticket:open"
	switch {
	case p.CategoryID != "":
		customID = "ticket:open:cat:" + p.CategoryID
	case p.PanelID != "":
		customID = "ticket:open:panel:" + p.PanelID
	}
	msg, err := h.Session.ChannelMessageSendComplex(p.ChannelID, &discordgo.MessageSend{
		Embeds: []*discordgo.MessageEmbed{{
			Title:       p.Title,
			Description: p.Description,
			Color:       0x57F287,
		}},
		Components: []discordgo.MessageComponent{
			discordgo.ActionsRow{Components: []discordgo.MessageComponent{
				discordgo.Button{
					Label:    p.ButtonLabel,
					Style:    discordgo.SuccessButton,
					CustomID: customID,
				},
			}},
		},
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"message_id": msg.ID}, nil
}

func (h *Handlers) sendGiveawayPanel(_ context.Context, t worker.Task) (map[string]any, error) {
	var p struct {
		ChannelID   string `json:"channel_id"`
		Prize       string `json:"prize"`
		Description string `json:"description"`
		EndsAtUnix  int64  `json:"ends_at_unix"`
	}
	if err := decode(t.Input, &p); err != nil {
		return nil, err
	}
	desc := p.Description
	if p.EndsAtUnix > 0 {
		desc = fmt.Sprintf("%s\n\nEnds <t:%d:R>", desc, p.EndsAtUnix)
	}
	msg, err := h.Session.ChannelMessageSendComplex(p.ChannelID, &discordgo.MessageSend{
		Embeds: []*discordgo.MessageEmbed{{
			Title:       "🎉 " + p.Prize,
			Description: desc,
			Color:       0xEB459E,
		}},
	})
	if err != nil {
		return nil, err
	}
	if err := h.Session.MessageReactionAdd(msg.ChannelID, msg.ID, "🎉"); err != nil {
		slog.Warn("giveaway reaction failed", "err", err)
	}
	return map[string]any{"message_id": msg.ID}, nil
}
