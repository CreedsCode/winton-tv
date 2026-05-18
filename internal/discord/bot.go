// Package discord wraps a discordgo Gateway connection to track which
// guild members are currently in which voice channels. We only need the
// VOICE_STATES / GUILDS intents (both non-privileged) — no message
// reading or member intent required.
//
// State is held in memory; on (re)connect the READY event re-populates
// the full snapshot, then VoiceStateUpdate events keep it fresh.
package discord

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"sync"

	"github.com/bwmarrin/discordgo"
)

type Bot struct {
	session *discordgo.Session
	guildID string
	logger  *slog.Logger

	mu        sync.RWMutex
	userToCh  map[string]string             // discord_user_id -> channel_id
	channels  map[string]*discordgo.Channel // channel_id -> channel (voice only)
}

// VoiceChannel is the API surface — channels with at least one member.
type VoiceChannel struct {
	ID      string
	Name    string
	Members []string // discord_user_ids, sorted for stable order
}

func New(token, guildID string, logger *slog.Logger) (*Bot, error) {
	s, err := discordgo.New("Bot " + token)
	if err != nil {
		return nil, fmt.Errorf("discordgo new: %w", err)
	}
	s.Identify.Intents = discordgo.IntentsGuilds | discordgo.IntentsGuildVoiceStates

	b := &Bot{
		session:  s,
		guildID:  guildID,
		logger:   logger,
		userToCh: make(map[string]string),
		channels: make(map[string]*discordgo.Channel),
	}

	s.AddHandler(b.onReady)
	s.AddHandler(b.onGuildCreate)
	s.AddHandler(b.onVoiceStateUpdate)
	s.AddHandler(b.onChannelCreate)
	s.AddHandler(b.onChannelUpdate)
	s.AddHandler(b.onChannelDelete)
	s.AddHandler(b.onDisconnect)
	s.AddHandler(b.onResumed)

	return b, nil
}

// Start opens the gateway connection (non-blocking — runs in its own goroutine).
func (b *Bot) Start(ctx context.Context) error {
	if err := b.session.Open(); err != nil {
		return fmt.Errorf("discord open: %w", err)
	}
	b.logger.Info("discord bot connected", "guild_id", b.guildID)
	return nil
}

func (b *Bot) Close() error {
	if b.session == nil {
		return nil
	}
	return b.session.Close()
}

// ─────────────────────── handlers ───────────────────────

func (b *Bot) onReady(s *discordgo.Session, r *discordgo.Ready) {
	b.logger.Info("discord ready event",
		"guild_count", len(r.Guilds),
		"session_id", r.SessionID,
		"user", r.User.Username)
	matched := false
	for _, g := range r.Guilds {
		b.logger.Info("ready: guild",
			"id", g.ID, "name", g.Name,
			"voice_states", len(g.VoiceStates), "channels", len(g.Channels),
			"matches", g.ID == b.guildID)
		if g.ID != b.guildID {
			continue
		}
		matched = true
		b.mu.Lock()
		b.userToCh = make(map[string]string, len(g.VoiceStates))
		for _, vs := range g.VoiceStates {
			if vs.ChannelID != "" {
				b.userToCh[vs.UserID] = vs.ChannelID
			}
		}
		b.mu.Unlock()
		b.logger.Info("discord ready: voice states snapshotted",
			"voice_count", len(g.VoiceStates))
	}
	if !matched {
		b.logger.Warn("discord ready: target guild NOT in ready event — bot might not be a member of guild_id "+b.guildID,
			"guild_id_expected", b.guildID)
	}
	b.refreshChannels()
}

func (b *Bot) onResumed(s *discordgo.Session, r *discordgo.Resumed) {
	b.logger.Info("discord resumed")
}

// onGuildCreate handles the GUILD_CREATE event that fires for every
// guild the bot is in shortly after READY. This is where Discord
// actually delivers the voice states + channel list — READY only sends
// stub guild objects (id, no name, empty voice_states/channels).
//
// Fires:
//   - Once per guild on initial connect (right after READY)
//   - Whenever the bot is added to a new guild
//   - When the bot regains access to a previously-unavailable guild
func (b *Bot) onGuildCreate(s *discordgo.Session, g *discordgo.GuildCreate) {
	if g.ID != b.guildID {
		return
	}
	voiceCount := 0
	channelCount := 0
	b.mu.Lock()
	// Re-snapshot voice states from the authoritative GUILD_CREATE payload
	b.userToCh = make(map[string]string, len(g.VoiceStates))
	for _, vs := range g.VoiceStates {
		if vs.ChannelID != "" {
			b.userToCh[vs.UserID] = vs.ChannelID
			voiceCount++
		}
	}
	// Snapshot voice channels too
	for _, c := range g.Channels {
		if c.Type == discordgo.ChannelTypeGuildVoice {
			b.channels[c.ID] = c
			channelCount++
		}
	}
	b.mu.Unlock()
	b.logger.Info("guild create",
		"name", g.Name,
		"voice_states", voiceCount,
		"voice_channels", channelCount,
		"total_channels", len(g.Channels))
}

func (b *Bot) onDisconnect(s *discordgo.Session, d *discordgo.Disconnect) {
	b.logger.Warn("discord disconnected — discordgo will auto-reconnect")
}

func (b *Bot) onVoiceStateUpdate(s *discordgo.Session, vsu *discordgo.VoiceStateUpdate) {
	b.logger.Info("voice state update",
		"guild_id", vsu.GuildID,
		"user_id", vsu.UserID,
		"channel_id", vsu.ChannelID,
		"matches_target", vsu.GuildID == b.guildID)
	if vsu.GuildID != b.guildID {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if vsu.ChannelID == "" {
		delete(b.userToCh, vsu.UserID)
	} else {
		b.userToCh[vsu.UserID] = vsu.ChannelID
	}
}

func (b *Bot) onChannelCreate(s *discordgo.Session, c *discordgo.ChannelCreate) {
	if c.GuildID != b.guildID || c.Type != discordgo.ChannelTypeGuildVoice {
		return
	}
	b.mu.Lock()
	b.channels[c.ID] = c.Channel
	b.mu.Unlock()
}

func (b *Bot) onChannelUpdate(s *discordgo.Session, c *discordgo.ChannelUpdate) {
	if c.GuildID != b.guildID || c.Type != discordgo.ChannelTypeGuildVoice {
		return
	}
	b.mu.Lock()
	b.channels[c.ID] = c.Channel
	b.mu.Unlock()
}

func (b *Bot) onChannelDelete(s *discordgo.Session, c *discordgo.ChannelDelete) {
	if c.GuildID != b.guildID {
		return
	}
	b.mu.Lock()
	delete(b.channels, c.ID)
	b.mu.Unlock()
}

func (b *Bot) refreshChannels() {
	chans, err := b.session.GuildChannels(b.guildID)
	if err != nil {
		b.logger.Warn("discord: refresh channels — bot may lack VIEW_CHANNEL or guild access",
			"guild_id", b.guildID, "err", err)
		return
	}
	voiceCount := 0
	b.mu.Lock()
	for _, c := range chans {
		if c.Type == discordgo.ChannelTypeGuildVoice {
			b.channels[c.ID] = c
			voiceCount++
		}
	}
	b.mu.Unlock()
	b.logger.Info("discord channels refreshed",
		"total", len(chans), "voice", voiceCount)
}

// ─────────────────────── API for handlers ───────────────────────

// ActiveVoiceChannels returns voice channels with at least one member,
// sorted by channel position then name.
func (b *Bot) ActiveVoiceChannels() []VoiceChannel {
	b.mu.RLock()
	defer b.mu.RUnlock()

	byCh := make(map[string][]string)
	for uid, cid := range b.userToCh {
		byCh[cid] = append(byCh[cid], uid)
	}

	out := make([]VoiceChannel, 0, len(byCh))
	for cid, members := range byCh {
		ch, ok := b.channels[cid]
		if !ok {
			continue
		}
		sort.Strings(members)
		out = append(out, VoiceChannel{
			ID:      cid,
			Name:    ch.Name,
			Members: members,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		// Position from channel struct gives stable Discord-side ordering
		ci := b.channels[out[i].ID]
		cj := b.channels[out[j].ID]
		if ci.Position != cj.Position {
			return ci.Position < cj.Position
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// UsersInChannel returns the user IDs currently in `channelID` (empty if
// no such channel / nobody there).
func (b *Bot) UsersInChannel(channelID string) []string {
	b.mu.RLock()
	defer b.mu.RUnlock()

	var out []string
	for uid, cid := range b.userToCh {
		if cid == channelID {
			out = append(out, uid)
		}
	}
	sort.Strings(out)
	return out
}

// ChannelName returns the voice channel's display name, ok=false if unknown.
func (b *Bot) ChannelName(channelID string) (string, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	c, ok := b.channels[channelID]
	if !ok {
		return "", false
	}
	return c.Name, true
}
