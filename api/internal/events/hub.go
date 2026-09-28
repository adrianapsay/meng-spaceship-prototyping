// Package events fans out design events to live SSE subscribers.
//
// Postgres is the source of truth; the hub only wakes up connected clients.
// A subscriber that falls behind is dropped and can reconnect with
// Last-Event-ID to replay from the database. With more than one API instance
// this would move to Postgres LISTEN/NOTIFY.
package events

import (
	"sync"

	"github.com/google/uuid"

	"github.com/adrianapsay/meng-spaceship-prototyping/api/internal/store"
)

const bufferSize = 64

type Hub struct {
	mu   sync.Mutex
	subs map[uuid.UUID]map[chan store.Event]struct{}
}

func NewHub() *Hub {
	return &Hub{subs: map[uuid.UUID]map[chan store.Event]struct{}{}}
}

// Subscribe returns a channel of events for one design and a cancel func.
// The channel is closed on cancel or if the subscriber falls behind.
func (h *Hub) Subscribe(designID uuid.UUID) (<-chan store.Event, func()) {
	ch := make(chan store.Event, bufferSize)
	h.mu.Lock()
	if h.subs[designID] == nil {
		h.subs[designID] = map[chan store.Event]struct{}{}
	}
	h.subs[designID][ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() { h.remove(designID, ch) }
}

func (h *Hub) Publish(ev store.Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs[ev.DesignID] {
		select {
		case ch <- ev:
		default:
			delete(h.subs[ev.DesignID], ch)
			close(ch)
		}
	}
}

func (h *Hub) remove(designID uuid.UUID, ch chan store.Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.subs[designID][ch]; ok {
		delete(h.subs[designID], ch)
		close(ch)
	}
	if len(h.subs[designID]) == 0 {
		delete(h.subs, designID)
	}
}
