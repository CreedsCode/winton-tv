// Package chat provides an in-memory pub/sub hub for SSE chat delivery.
//
// Subscribers register per channel_slug; publishers fan-out to all
// subscribers for that slug. Buffer per subscriber is small; slow
// readers get dropped messages (their browser refetches via history
// API on reconnect).
//
// Single-process only. For multi-instance scaling, swap for Redis
// pub/sub (already available — LiveKit + Ingress use it).
package chat

import (
	"sync"

	"github.com/CreedsCode/winton-tv/internal/store"
)

type Hub struct {
	mu   sync.RWMutex
	subs map[string][]chan store.ChatMessage
}

func NewHub() *Hub {
	return &Hub{subs: make(map[string][]chan store.ChatMessage)}
}

// Subscribe returns a buffered channel that receives messages for the
// given channel_slug. Caller MUST Unsubscribe when done (use defer).
func (h *Hub) Subscribe(channel string) chan store.ChatMessage {
	ch := make(chan store.ChatMessage, 16)
	h.mu.Lock()
	h.subs[channel] = append(h.subs[channel], ch)
	h.mu.Unlock()
	return ch
}

func (h *Hub) Unsubscribe(channel string, ch chan store.ChatMessage) {
	h.mu.Lock()
	defer h.mu.Unlock()
	list := h.subs[channel]
	for i, c := range list {
		if c == ch {
			h.subs[channel] = append(list[:i], list[i+1:]...)
			if len(h.subs[channel]) == 0 {
				delete(h.subs, channel)
			}
			close(ch)
			return
		}
	}
}

// Publish fans out a message to all current subscribers for the channel.
// Non-blocking: full buffers drop the message for that subscriber
// (they'll catch up via /api/chat/{slug}/history on reconnect).
func (h *Hub) Publish(channel string, msg store.ChatMessage) {
	h.mu.RLock()
	list := append([]chan store.ChatMessage(nil), h.subs[channel]...)
	h.mu.RUnlock()
	for _, ch := range list {
		select {
		case ch <- msg:
		default:
		}
	}
}
