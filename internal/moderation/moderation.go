package moderation

import (
	"context"
	"log"

	"github.com/bwmarrin/discordgo"

	"github.com/che1/bot/internal/db"
	"github.com/che1/bot/internal/handler"
)

type Module struct{ DB *db.DB }

func (m *Module) Name() string                                                 { return "moderation" }
func (m *Module) MessageCreate(*discordgo.Session, *discordgo.MessageCreate) {}

func (m *Module) Commands() []handler.SlashCommand {
	userOpt := func(req bool) *discordgo.ApplicationCommandOption {
		return &discordgo.ApplicationCommandOption{
			Type: discordgo.ApplicationCommandOptionUser, Name: "user", Description: "Target user", Required: req,
		}
	}
	reasonOpt := &discordgo.ApplicationCommandOption{
		Type: discordgo.ApplicationCommandOptionString, Name: "reason", Description: "Reason", Required: false,
	}
	return []handler.SlashCommand{
		{Definition: &discordgo.ApplicationCommand{Name: "kick", Description: "Kick a user",
			Options: []*discordgo.ApplicationCommandOption{userOpt(true), reasonOpt}}, Handler: m.action("kick")},
		{Definition: &discordgo.ApplicationCommand{Name: "ban", Description: "Ban a user",
			Options: []*discordgo.ApplicationCommandOption{userOpt(true), reasonOpt}}, Handler: m.action("ban")},
		{Definition: &discordgo.ApplicationCommand{Name: "mute", Description: "Timeout a user",
			Options: []*discordgo.ApplicationCommandOption{userOpt(true), reasonOpt}}, Handler: m.action("mute")},
		{Definition: &discordgo.ApplicationCommand{Name: "warn", Description: "Warn a user",
			Options: []*discordgo.ApplicationCommandOption{userOpt(true), reasonOpt}}, Handler: m.action("warn")},
	}
}

func (m *Module) action(kind string) func(*discordgo.Session, *discordgo.InteractionCreate) {
	return func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		opts := i.ApplicationCommandData().Options
		target := opts[0].UserValue(s)
		reason := ""
		if len(opts) > 1 {
			reason = opts[1].StringValue()
		}
		switch kind {
		case "kick":
			_ = s.GuildMemberDeleteWithReason(i.GuildID, target.ID, reason)
		case "ban":
			_ = s.GuildBanCreateWithReason(i.GuildID, target.ID, reason, 0)
		}
		_, err := m.DB.Exec(context.Background(), `
			INSERT INTO mod_logs (guild_id, moderator_id, target_id, action, reason, created_at)
			VALUES ($1,$2,$3,$4,$5,NOW())`,
			i.GuildID, i.Member.User.ID, target.ID, kind, reason)
		if err != nil {
			log.Printf("mod log: %v", err)
		}
		handler.Reply(s, i, kind+" applied to <@"+target.ID+">")
	}
}
