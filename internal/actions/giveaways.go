package actions

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand"
	"strings"

	"github.com/bwmarrin/discordgo"

	"github.com/che1/bot/internal/worker"
)

// giveaway mirrors the Dashboard's Giveaway shape. Embed-customization
// fields are accepted so the "Ended" state can preserve the operator's
// styling — they're identical to the keys used by send_giveaway_panel.
type giveaway struct {
	ID          string   `json:"id"`
	GuildID     string   `json:"guild_id"`
	ChannelID   string   `json:"channel_id"`
	MessageID   string   `json:"message_id"`
	Prize       string   `json:"prize"`
	Winners     []string `json:"winners"`
	WinnerCount int      `json:"winner_count"`
	Entrants    []string `json:"entrants"`
	HostedBy    string   `json:"hosted_by"`
	EndsAtUnix  int64    `json:"ends_at_unix"`
	LockChannel bool     `json:"lock_channel"`

	// Embed customization (mirrored from send_giveaway_panel so we can
	// rebuild the panel in its Ended state).
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
	ButtonLabel  string `json:"button_label"`
	ButtonEmoji  string `json:"button_emoji"`
	ButtonStyle  string `json:"button_style"`
}

func (h *Handlers) giveawaysEnd(_ context.Context, t worker.Task) (map[string]any, error) {
	var g giveaway
	if err := decode(t.Input, &g); err != nil {
		return nil, err
	}
	winners, drawErr := h.drawWinners(g, nil)
	// Always update the panel to its Ended state, even if there were no
	// entrants — the operator should see the giveaway closed.
	h.markPanelEnded(g)
	// Reverse channel lock if it was applied at create time.
	if g.LockChannel && g.GuildID != "" && g.ChannelID != "" {
		if err := unlockChannel(h.Session, g.GuildID, g.ChannelID); err != nil {
			slog.Warn("giveaway channel unlock failed", "channel_id", g.ChannelID, "err", err)
		}
	}

	if drawErr != nil {
		_, _ = h.Session.ChannelMessageSendComplex(g.ChannelID, &discordgo.MessageSend{
			Reference: messageRef(g),
			Embeds: []*discordgo.MessageEmbed{{
				Title:       "🎊 Giveaway ended",
				Description: "**Prize:** " + g.Prize + "\nNo eligible entrants — no winner drawn.",
				Color:       0x747F8D,
			}},
		})
		return map[string]any{"winners": []string{}, "reason": drawErr.Error()}, nil
	}

	if err := h.announceWinners(g, winners, "🎊 Giveaway ended"); err != nil {
		return nil, err
	}
	return map[string]any{"winners": winners}, nil
}

func (h *Handlers) giveawaysReroll(_ context.Context, t worker.Task) (map[string]any, error) {
	var g giveaway
	if err := decode(t.Input, &g); err != nil {
		return nil, err
	}
	excluded := map[string]bool{}
	for _, w := range g.Winners {
		excluded[w] = true
	}
	winners, err := h.drawWinners(g, excluded)
	if err != nil {
		return nil, err
	}
	if err := h.announceWinners(g, winners, "🎉 Giveaway rerolled"); err != nil {
		return nil, err
	}
	return map[string]any{"winners": winners}, nil
}

// drawWinners picks WinnerCount unique entrants from the giveaway. If no
// entrants list was provided, falls back to the 🎉 reaction roster.
func (h *Handlers) drawWinners(g giveaway, exclude map[string]bool) ([]string, error) {
	count := g.WinnerCount
	if count < 1 {
		count = 1
	}

	pool := append([]string(nil), g.Entrants...)
	if len(pool) == 0 && g.ChannelID != "" && g.MessageID != "" {
		var err error
		pool, err = h.fetchReactionUsers(g.ChannelID, g.MessageID, "🎉")
		if err != nil {
			return nil, err
		}
	}

	if exclude != nil {
		filtered := pool[:0]
		for _, u := range pool {
			if !exclude[u] {
				filtered = append(filtered, u)
			}
		}
		pool = filtered
	}
	if len(pool) == 0 {
		return nil, fmt.Errorf("no eligible entrants")
	}

	rand.Shuffle(len(pool), func(i, j int) { pool[i], pool[j] = pool[j], pool[i] })
	if count > len(pool) {
		count = len(pool)
	}
	return pool[:count], nil
}

func (h *Handlers) fetchReactionUsers(channelID, messageID, emoji string) ([]string, error) {
	var ids []string
	after := ""
	for {
		users, err := h.Session.MessageReactions(channelID, messageID, emoji, 100, "", after)
		if err != nil {
			return nil, err
		}
		if len(users) == 0 {
			break
		}
		for _, u := range users {
			if u.Bot {
				continue
			}
			ids = append(ids, u.ID)
		}
		if len(users) < 100 {
			break
		}
		after = users[len(users)-1].ID
	}
	return ids, nil
}

func (h *Handlers) announceWinners(g giveaway, winners []string, title string) error {
	mentions := make([]string, len(winners))
	for i, id := range winners {
		mentions[i] = "<@" + id + ">"
	}
	_, err := h.Session.ChannelMessageSendComplex(g.ChannelID, &discordgo.MessageSend{
		Reference: messageRef(g),
		Embeds: []*discordgo.MessageEmbed{{
			Title:       title,
			Description: "**Prize:** " + g.Prize + "\n**Winners:** " + strings.Join(mentions, ", "),
			Color:       0xEB459E,
		}},
	})
	return err
}

// markPanelEnded edits the original giveaway message into its Ended
// state: greyed-out embed and a disabled Enter button. Best-effort —
// failures are logged but don't fail the task.
func (h *Handlers) markPanelEnded(g giveaway) {
	if g.ChannelID == "" || g.MessageID == "" {
		return
	}
	p := giveawayPanelInput{
		ChannelID:    g.ChannelID,
		GiveawayID:   g.ID,
		Prize:        g.Prize,
		WinnerCount:  g.WinnerCount,
		EndsAtUnix:   g.EndsAtUnix,
		HostedBy:     g.HostedBy,
		Title:        g.Title,
		Description:  g.Description,
		Color:        g.Color,
		ImageURL:     g.ImageURL,
		ThumbnailURL: g.ThumbnailURL,
		FooterText:   g.FooterText,
		FooterIcon:   g.FooterIcon,
		AuthorName:   g.AuthorName,
		AuthorIcon:   g.AuthorIcon,
		Requirements: g.Requirements,
		ButtonLabel:  g.ButtonLabel,
		ButtonEmoji:  g.ButtonEmoji,
		ButtonStyle:  g.ButtonStyle,
	}
	endedEmbed := buildGiveawayEmbed(p, true)
	disabled := buildEnterButton(p, true)
	disabled.Label = "Giveaway ended"

	components := []discordgo.MessageComponent{
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{disabled}},
	}
	_, err := h.Session.ChannelMessageEditComplex(&discordgo.MessageEdit{
		Channel:    g.ChannelID,
		ID:         g.MessageID,
		Embeds:     &[]*discordgo.MessageEmbed{endedEmbed},
		Components: &components,
	})
	if err != nil {
		slog.Warn("giveaway end: edit panel", "id", g.ID, "err", err)
	}
}

func messageRef(g giveaway) *discordgo.MessageReference {
	if g.MessageID == "" {
		return nil
	}
	return &discordgo.MessageReference{MessageID: g.MessageID, ChannelID: g.ChannelID, GuildID: g.GuildID}
}
