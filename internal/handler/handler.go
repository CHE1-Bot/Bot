package handler

import (
	"fmt"
	"log/slog"
	"runtime/debug"
	"strings"

	"github.com/bwmarrin/discordgo"
)

const thinkingMessage = "CHE1 is thinking..."

// SlashCommand is implemented by each feature package (tickets, leveling, ...).
type SlashCommand struct {
	Definition *discordgo.ApplicationCommand
	Handler    func(s *discordgo.Session, i *discordgo.InteractionCreate)
	// NoAck skips the auto "CHE1 is thinking..." response — required for
	// commands that respond with a modal (Discord rejects a modal response
	// after the interaction has already been acknowledged).
	NoAck bool
}

type Module interface {
	Name() string
	Commands() []SlashCommand
	// MessageCreate is optional; modules that don't care can no-op.
	MessageCreate(s *discordgo.Session, m *discordgo.MessageCreate)
}

// ComponentHandler runs when a message component (button/select) is clicked
// and its CustomID has the registered prefix.
type ComponentHandler func(s *discordgo.Session, i *discordgo.InteractionCreate)

// ModalHandler runs when a modal is submitted and its CustomID has the
// registered prefix.
type ModalHandler func(s *discordgo.Session, i *discordgo.InteractionCreate)

type Router struct {
	modules    []Module
	commands   map[string]SlashCommand
	components map[string]ComponentHandler // keyed by CustomID prefix
	modals     map[string]ModalHandler     // keyed by CustomID prefix
}

func New() *Router {
	return &Router{
		commands:   map[string]SlashCommand{},
		components: map[string]ComponentHandler{},
		modals:     map[string]ModalHandler{},
	}
}

func (r *Router) Register(m Module) {
	r.modules = append(r.modules, m)
	for _, c := range m.Commands() {
		r.commands[c.Definition.Name] = c
	}
}

// OnComponent registers a handler for any component interaction whose
// CustomID begins with prefix (matched against "prefix" or "prefix:...").
func (r *Router) OnComponent(prefix string, h ComponentHandler) {
	r.components[prefix] = h
}

// OnModal registers a handler for any modal-submit interaction whose
// CustomID begins with prefix.
func (r *Router) OnModal(prefix string, h ModalHandler) {
	r.modals[prefix] = h
}

func (r *Router) Attach(s *discordgo.Session, guildID string) error {
	s.AddHandler(func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		switch i.Type {
		case discordgo.InteractionApplicationCommand:
			r.handleCommand(s, i)
		case discordgo.InteractionMessageComponent:
			r.handleComponent(s, i)
		case discordgo.InteractionModalSubmit:
			r.handleModal(s, i)
		}
	})
	s.AddHandler(func(s *discordgo.Session, m *discordgo.MessageCreate) {
		if m.Author == nil || m.Author.Bot {
			return
		}
		for _, mod := range r.modules {
			mod := mod
			func() {
				defer recoverInteraction("message handler", "module", mod.Name())
				mod.MessageCreate(s, m)
			}()
		}
	})

	defs := make([]*discordgo.ApplicationCommand, 0, len(r.commands))
	for _, c := range r.commands {
		defs = append(defs, c.Definition)
	}
	_, err := s.ApplicationCommandBulkOverwrite(s.State.User.ID, guildID, defs)
	if err != nil {
		slog.Error("command registration failed", "err", err)
	}
	return err
}

func (r *Router) handleCommand(s *discordgo.Session, i *discordgo.InteractionCreate) {
	name := i.ApplicationCommandData().Name
	cmd, ok := r.commands[name]
	if !ok {
		return
	}
	if !cmd.NoAck {
		if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: thinkingMessage,
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		}); err != nil {
			slog.Error("thinking ack failed", "command", name, "err", err)
			return
		}
		defer recoverInteractionReply(s, i, "command", "name", name)
	} else {
		defer recoverInteraction("command", "name", name)
	}
	cmd.Handler(s, i)
}

func (r *Router) handleModal(s *discordgo.Session, i *discordgo.InteractionCreate) {
	id := i.ModalSubmitData().CustomID
	prefix := id
	if idx := strings.Index(id, ":"); idx >= 0 {
		prefix = id[:idx]
	}
	h, ok := r.modals[prefix]
	if !ok {
		return
	}
	if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: thinkingMessage,
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	}); err != nil {
		slog.Error("thinking ack failed", "modal", id, "err", err)
		return
	}
	defer recoverInteractionReply(s, i, "modal", "custom_id", id)
	h(s, i)
}

func (r *Router) handleComponent(s *discordgo.Session, i *discordgo.InteractionCreate) {
	id := i.MessageComponentData().CustomID
	prefix := id
	if idx := strings.Index(id, ":"); idx >= 0 {
		prefix = id[:idx]
	}
	h, ok := r.components[prefix]
	if !ok {
		return
	}
	if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: thinkingMessage,
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	}); err != nil {
		slog.Error("thinking ack failed", "component", id, "err", err)
		return
	}
	defer recoverInteractionReply(s, i, "component", "custom_id", id)
	h(s, i)
}

// Reply edits the initial "CHE1 is thinking..." ack with the real response.
// Handlers should call this instead of InteractionRespond.
func Reply(s *discordgo.Session, i *discordgo.InteractionCreate, content string) {
	_, err := s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
		Content: &content,
	})
	if err != nil {
		slog.Error("reply edit failed", "err", err)
	}
}

func recoverInteraction(where string, attrs ...any) {
	if r := recover(); r != nil {
		args := append([]any{"where", where, "panic", fmt.Sprint(r), "stack", string(debug.Stack())}, attrs...)
		slog.Error("recovered from panic", args...)
	}
}

// recoverInteractionReply also lets the user know something went wrong
// instead of leaving them staring at "CHE1 is thinking..." forever.
func recoverInteractionReply(s *discordgo.Session, i *discordgo.InteractionCreate, where string, attrs ...any) {
	if r := recover(); r != nil {
		args := append([]any{"where", where, "panic", fmt.Sprint(r), "stack", string(debug.Stack())}, attrs...)
		slog.Error("recovered from panic", args...)
		Reply(s, i, "Something went wrong. The team has been notified.")
	}
}
