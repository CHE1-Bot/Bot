package applications

import (
	"context"

	"github.com/bwmarrin/discordgo"

	"github.com/che1/bot/internal/db"
	"github.com/che1/bot/internal/handler"
)

// Applications are submitted through the Dashboard; the bot only surfaces
// status and notifies reviewers. The Dashboard writes/reads the same
// `applications` table.
type Module struct{ DB *db.DB }

func (m *Module) Name() string                                                 { return "applications" }
func (m *Module) MessageCreate(*discordgo.Session, *discordgo.MessageCreate) {}

func (m *Module) Commands() []handler.SlashCommand {
	return []handler.SlashCommand{{
		Definition: &discordgo.ApplicationCommand{
			Name: "apply", Description: "Open the application form",
			Options: []*discordgo.ApplicationCommandOption{{
				Type: discordgo.ApplicationCommandOptionString, Name: "role",
				Description: "Role you are applying for", Required: true,
			}},
		},
		Handler: m.handleApply,
	}}
}

func (m *Module) handleApply(s *discordgo.Session, i *discordgo.InteractionCreate) {
	role := i.ApplicationCommandData().Options[0].StringValue()
	var formURL string
	_ = m.DB.QueryRow(context.Background(),
		`SELECT url FROM application_forms WHERE guild_id=$1 AND role=$2`,
		i.GuildID, role,
	).Scan(&formURL)
	if formURL == "" {
		formURL = "https://dashboard.example.com/apply"
	}
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: "Apply here: " + formURL,
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	})
}
