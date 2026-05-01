package actions

import (
	"context"
	"fmt"
	"math/rand"
	"strings"

	"github.com/bwmarrin/discordgo"

	"github.com/che1/bot/internal/worker"
)

// giveaway mirrors the Dashboard's Giveaway shape (only the fields we use).
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
}

func (h *Handlers) giveawaysEnd(_ context.Context, t worker.Task) (map[string]any, error) {
	var g giveaway
	if err := decode(t.Input, &g); err != nil {
		return nil, err
	}
	winners, err := h.drawWinners(g, nil)
	if err != nil {
		return nil, err
	}
	if err := h.announceWinners(g, winners, "🎉 Giveaway ended"); err != nil {
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
		Reference: &discordgo.MessageReference{MessageID: g.MessageID, ChannelID: g.ChannelID, GuildID: g.GuildID},
		Embeds: []*discordgo.MessageEmbed{{
			Title:       title,
			Description: "**Prize:** " + g.Prize + "\n**Winners:** " + strings.Join(mentions, ", "),
			Color:       0xEB459E,
		}},
	})
	return err
}
