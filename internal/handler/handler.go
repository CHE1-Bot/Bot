package handler

import (
	"log"

	"github.com/bwmarrin/discordgo"
)

// SlashCommand is implemented by each feature package (tickets, leveling, ...).
type SlashCommand struct {
	Definition *discordgo.ApplicationCommand
	Handler    func(s *discordgo.Session, i *discordgo.InteractionCreate)
}

type Module interface {
	Name() string
	Commands() []SlashCommand
	// MessageCreate is optional; modules that don't care can no-op.
	MessageCreate(s *discordgo.Session, m *discordgo.MessageCreate)
}

type Router struct {
	modules  []Module
	commands map[string]SlashCommand
}

func New() *Router {
	return &Router{commands: map[string]SlashCommand{}}
}

func (r *Router) Register(m Module) {
	r.modules = append(r.modules, m)
	for _, c := range m.Commands() {
		r.commands[c.Definition.Name] = c
	}
}

func (r *Router) Attach(s *discordgo.Session, guildID string) error {
	s.AddHandler(func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		if i.Type != discordgo.InteractionApplicationCommand {
			return
		}
		cmd, ok := r.commands[i.ApplicationCommandData().Name]
		if !ok {
			return
		}
		cmd.Handler(s, i)
	})
	s.AddHandler(func(s *discordgo.Session, m *discordgo.MessageCreate) {
		if m.Author == nil || m.Author.Bot {
			return
		}
		for _, mod := range r.modules {
			mod.MessageCreate(s, m)
		}
	})

	defs := make([]*discordgo.ApplicationCommand, 0, len(r.commands))
	for _, c := range r.commands {
		defs = append(defs, c.Definition)
	}
	_, err := s.ApplicationCommandBulkOverwrite(s.State.User.ID, guildID, defs)
	if err != nil {
		log.Printf("command registration failed: %v", err)
	}
	return err
}
