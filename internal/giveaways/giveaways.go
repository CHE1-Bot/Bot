// Package giveaways implements the GiveawayBot-style slash command surface:
// /gstart, /gcreate (modal), /gend, /greroll, /glist, /gdelete.
//
// The commands are thin frontends. Persistence and embed rendering live
// in the Worker (see CHE1-Bot/Worker task-kind catalog). This module just
// validates input, dispatches the right task kind, and answers the user.
//
// /glist is the only command that reads back: it queries the bot's
// Postgres pool directly (the Worker writes there too, so the schema is
// shared with the Dashboard).
package giveaways

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/che1/bot/internal/db"
	"github.com/che1/bot/internal/handler"
	"github.com/che1/bot/internal/worker"
)

const (
	kindCreate = "giveaways.create"
	kindEnd    = "giveaways.end"
	kindReroll = "giveaways.reroll"
	kindDelete = "giveaways.delete"

	modalPrefix = "gcreate"
)

type Module struct {
	DB     *db.DB
	Worker *worker.Queue
}

func (m *Module) Name() string                                                 { return "giveaways" }
func (m *Module) MessageCreate(*discordgo.Session, *discordgo.MessageCreate) {}

func (m *Module) Commands() []handler.SlashCommand {
	messageIDOpt := &discordgo.ApplicationCommandOption{
		Type: discordgo.ApplicationCommandOptionString, Name: "message_id",
		Description: "Giveaway message ID", Required: true,
	}

	return []handler.SlashCommand{
		{
			Definition: &discordgo.ApplicationCommand{
				Name:        "gstart",
				Description: "Start a giveaway in this channel",
				Options: []*discordgo.ApplicationCommandOption{
					{Type: discordgo.ApplicationCommandOptionString, Name: "duration",
						Description: "Duration, e.g. 30s, 10m, 2h, 1d, 1w", Required: true},
					{Type: discordgo.ApplicationCommandOptionInteger, Name: "winners",
						Description: "Number of winners", Required: true, MinValue: ptrFloat(1)},
					{Type: discordgo.ApplicationCommandOptionString, Name: "prize",
						Description: "What's being given away", Required: true},
					{Type: discordgo.ApplicationCommandOptionBoolean, Name: "lock_channel",
						Description: "Lock the channel for the duration of the giveaway", Required: false},
				},
			},
			Handler: m.handleGStart,
		},
		{
			Definition: &discordgo.ApplicationCommand{
				Name:        "gcreate",
				Description: "Start a giveaway in this channel with a customizable embed (opens a modal)",
			},
			Handler: m.handleGCreate,
			NoAck:   true, // modal response must be the very first ack
		},
		{
			Definition: &discordgo.ApplicationCommand{
				Name:        "gend",
				Description: "End a giveaway early and pick winners",
				Options:     []*discordgo.ApplicationCommandOption{messageIDOpt},
			},
			Handler: m.handleGEnd,
		},
		{
			Definition: &discordgo.ApplicationCommand{
				Name:        "greroll",
				Description: "Reroll the winners of a finished giveaway",
				Options: []*discordgo.ApplicationCommandOption{
					messageIDOpt,
					{Type: discordgo.ApplicationCommandOptionInteger, Name: "winners",
						Description: "How many new winners to draw", Required: false, MinValue: ptrFloat(1)},
				},
			},
			Handler: m.handleGReroll,
		},
		{
			Definition: &discordgo.ApplicationCommand{
				Name:        "glist",
				Description: "List currently running giveaways in this server",
			},
			Handler: m.handleGList,
		},
		{
			Definition: &discordgo.ApplicationCommand{
				Name:        "gdelete",
				Description: "Cancel a giveaway without drawing winners",
				Options:     []*discordgo.ApplicationCommandOption{messageIDOpt},
			},
			Handler: m.handleGDelete,
		},
	}
}

// RegisterModals wires the /gcreate modal-submit handler into the router.
// Called from main.go after the module is registered.
func (m *Module) RegisterModals(r *handler.Router) {
	r.OnModal(modalPrefix, m.handleGCreateSubmit)
}

func (m *Module) handleGStart(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if m.Worker == nil {
		handler.Reply(s, i, "Giveaway service is offline (no Worker connection).")
		return
	}
	opts := optionMap(i.ApplicationCommandData().Options)

	dur, err := parseDuration(opts["duration"].StringValue())
	if err != nil {
		handler.Reply(s, i, "Invalid duration: "+err.Error())
		return
	}
	winners := int(opts["winners"].IntValue())
	prize := opts["prize"].StringValue()
	lockChannel := false
	if lc, ok := opts["lock_channel"]; ok {
		lockChannel = lc.BoolValue()
	}

	endsAt := time.Now().Add(dur).Unix()
	host := interactionUserID(i)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	taskID, err := m.Worker.Enqueue(ctx, kindCreate, map[string]any{
		"guild_id":     i.GuildID,
		"channel_id":   i.ChannelID,
		"prize":        prize,
		"winner_count": winners,
		"ends_at_unix": endsAt,
		"hosted_by":    host,
		"lock_channel": lockChannel,
	})
	if err != nil {
		handler.Reply(s, i, "Couldn't create giveaway: "+err.Error())
		return
	}
	suffix := ""
	if lockChannel {
		suffix = " 🔒 channel will lock until it ends"
	}
	handler.Reply(s, i, fmt.Sprintf("🎉 Giveaway scheduled — `%s` for **%s**, ending <t:%d:R>.%s (job `%s`)",
		prize, dur, endsAt, suffix, taskID))
}

func (m *Module) handleGCreate(s *discordgo.Session, i *discordgo.InteractionCreate) {
	// The modal-submit interaction carries i.ChannelID = the channel where
	// the slash command was invoked, so no need to encode it in CustomID.
	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseModal,
		Data: &discordgo.InteractionResponseData{
			CustomID: modalPrefix,
			Title:    "Create giveaway",
			Components: []discordgo.MessageComponent{
				discordgo.ActionsRow{Components: []discordgo.MessageComponent{
					&discordgo.TextInput{
						CustomID: "prize", Label: "Prize",
						Style: discordgo.TextInputShort, Required: true,
						MaxLength:   256,
						Placeholder: "What are you giving away?",
					},
				}},
				discordgo.ActionsRow{Components: []discordgo.MessageComponent{
					&discordgo.TextInput{
						CustomID: "duration", Label: "Duration",
						Style: discordgo.TextInputShort, Required: true,
						MaxLength:   16,
						Placeholder: "30s, 10m, 2h, 1d, 1w (or 1d12h)",
					},
				}},
				discordgo.ActionsRow{Components: []discordgo.MessageComponent{
					&discordgo.TextInput{
						CustomID: "winners", Label: "Number of winners",
						Style: discordgo.TextInputShort, Required: true,
						MaxLength: 4, Value: "1",
					},
				}},
				discordgo.ActionsRow{Components: []discordgo.MessageComponent{
					&discordgo.TextInput{
						CustomID: "description", Label: "Description (optional)",
						Style: discordgo.TextInputParagraph, Required: false,
						MaxLength:   1500,
						Placeholder: "Click the button below to enter!",
					},
				}},
				discordgo.ActionsRow{Components: []discordgo.MessageComponent{
					&discordgo.TextInput{
						CustomID: "lock_channel", Label: "Lock channel during giveaway? (yes/no)",
						Style: discordgo.TextInputShort, Required: false,
						MaxLength: 5, Value: "no",
					},
				}},
			},
		},
	})
	if err != nil {
		// Can't fall back to handler.Reply — no ack was sent, so the
		// interaction will just time out for the user. Log and move on.
		slog.Error("gcreate modal open failed", "err", err)
	}
}

// handleGCreateSubmit processes the modal submission. Wired via RegisterModals.
func (m *Module) handleGCreateSubmit(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if m.Worker == nil {
		handler.Reply(s, i, "Giveaway service is offline.")
		return
	}
	values := modalValues(i.ModalSubmitData())
	prize := values["prize"]
	durationStr := values["duration"]
	winnersStr := values["winners"]
	description := values["description"]

	if prize == "" || durationStr == "" || winnersStr == "" {
		handler.Reply(s, i, "Prize, duration, and winners are required.")
		return
	}

	dur, err := parseDuration(durationStr)
	if err != nil {
		handler.Reply(s, i, "Invalid duration: "+err.Error())
		return
	}
	winners, err := strconv.Atoi(strings.TrimSpace(winnersStr))
	if err != nil || winners < 1 {
		handler.Reply(s, i, "Winners must be a positive integer.")
		return
	}
	lockChannel := parseYesNo(values["lock_channel"])

	endsAt := time.Now().Add(dur).Unix()
	host := interactionUserID(i)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	taskID, err := m.Worker.Enqueue(ctx, kindCreate, map[string]any{
		"guild_id":     i.GuildID,
		"channel_id":   i.ChannelID,
		"prize":        prize,
		"winner_count": winners,
		"ends_at_unix": endsAt,
		"hosted_by":    host,
		"description":  description,
		"lock_channel": lockChannel,
	})
	if err != nil {
		handler.Reply(s, i, "Couldn't create giveaway: "+err.Error())
		return
	}
	suffix := ""
	if lockChannel {
		suffix = " 🔒 channel locked until it ends."
	}
	handler.Reply(s, i, fmt.Sprintf("🎉 Giveaway scheduled.%s (job `%s`)", suffix, taskID))
}

func (m *Module) handleGEnd(s *discordgo.Session, i *discordgo.InteractionCreate) {
	m.dispatchByMessage(s, i, kindEnd, "Ending giveaway")
}

func (m *Module) handleGReroll(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if m.Worker == nil {
		handler.Reply(s, i, "Giveaway service is offline.")
		return
	}
	opts := optionMap(i.ApplicationCommandData().Options)
	messageID := opts["message_id"].StringValue()
	winners := 1
	if w, ok := opts["winners"]; ok {
		winners = int(w.IntValue())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	taskID, err := m.Worker.Enqueue(ctx, kindReroll, map[string]any{
		"guild_id":     i.GuildID,
		"channel_id":   i.ChannelID,
		"message_id":   messageID,
		"winner_count": winners,
	})
	if err != nil {
		handler.Reply(s, i, "Couldn't reroll: "+err.Error())
		return
	}
	handler.Reply(s, i, fmt.Sprintf("🔁 Reroll requested for message `%s`. (job `%s`)", messageID, taskID))
}

func (m *Module) handleGDelete(s *discordgo.Session, i *discordgo.InteractionCreate) {
	m.dispatchByMessage(s, i, kindDelete, "Deleting giveaway")
}

func (m *Module) handleGList(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if m.DB == nil {
		handler.Reply(s, i, "Giveaway listing requires a database connection — check the dashboard instead.")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	rows, err := m.DB.Query(ctx, `
		SELECT message_id, channel_id, prize, ends_at, winner_count
		FROM giveaways
		WHERE guild_id=$1 AND status='running'
		ORDER BY ends_at ASC
		LIMIT 25`, i.GuildID)
	if err != nil {
		handler.Reply(s, i, "Couldn't read giveaways: "+err.Error())
		return
	}
	defer rows.Close()

	var lines []string
	for rows.Next() {
		var messageID, channelID, prize string
		var endsAt time.Time
		var winners int
		if err := rows.Scan(&messageID, &channelID, &prize, &endsAt, &winners); err != nil {
			continue
		}
		lines = append(lines,
			fmt.Sprintf("• `%s` — **%s** (%d winners) in <#%s>, ends <t:%d:R>",
				messageID, prize, winners, channelID, endsAt.Unix()))
	}
	if len(lines) == 0 {
		handler.Reply(s, i, "No active giveaways in this server.")
		return
	}
	handler.Reply(s, i, "**Active giveaways**\n"+strings.Join(lines, "\n"))
}

func (m *Module) dispatchByMessage(s *discordgo.Session, i *discordgo.InteractionCreate, kind, verb string) {
	if m.Worker == nil {
		handler.Reply(s, i, "Giveaway service is offline.")
		return
	}
	opts := optionMap(i.ApplicationCommandData().Options)
	messageID := opts["message_id"].StringValue()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	taskID, err := m.Worker.Enqueue(ctx, kind, map[string]any{
		"guild_id":   i.GuildID,
		"channel_id": i.ChannelID,
		"message_id": messageID,
	})
	if err != nil {
		handler.Reply(s, i, fmt.Sprintf("%s failed: %v", verb, err))
		return
	}
	handler.Reply(s, i, fmt.Sprintf("%s for message `%s`. (job `%s`)", verb, messageID, taskID))
}

// --- helpers -----------------------------------------------------------------

func optionMap(opts []*discordgo.ApplicationCommandInteractionDataOption) map[string]*discordgo.ApplicationCommandInteractionDataOption {
	m := make(map[string]*discordgo.ApplicationCommandInteractionDataOption, len(opts))
	for _, o := range opts {
		m[o.Name] = o
	}
	return m
}

func modalValues(d discordgo.ModalSubmitInteractionData) map[string]string {
	out := map[string]string{}
	for _, row := range d.Components {
		ar, ok := row.(*discordgo.ActionsRow)
		if !ok {
			continue
		}
		for _, c := range ar.Components {
			if ti, ok := c.(*discordgo.TextInput); ok {
				out[ti.CustomID] = ti.Value
			}
		}
	}
	return out
}

func interactionUserID(i *discordgo.InteractionCreate) string {
	if i.Member != nil && i.Member.User != nil {
		return i.Member.User.ID
	}
	if i.User != nil {
		return i.User.ID
	}
	return ""
}

// parseDuration accepts GiveawayBot-style duration strings: "30s", "10m",
// "2h", "1d", "2w", or compounds like "1d12h". Empty/zero yields error.
func parseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return 0, fmt.Errorf("empty duration")
	}
	var total time.Duration
	var num strings.Builder
	for _, ch := range s {
		if ch >= '0' && ch <= '9' {
			num.WriteRune(ch)
			continue
		}
		if num.Len() == 0 {
			return 0, fmt.Errorf("expected number before %q in %q", ch, s)
		}
		n, err := strconv.Atoi(num.String())
		if err != nil {
			return 0, err
		}
		num.Reset()
		switch ch {
		case 's':
			total += time.Duration(n) * time.Second
		case 'm':
			total += time.Duration(n) * time.Minute
		case 'h':
			total += time.Duration(n) * time.Hour
		case 'd':
			total += time.Duration(n) * 24 * time.Hour
		case 'w':
			total += time.Duration(n) * 7 * 24 * time.Hour
		case ' ':
			// allow spaces between tokens
		default:
			return 0, fmt.Errorf("unknown unit %q", ch)
		}
	}
	if num.Len() > 0 {
		return 0, fmt.Errorf("missing unit after %q in %q", num.String(), s)
	}
	if total <= 0 {
		return 0, fmt.Errorf("duration must be positive")
	}
	return total, nil
}

// parseYesNo accepts y/yes/true/1/on as truthy; everything else is false.
func parseYesNo(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "y", "yes", "true", "1", "on":
		return true
	}
	return false
}

func ptrFloat(f float64) *float64 { return &f }
