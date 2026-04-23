package applications

import (
	"context"

	"github.com/bwmarrin/discordgo"

	"github.com/che1/bot/internal/dashboard"
	"github.com/che1/bot/internal/db"
	"github.com/che1/bot/internal/handler"
)

// Applications are submitted through the Dashboard; the bot only surfaces
// status and notifies reviewers. The Dashboard writes/reads the same
// `applications` table.
type Module struct {
	DB        *db.DB
	Dashboard *dashboard.Client
}

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
	formURL := m.lookupFormURL(i.GuildID, role)
	if formURL == "" {
		formURL = "https://dashboard.example.com/apply"
	}
	handler.Reply(s, i, "Apply here: "+formURL)
}

// lookupFormURL prefers the Dashboard API; falls back to direct DB read.
func (m *Module) lookupFormURL(guildID, role string) string {
	if m.Dashboard != nil {
		forms, err := m.Dashboard.ApplicationForms(context.Background(), guildID)
		if err == nil {
			for _, f := range forms {
				if f.Role == role {
					return f.URL
				}
			}
		}
	}
	if m.DB != nil {
		var url string
		_ = m.DB.QueryRow(context.Background(),
			`SELECT url FROM application_forms WHERE guild_id=$1 AND role=$2`,
			guildID, role,
		).Scan(&url)
		return url
	}
	return ""
}
