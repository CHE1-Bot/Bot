package giveaways

import (
	"context"
	"strconv"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/che1/bot/internal/db"
	"github.com/che1/bot/internal/handler"
	"github.com/che1/bot/internal/worker"
)

type Module struct {
	DB     *db.DB
	Worker *worker.Queue
}

func (m *Module) Name() string                                                 { return "giveaways" }
func (m *Module) MessageCreate(*discordgo.Session, *discordgo.MessageCreate) {}

func (m *Module) Commands() []handler.SlashCommand {
	return []handler.SlashCommand{{
		Definition: &discordgo.ApplicationCommand{
			Name: "giveaway", Description: "Start a giveaway",
			Options: []*discordgo.ApplicationCommandOption{
				{Type: discordgo.ApplicationCommandOptionString, Name: "prize", Description: "Prize", Required: true},
				{Type: discordgo.ApplicationCommandOptionInteger, Name: "minutes", Description: "Duration in minutes", Required: true},
			},
		},
		Handler: m.handleStart,
	}}
}

func (m *Module) handleStart(s *discordgo.Session, i *discordgo.InteractionCreate) {
	opts := i.ApplicationCommandData().Options
	prize := opts[0].StringValue()
	mins := opts[1].IntValue()
	ends := time.Now().Add(time.Duration(mins) * time.Minute)

	msg, err := s.ChannelMessageSend(i.ChannelID, "🎉 **Giveaway:** "+prize+" — react 🎉 to enter! Ends <t:"+strconv.FormatInt(ends.Unix(), 10)+":R>")
	if err != nil {
		return
	}
	_ = s.MessageReactionAdd(msg.ChannelID, msg.ID, "🎉")

	var gid int64
	_ = m.DB.QueryRow(context.Background(), `
		INSERT INTO giveaways (guild_id, channel_id, message_id, prize, ends_at, status)
		VALUES ($1,$2,$3,$4,$5,'running') RETURNING id`,
		i.GuildID, msg.ChannelID, msg.ID, prize, ends,
	).Scan(&gid)

	// Worker owns the timer + winner draw so the bot can restart freely.
	_, _ = m.Worker.Enqueue(context.Background(), worker.JobGiveawayTimer, map[string]any{
		"giveaway_id": gid,
		"ends_at":     ends,
	})

	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Content: "Giveaway started.", Flags: discordgo.MessageFlagsEphemeral},
	})
}
