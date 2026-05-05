// Package actions handles dashboard-driven Discord side-effects.
//
// Flow: Dashboard creates a task via Worker REST (POST /api/v1/tasks);
// the Worker emits task.created on its WebSocket hub; the bot subscribes
// and runs the side-effect (post a message, render a panel, kick a user,
// DM an applicant, draw a giveaway winner, ...). The bot reports
// completion via Worker.Complete, which the Dashboard observes through
// task.completed.
//
// The kind catalog mirrors CHE1-Bot/Worker README's task-kind table.
package actions

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/bwmarrin/discordgo"

	"github.com/che1/bot/internal/worker"
)

// Task kinds the bot performs on the Dashboard's behalf. Strings are
// canonical names from the Worker repo's catalog. Some Dashboard code
// still uses singular `giveaway.*` aliases — registered alongside the
// canonical plural form in Register so we're robust to both.
const (
	KindSendMessage          = "send_message"
	KindSendApplicationPanel = "send_application_panel"
	KindSendTicketPanel      = "send_ticket_panel"
	KindSendGiveawayPanel    = "send_giveaway_panel"

	KindTicketsCreate  = "tickets.create"
	KindTicketsUpdate  = "tickets.update"
	KindTicketsClaim   = "tickets.claim"
	KindTicketsUnclaim = "tickets.unclaim"

	KindModerationAction = "moderation.action"
	KindModerationKick   = "moderation.kick"
	KindModerationBan    = "moderation.ban"
	KindModerationUnban  = "moderation.unban"
	KindModerationMute   = "moderation.mute"
	KindModerationUnmute = "moderation.unmute"
	KindModerationWarn   = "moderation.warn"

	KindApplicationsAccepted = "applications.accepted"
	KindApplicationsRejected = "applications.rejected"

	KindGiveawaysEnd    = "giveaways.end"
	KindGiveawaysReroll = "giveaways.reroll"
	// Singular aliases used by some Dashboard code paths.
	KindGiveawayEnd    = "giveaway.end"
	KindGiveawayReroll = "giveaway.reroll"

	// Bot → Worker: a user pressed the Enter button on a giveaway panel.
	// Worker stores the entrant and emits task.completed so the Dashboard
	// sees live counts.
	KindGiveawaysEnter = "giveaways.enter"
)

type Handlers struct {
	Session *discordgo.Session
}

// Register wires every supported task kind into the Subscriber, and logs
// task.completed so operators can see settings PATCH/POST flows propagating.
func (h *Handlers) Register(sub *worker.Subscriber) {
	// Panels & raw messages.
	sub.OnTask(KindSendMessage, h.sendMessage)
	sub.OnTask(KindSendApplicationPanel, h.sendApplicationPanel)
	sub.OnTask(KindSendTicketPanel, h.sendTicketPanel)
	sub.OnTask(KindSendGiveawayPanel, h.sendGiveawayPanel)

	// Tickets.
	sub.OnTask(KindTicketsCreate, h.ticketsCreate)
	sub.OnTask(KindTicketsUpdate, h.ticketsUpdate)
	sub.OnTask(KindTicketsClaim, h.ticketsClaim)
	sub.OnTask(KindTicketsUnclaim, h.ticketsUnclaim)

	// Moderation. Both the unified `moderation.action` and split kinds
	// are wired so the bot works regardless of how the Dashboard dispatches.
	sub.OnTask(KindModerationAction, h.moderationAction)
	sub.OnTask(KindModerationKick, h.moderationKick)
	sub.OnTask(KindModerationBan, h.moderationBan)
	sub.OnTask(KindModerationUnban, h.moderationUnban)
	sub.OnTask(KindModerationMute, h.moderationMute)
	sub.OnTask(KindModerationUnmute, h.moderationUnmute)
	sub.OnTask(KindModerationWarn, h.moderationWarn)

	// Applications.
	sub.OnTask(KindApplicationsAccepted, h.applicationsAccepted)
	sub.OnTask(KindApplicationsRejected, h.applicationsRejected)

	// Giveaways. Plural is canonical; singular aliases share handlers.
	sub.OnTask(KindGiveawaysEnd, h.giveawaysEnd)
	sub.OnTask(KindGiveawayEnd, h.giveawaysEnd)
	sub.OnTask(KindGiveawaysReroll, h.giveawaysReroll)
	sub.OnTask(KindGiveawayReroll, h.giveawaysReroll)

	sub.OnEvent(worker.EventTaskCompleted, func(_ context.Context, e worker.Event) {
		slog.Debug("dashboard event", "type", e.Type, "subject", e.Subject)
	})
}

func decode(in map[string]any, out any) error {
	raw, err := json.Marshal(in)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}
