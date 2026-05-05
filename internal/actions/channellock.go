package actions

import (
	"fmt"

	"github.com/bwmarrin/discordgo"
)

// lockChannel adds the SEND_MESSAGES deny bit to @everyone's permission
// overwrite, so non-staff can read but not type while a giveaway runs.
// Other bits in the overwrite are preserved.
//
// guildID doubles as the @everyone role ID (Discord convention).
func lockChannel(s *discordgo.Session, guildID, channelID string) error {
	allow, deny, err := everyoneOverwrite(s, guildID, channelID)
	if err != nil {
		return err
	}
	// Clear allow bit (in case it was explicitly granted), set deny bit.
	allow &^= discordgo.PermissionSendMessages
	deny |= discordgo.PermissionSendMessages
	return s.ChannelPermissionSet(channelID, guildID, discordgo.PermissionOverwriteTypeRole, allow, deny)
}

// unlockChannel removes the SEND_MESSAGES deny bit from @everyone's
// overwrite. If no overwrite existed (or no deny was set), this is a no-op.
func unlockChannel(s *discordgo.Session, guildID, channelID string) error {
	allow, deny, err := everyoneOverwrite(s, guildID, channelID)
	if err != nil {
		return err
	}
	if deny&discordgo.PermissionSendMessages == 0 {
		// Nothing to unlock.
		return nil
	}
	deny &^= discordgo.PermissionSendMessages

	// If the overwrite is now completely empty, delete it instead of
	// leaving an empty rule polluting the channel's overrides.
	if allow == 0 && deny == 0 {
		return s.ChannelPermissionDelete(channelID, guildID)
	}
	return s.ChannelPermissionSet(channelID, guildID, discordgo.PermissionOverwriteTypeRole, allow, deny)
}

func everyoneOverwrite(s *discordgo.Session, guildID, channelID string) (allow, deny int64, err error) {
	ch, cerr := s.Channel(channelID)
	if cerr != nil {
		return 0, 0, fmt.Errorf("fetch channel: %w", cerr)
	}
	for _, ow := range ch.PermissionOverwrites {
		if ow.ID == guildID && ow.Type == discordgo.PermissionOverwriteTypeRole {
			return ow.Allow, ow.Deny, nil
		}
	}
	// No existing overwrite for @everyone — return zero, caller will create.
	return 0, 0, nil
}
