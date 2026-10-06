// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

package events

import "testing"

func TestPublishDeliversInOrder(t *testing.T) {
	h := NewHub()
	a, b := h.Subscribe(8), h.Subscribe(8)
	h.Publish(TypeConfigChanged, 1)
	h.Publish(TypeEngineStatus, 2)
	for _, s := range []*Subscription{a, b} {
		e1, e2 := <-s.C, <-s.C
		if e1.Seq != 1 || e1.Type != TypeConfigChanged || e2.Seq != 2 || e2.Data != 2 {
			t.Fatalf("got %+v %+v", e1, e2)
		}
	}
	a.Cancel()
	a.Cancel() // idempotent
	if _, ok := <-a.C; ok {
		t.Fatal("cancelled channel not closed")
	}
	if h.Subscribers() != 1 {
		t.Fatalf("subscribers = %d", h.Subscribers())
	}
}

func TestSlowSubscriberIsDroppedNotBlocking(t *testing.T) {
	h := NewHub()
	slow := h.Subscribe(1)
	fast := h.Subscribe(16)
	for i := 0; i < 5; i++ {
		h.Publish(TypeJobProgress, i) // must never block
	}
	if !slow.Dropped() {
		t.Fatal("slow subscriber should be dropped")
	}
	n := 0
	for range slow.C {
		n++
	}
	if n != 1 {
		t.Fatalf("slow got %d buffered events", n)
	}
	if len(fast.C) != 5 || fast.Dropped() {
		t.Fatalf("fast subscriber affected: %d dropped=%v", len(fast.C), fast.Dropped())
	}
}

func TestCloseClosesAll(t *testing.T) {
	h := NewHub()
	s := h.Subscribe(1)
	h.Close()
	if _, ok := <-s.C; ok {
		t.Fatal("not closed")
	}
	late := h.Subscribe(1)
	if _, ok := <-late.C; ok {
		t.Fatal("subscribe after close should be closed")
	}
	if s.Dropped() {
		t.Fatal("close is not a drop")
	}
}
