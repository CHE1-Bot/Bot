package leveling

import (
	"context"
	"log"
	"math"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/che1/bot/internal/db"
	"github.com/che1/bot/internal/handler"
	"github.com/che1/bot/internal/worker"
)

// XP gained per qualifying message, with a per-user cooldown to deter spam.
const (
	xpPerMessage = 15
	cooldown     = 60 * time.Second
)

type Module struct {
	DB     *db.DB
	Worker *worker.Queue

	mu         sync.Mutex
	lastAwards map[string]time.Time // userID -> last XP grant
}

func New(d *db.DB, w *worker.Queue) *Module {
	return &Module{DB: d, Worker: w, lastAwards: map[string]time.Time{}}
}

func (m *Module) Name() string { return "leveling" }

func (m *Module) Commands() []handler.SlashCommand {
	return []handler.SlashCommand{{
		Definition: &discordgo.ApplicationCommand{
			Name:        "rank",
			Description: "Show your level card",
		},
		Handler: m.handleRank,
	}}
}

// MessageCreate grants XP on each qualifying message and recomputes level.
// Writes go to the SAME Postgres `user_levels` table the Dashboard reads from,
// so the Dashboard sees updates live via its own pool.
func (m *Module) MessageCreate(s *discordgo.Session, msg *discordgo.MessageCreate) {
	if msg.GuildID == "" {
		return
	}
	if !m.takeToken(msg.Author.ID) {
		return
	}

	ctx := context.Background()
	var xp, level int64
	err := m.DB.QueryRow(ctx, `
		INSERT INTO user_levels (guild_id, user_id, xp, level, updated_at)
		VALUES ($1, $2, $3, 0, NOW())
		ON CONFLICT (guild_id, user_id) DO UPDATE
			SET xp = user_levels.xp + EXCLUDED.xp,
			    updated_at = NOW()
		RETURNING xp, level`,
		msg.GuildID, msg.Author.ID, xpPerMessage,
	).Scan(&xp, &level)
	if err != nil {
		log.Printf("xp upsert: %v", err)
		return
	}

	newLevel := levelFromXP(xp)
	if newLevel > level {
		_, err := m.DB.Exec(ctx,
			`UPDATE user_levels SET level=$1 WHERE guild_id=$2 AND user_id=$3`,
			newLevel, msg.GuildID, msg.Author.ID)
		if err != nil {
			log.Printf("level bump: %v", err)
			return
		}
		_, _ = s.ChannelMessageSend(msg.ChannelID,
			"<@"+msg.Author.ID+"> leveled up to **"+itoa(newLevel)+"**!")
	}
}

// takeToken returns true if the user is off cooldown; updates the timestamp.
func (m *Module) takeToken(userID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	if last, ok := m.lastAwards[userID]; ok && now.Sub(last) < cooldown {
		return false
	}
	m.lastAwards[userID] = now
	return true
}

// Level curve: level = floor(sqrt(xp / 100)).
func levelFromXP(xp int64) int64 {
	return int64(math.Floor(math.Sqrt(float64(xp) / 100.0)))
}

func (m *Module) handleRank(s *discordgo.Session, i *discordgo.InteractionCreate) {
	ctx := context.Background()
	var xp, level int64
	err := m.DB.QueryRow(ctx,
		`SELECT xp, level FROM user_levels WHERE guild_id=$1 AND user_id=$2`,
		i.GuildID, i.Member.User.ID,
	).Scan(&xp, &level)
	if err != nil {
		replyEphemeral(s, i, "No rank yet — send some messages first.")
		return
	}

	// Delegate PNG rendering to the Worker; reply with its URL when ready.
	jobID, err := m.Worker.Enqueue(ctx, worker.JobLevelCard, map[string]any{
		"user_id":  i.Member.User.ID,
		"username": i.Member.User.Username,
		"avatar":   i.Member.User.AvatarURL("256"),
		"xp":       xp,
		"level":    level,
	})
	if err != nil {
		replyEphemeral(s, i, "Worker error: "+err.Error())
		return
	}
	replyEphemeral(s, i, "Generating card (job "+jobID+")...")
}

func replyEphemeral(s *discordgo.Session, i *discordgo.InteractionCreate, msg string) {
	handler.Reply(s, i, msg)
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
