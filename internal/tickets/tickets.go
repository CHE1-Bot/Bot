package tickets

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
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

func (m *Module) Name() string { return "tickets" }

func (m *Module) Commands() []handler.SlashCommand {
	return []handler.SlashCommand{
		{
			Definition: &discordgo.ApplicationCommand{
				Name:        "ticket",
				Description: "Open a support ticket",
				Options: []*discordgo.ApplicationCommandOption{{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "subject",
					Description: "What do you need help with?",
					Required:    true,
				}},
			},
			Handler: m.handleOpen,
		},
		{
			Definition: &discordgo.ApplicationCommand{
				Name:        "ticket-close",
				Description: "Close the current ticket and save a transcript",
			},
			Handler: m.handleClose,
		},
	}
}

func (m *Module) MessageCreate(*discordgo.Session, *discordgo.MessageCreate) {}

func (m *Module) handleOpen(s *discordgo.Session, i *discordgo.InteractionCreate) {
	ctx := context.Background()
	subject := i.ApplicationCommandData().Options[0].StringValue()
	userID := i.Member.User.ID

	ch, err := s.GuildChannelCreateComplex(i.GuildID, discordgo.GuildChannelCreateData{
		Name: fmt.Sprintf("ticket-%s", userID[:6]),
		Type: discordgo.ChannelTypeGuildText,
		PermissionOverwrites: []*discordgo.PermissionOverwrite{
			{ID: i.GuildID, Type: discordgo.PermissionOverwriteTypeRole, Deny: discordgo.PermissionViewChannel},
			{ID: userID, Type: discordgo.PermissionOverwriteTypeMember, Allow: discordgo.PermissionViewChannel | discordgo.PermissionSendMessages},
		},
	})
	if err != nil {
		respond(s, i, "Failed to open ticket: "+err.Error())
		return
	}

	var ticketID int64
	err = m.DB.QueryRow(ctx, `
		INSERT INTO tickets (guild_id, channel_id, user_id, subject, status, opened_at)
		VALUES ($1, $2, $3, $4, 'open', NOW())
		RETURNING id`,
		i.GuildID, ch.ID, userID, subject,
	).Scan(&ticketID)
	if err != nil {
		slog.Error("ticket insert failed", "err", err)
	}

	respond(s, i, fmt.Sprintf("Ticket opened in <#%s> (id=%d)", ch.ID, ticketID))
}

type transcriptPayload struct {
	TicketID  int64                      `json:"ticket_id"`
	GuildID   string                     `json:"guild_id"`
	ChannelID string                     `json:"channel_id"`
	Messages  []*discordgo.Message       `json:"messages"`
	ClosedAt  time.Time                  `json:"closed_at"`
}

func (m *Module) handleClose(s *discordgo.Session, i *discordgo.InteractionCreate) {
	ctx := context.Background()

	var ticketID int64
	err := m.DB.QueryRow(ctx, `
		UPDATE tickets SET status='closed', closed_at=NOW()
		WHERE channel_id=$1 AND status='open'
		RETURNING id`, i.ChannelID).Scan(&ticketID)
	if err != nil {
		respond(s, i, "No open ticket in this channel.")
		return
	}

	msgs, err := s.ChannelMessages(i.ChannelID, 100, "", "", "")
	if err != nil {
		slog.Error("fetch messages", "err", err)
	}

	// Offload transcript rendering (HTML/PDF) to the Worker.
	jobID, err := m.Worker.Enqueue(ctx, worker.JobTicketTranscript, transcriptPayload{
		TicketID:  ticketID,
		GuildID:   i.GuildID,
		ChannelID: i.ChannelID,
		Messages:  msgs,
		ClosedAt:  time.Now().UTC(),
	})
	if err != nil {
		respond(s, i, "Queue error: "+err.Error())
		return
	}
	respond(s, i, fmt.Sprintf("Ticket #%d closed. Transcript job: %s", ticketID, jobID))

	// Optionally await the Worker result (URL to transcript file) and store it.
	go func() {
		raw, err := m.Worker.WaitResult(context.Background(), jobID, 60*time.Second)
		if err != nil {
			return
		}
		var result struct{ URL string `json:"url"` }
		if err := json.Unmarshal(raw, &result); err == nil && result.URL != "" {
			_, _ = m.DB.Exec(context.Background(),
				`UPDATE tickets SET transcript_url=$1 WHERE id=$2`, result.URL, ticketID)
		}
	}()
}

func respond(s *discordgo.Session, i *discordgo.InteractionCreate, msg string) {
	handler.Reply(s, i, msg)
}
