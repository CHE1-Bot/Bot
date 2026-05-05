package actions

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/jackc/pgx/v5"

	"github.com/che1/bot/internal/db"
	"github.com/che1/bot/internal/handler"
	"github.com/che1/bot/internal/worker"
)

// Components holds the dependencies needed by interactive button handlers
// (giveaway entry/leave, ticket panel button, etc.).
type Components struct {
	Worker *worker.Queue
	DB     *db.DB
}

// Register wires component-button handlers into the router. Button
// CustomIDs follow the convention `<prefix>:<verb>:<id>`.
func (c *Components) Register(r *handler.Router) {
	r.OnComponent("giveaway", c.giveawayComponent)
}

func (c *Components) giveawayComponent(s *discordgo.Session, i *discordgo.InteractionCreate) {
	id := i.MessageComponentData().CustomID
	parts := strings.Split(id, ":")
	if len(parts) < 3 {
		handler.Reply(s, i, "Unknown action.")
		return
	}
	verb, giveawayID := parts[1], parts[2]

	user := interactionUser(i)
	if user == nil {
		handler.Reply(s, i, "Couldn't read your user info — try again.")
		return
	}

	switch verb {
	case "enter":
		c.giveawayEnter(s, i, giveawayID, user)
	case "leave":
		c.giveawayLeave(s, i, giveawayID, user)
	default:
		handler.Reply(s, i, "Unknown giveaway action.")
	}
}

func (c *Components) giveawayEnter(s *discordgo.Session, i *discordgo.InteractionCreate, giveawayID string, user *discordgo.User) {
	if c.DB == nil {
		// No DB → fire-and-forget the Worker task and reply optimistically.
		c.forwardEnter(i, giveawayID, user)
		handler.Reply(s, i, "🎉 You're entered! Good luck.")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// INSERT ... ON CONFLICT DO NOTHING and detect whether a row was
	// actually inserted. RowsAffected is 0 when the user was already in.
	tag, err := c.DB.Exec(ctx, `
		INSERT INTO giveaway_entries (giveaway_id, user_id)
		VALUES ($1, $2)
		ON CONFLICT (giveaway_id, user_id) DO NOTHING`,
		giveawayID, user.ID,
	)
	if err != nil {
		handler.Reply(s, i, "Couldn't record your entry — please try again in a moment.")
		return
	}

	if tag.RowsAffected() == 0 {
		// Already entered — show the Leave button so they can withdraw.
		c.replyAlreadyEntered(s, i, giveawayID)
		return
	}

	// New entry — keep the Worker / Dashboard in sync, then confirm.
	c.forwardEnter(i, giveawayID, user)
	handler.Reply(s, i, "🎉 You're entered! Good luck.")
}

func (c *Components) giveawayLeave(s *discordgo.Session, i *discordgo.InteractionCreate, giveawayID string, user *discordgo.User) {
	if c.DB == nil {
		handler.Reply(s, i, "Leaving requires the DB to be enabled.")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	tag, err := c.DB.Exec(ctx, `
		DELETE FROM giveaway_entries WHERE giveaway_id=$1 AND user_id=$2`,
		giveawayID, user.ID,
	)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		handler.Reply(s, i, "Couldn't remove your entry — please try again.")
		return
	}
	if tag.RowsAffected() == 0 {
		handler.Reply(s, i, "You weren't entered in this giveaway.")
		return
	}

	// Best-effort upstream notification so the Dashboard's live count updates.
	if c.Worker != nil {
		ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel2()
		_, _ = c.Worker.Enqueue(ctx2, "giveaways.leave", map[string]any{
			"giveaway_id": giveawayID,
			"guild_id":    i.GuildID,
			"user_id":     user.ID,
			"username":    user.Username,
		})
	}

	handler.Reply(s, i, "👋 You've been removed from the giveaway.")
}

// replyAlreadyEntered swaps the standard ack for an ephemeral message
// containing a Leave button — clicking it routes back through this same
// component handler with verb=leave.
func (c *Components) replyAlreadyEntered(s *discordgo.Session, i *discordgo.InteractionCreate, giveawayID string) {
	content := "You've already entered this giveaway. Press the button below if you'd like to leave it — you won't be eligible to win once you do."
	components := []discordgo.MessageComponent{
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.Button{
				Label:    "Leave giveaway",
				Style:    discordgo.DangerButton,
				CustomID: "giveaway:leave:" + giveawayID,
				Emoji:    &discordgo.ComponentEmoji{Name: "🚪"},
			},
		}},
	}
	_, err := s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
		Content:    &content,
		Components: &components,
	})
	if err != nil {
		// Fall back to plain text if the component edit fails.
		handler.Reply(s, i, "You've already entered this giveaway.")
	}
}

// forwardEnter fires-and-forgets a giveaways.enter task to the Worker so
// the Dashboard's live entrant count stays in sync. The bot is the source
// of truth for entries; this is for downstream observers.
func (c *Components) forwardEnter(i *discordgo.InteractionCreate, giveawayID string, user *discordgo.User) {
	if c.Worker == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = c.Worker.Enqueue(ctx, KindGiveawaysEnter, map[string]any{
		"giveaway_id": giveawayID,
		"guild_id":    i.GuildID,
		"channel_id":  i.ChannelID,
		"user_id":     user.ID,
		"username":    user.Username,
	})
}

func interactionUser(i *discordgo.InteractionCreate) *discordgo.User {
	if i.Member != nil && i.Member.User != nil {
		return i.Member.User
	}
	return i.User
}
