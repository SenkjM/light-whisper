// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

// Package events is the in-process broadcast hub behind WS /v1/events.
package events

import (
	"sync"
	"time"
)

// Event types defined by PLAN §4.1.
const (
	TypeEngineStatus     = "engine_status"
	TypeConfigChanged    = "config_changed"
	TypeDownloadProgress = "download_progress"
	TypeJobProgress      = "job_progress"
)

// Event is one message on the event stream.
type Event struct {
	Seq  uint64    `json:"seq"`
	Type string    `json:"type"`
	Time time.Time `json:"time"`
	Data any       `json:"data,omitempty"`
}

// Subscription receives events on C. C is closed when the subscription is
// cancelled, the hub is closed, or the subscriber fell behind (buffer full);
// in the last case Dropped() is true and the client should resync via the
// status / config endpoints and reconnect.
type Subscription struct {
	C       <-chan Event
	ch      chan Event
	hub     *Hub
	dropped bool
	closed  bool
}

// Dropped reports whether the subscription was closed for being too slow.
func (s *Subscription) Dropped() bool {
	s.hub.mu.Lock()
	defer s.hub.mu.Unlock()
	return s.dropped
}

// Cancel unsubscribes. Safe to call more than once.
func (s *Subscription) Cancel() {
	s.hub.mu.Lock()
	defer s.hub.mu.Unlock()
	s.hub.removeLocked(s)
}

// Hub fans events out to subscribers without ever blocking publishers.
type Hub struct {
	mu     sync.Mutex
	seq    uint64
	subs   map[*Subscription]struct{}
	closed bool
	now    func() time.Time
}

// NewHub creates a hub.
func NewHub() *Hub {
	return &Hub{subs: map[*Subscription]struct{}{}, now: time.Now}
}

// Subscribe registers a subscriber with the given buffer size.
func (h *Hub) Subscribe(buffer int) *Subscription {
	if buffer < 1 {
		buffer = 1
	}
	ch := make(chan Event, buffer)
	s := &Subscription{C: ch, ch: ch, hub: h}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		s.closed = true
		close(ch)
		return s
	}
	h.subs[s] = struct{}{}
	return s
}

func (h *Hub) removeLocked(s *Subscription) {
	if s.closed {
		return
	}
	s.closed = true
	delete(h.subs, s)
	close(s.ch)
}

// Publish sends an event to all subscribers and returns it.
func (h *Hub) Publish(typ string, data any) Event {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.seq++
	ev := Event{Seq: h.seq, Type: typ, Time: h.now().UTC(), Data: data}
	for s := range h.subs {
		select {
		case s.ch <- ev:
		default:
			s.dropped = true
			h.removeLocked(s)
		}
	}
	return ev
}

// Subscribers returns the current subscriber count.
func (h *Hub) Subscribers() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs)
}

// Close closes every subscription; later Subscribe calls get a closed channel.
func (h *Hub) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.closed = true
	for s := range h.subs {
		h.removeLocked(s)
	}
}
