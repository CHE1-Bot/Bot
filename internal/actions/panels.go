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

// giveawayPanelInput is the payload the Dashboard sends for a button-based
// (GiveawayBot-style) giveaway. Every embed field is optional and falls
// back to a sensible default if blank — the Dashboard's embed editor maps
// 1-to-1 to these fields. Required: channel_id, giveaway_id, prize.
type giveawayPanelInput struct {
	GuildID     string `json:"guild_id"`
	ChannelID   string `json:"channel_id"`
	GiveawayID  string `json:"giveaway_id"`
	Prize       string `json:"prize"`
	WinnerCount int    `json:"winner_count"`
	EndsAtUnix  int64  `json:"ends_at_unix"`
	HostedBy    string `json:"hosted_by"` // user ID; rendered as mention
	LockChannel bool   `json:"lock_channel"`

	// Embed customization.
	Title        string `json:"title"`
	Description  string `json:"description"`
	Color        int    `json:"color"`
	ImageURL     string `json:"image_url"`
	ThumbnailURL string `json:"thumbnail_url"`
	FooterText   string `json:"footer_text"`
	FooterIcon   string `json:"footer_icon"`
	AuthorName   string `json:"author_name"`
	AuthorIcon   string `json:"author_icon"`
	Requirements string `json:"requirements"`

	// Button customization.
	ButtonLabel string `json:"button_label"`
	ButtonEmoji string `json:"button_emoji"`
	ButtonStyle string `json:"button_style"` // primary|success|secondary|danger
}

func (h *Handlers) sendGiveawayPanel(_ context.Context, t worker.Task) (map[string]any, error) {
	var p giveawayPanelInput
	if err := decode(t.Input, &p); err != nil {
		return nil, err
	}
	if p.ChannelID == "" || p.GiveawayID == "" || p.Prize == "" {
		return nil, fmt.Errorf("send_giveaway_panel: channel_id, giveaway_id, prize required")
	}

	embed := buildGiveawayEmbed(p, false)
	components := []discordgo.MessageComponent{
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			buildEnterButton(p, false),
		}},
	}

	msg, err := h.Session.ChannelMessageSendComplex(p.ChannelID, &discordgo.MessageSend{
		Embeds:     []*discordgo.MessageEmbed{embed},
		Components: components,
	})
	if err != nil {
		return nil, err
	}

	// If the operator asked for it, lock the channel for non-staff. The
	// matching unlock happens when giveaways.end fires (see giveaways.go).
	if p.LockChannel && p.GuildID != "" {
		if err := lockChannel(h.Session, p.GuildID, p.ChannelID); err != nil {
			slog.Warn("giveaway channel lock failed", "channel_id", p.ChannelID, "err", err)
		}
	}

	return map[string]any{"message_id": msg.ID, "channel_locked": p.LockChannel}, nil
}

func buildGiveawayEmbed(p giveawayPanelInput, ended bool) *discordgo.MessageEmbed {
	title := p.Title
	if title == "" {
		title = "🎉 " + p.Prize
	}
	if ended {
		title = "🎊 ENDED — " + p.Prize
	}

	desc := p.Description
	if desc == "" {
		desc = "Click the button below to enter!"
	}

	winners := p.WinnerCount
	if winners < 1 {
		winners = 1
	}
	desc += fmt.Sprintf("\n\n**Winners:** %d", winners)
	if p.EndsAtUnix > 0 {
		if ended {
			desc += fmt.Sprintf("\n**Ended:** <t:%d:R>", p.EndsAtUnix)
		} else {
			desc += fmt.Sprintf("\n**Ends:** <t:%d:R> (<t:%d:f>)", p.EndsAtUnix, p.EndsAtUnix)
		}
	}
	if p.HostedBy != "" {
		desc += "\n**Hosted by:** <@" + p.HostedBy + ">"
	}
	if p.Requirements != "" {
		desc += "\n\n**Requirements**\n" + p.Requirements
	}

	color := p.Color
	if color == 0 {
		color = 0xEB459E
	}
	if ended {
		color = 0x747F8D
	}

	embed := &discordgo.MessageEmbed{
		Title:       title,
		Description: desc,
		Color:       color,
	}
	if p.ImageURL != "" {
		embed.Image = &discordgo.MessageEmbedImage{URL: p.ImageURL}
	}
	if p.ThumbnailURL != "" {
		embed.Thumbnail = &discordgo.MessageEmbedThumbnail{URL: p.ThumbnailURL}
	}
	if p.FooterText != "" || p.FooterIcon != "" {
		embed.Footer = &discordgo.MessageEmbedFooter{Text: p.FooterText, IconURL: p.FooterIcon}
	}
	if p.AuthorName != "" || p.AuthorIcon != "" {
		embed.Author = &discordgo.MessageEmbedAuthor{Name: p.AuthorName, IconURL: p.AuthorIcon}
	}
	return embed
}

func buildEnterButton(p giveawayPanelInput, disabled bool) discordgo.Button {
	label := p.ButtonLabel
	if label == "" {
		label = "Enter Giveaway"
	}
	emoji := p.ButtonEmoji
	if emoji == "" {
		emoji = "🎉"
	}
	style := discordgo.PrimaryButton
	switch p.ButtonStyle {
	case "success":
		style = discordgo.SuccessButton
	case "secondary":
		style = discordgo.SecondaryButton
	case "danger":
		style = discordgo.DangerButton
	}
	return discordgo.Button{
		Label:    label,
		Style:    style,
		Disabled: disabled,
		CustomID: "giveaway:enter:" + p.GiveawayID,
		Emoji:    &discordgo.ComponentEmoji{Name: emoji},
	}
}

