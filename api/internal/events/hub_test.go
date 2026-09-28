package events

import (
	"testing"

	"github.com/google/uuid"

	"github.com/adrianapsay/meng-spaceship-prototyping/api/internal/store"
)

func TestPublishReachesOnlyThatDesignsSubscribers(t *testing.T) {
	h := NewHub()
	a, b := uuid.New(), uuid.New()
	subA, cancelA := h.Subscribe(a)
	defer cancelA()
	subB, cancelB := h.Subscribe(b)
	defer cancelB()

	h.Publish(store.Event{ID: 1, DesignID: a})
	if ev := <-subA; ev.ID != 1 {
		t.Fatalf("got %+v", ev)
	}
	select {
	case ev := <-subB:
		t.Fatalf("leaked event %+v", ev)
	default:
	}
}

func TestSlowSubscriberIsDropped(t *testing.T) {
	h := NewHub()
	id := uuid.New()
	sub, cancel := h.Subscribe(id)
	defer cancel()
	for i := 0; i <= bufferSize; i++ {
		h.Publish(store.Event{ID: int64(i), DesignID: id})
	}
	n := 0
	for range sub {
		n++
	}
	if n != bufferSize {
		t.Fatalf("drained %d events, want %d before close", n, bufferSize)
	}
}
