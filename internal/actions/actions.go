// Package actions handles dashboard-driven Discord side-effects.
//
// The Dashboard creates tasks via Worker REST (POST /api/v1/tasks); the
// Worker emits task.created events on its WebSocket hub; the bot
// subscribes and runs the side-effect (post a message, render a panel, ...).
// The bot reports completion back via Worker.Complete, which the Dashboard
// observes through task.completed.
package actions

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/bwmarrin/discordgo"

	"github.com/che1/bot/internal/worker"
)

// Task kinds the bot performs on the Dashboard's behalf.
const (
	KindSendMessage          = "send_message"
	KindSendApplicationPanel = "send_application_panel"
	KindSendTicketPanel      = "send_ticket_panel"
	KindSendGiveawayPanel    = "send_giveaway_panel"
)

type Handlers struct {
	Session *discordgo.Session
}

// Register wires every supported task kind into the Subscriber, and logs
// settings updates so operators can see config changes propagating.
func (h *Handlers) Register(sub *worker.Subscriber) {
	sub.OnTask(KindSendMessage, h.sendMessage)
	sub.OnTask(KindSendApplicationPanel, h.sendApplicationPanel)
	sub.OnTask(KindSendTicketPanel, h.sendTicketPanel)
	sub.OnTask(KindSendGiveawayPanel, h.sendGiveawayPanel)

	sub.OnEvent(worker.EventTaskCompleted, func(_ context.Context, e worker.Event) {
		// The Worker emits task.completed for settings PATCH/POST flows that
		// the Dashboard runs. There's no in-process cache to invalidate yet,
		// but logging makes the propagation visible.
		log.Printf("dashboard event %s subject=%s", e.Type, e.Subject)
	})
}

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
		Role        string `json:"role"`
		ButtonLabel string `json:"button_label"`
	}
	if err := decode(t.Input, &p); err != nil {
		return nil, err
	}
	if p.ButtonLabel == "" {
		p.ButtonLabel = "Apply"
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
					CustomID: "apply:" + p.Role,
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
	}
	if err := decode(t.Input, &p); err != nil {
		return nil, err
	}
	if p.ButtonLabel == "" {
		p.ButtonLabel = "Open ticket"
	}
	customID := "ticket:open"
	if p.PanelID != "" {
		customID = "ticket:open:" + p.PanelID
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
		log.Printf("giveaway reaction: %v", err)
	}
	return map[string]any{"message_id": msg.ID}, nil
}

func decode(in map[string]any, out any) error {
	raw, err := json.Marshal(in)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}
