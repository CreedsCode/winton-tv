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
	for _, g := range r.Guilds {
		if g.ID != b.guildID {
			continue
		}
		b.mu.Lock()
		// reset voice state map and refill from READY snapshot
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
	// Fetch channel list separately (READY only has IDs sometimes)
	b.refreshChannels()
}

func (b *Bot) onResumed(s *discordgo.Session, r *discordgo.Resumed) {
	b.logger.Info("discord resumed")
}

func (b *Bot) onDisconnect(s *discordgo.Session, d *discordgo.Disconnect) {
	b.logger.Warn("discord disconnected — discordgo will auto-reconnect")
}

func (b *Bot) onVoiceStateUpdate(s *discordgo.Session, vsu *discordgo.VoiceStateUpdate) {
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
		b.logger.Warn("discord: refresh channels", "err", err)
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, c := range chans {
		if c.Type == discordgo.ChannelTypeGuildVoice {
			b.channels[c.ID] = c
		}
	}
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
