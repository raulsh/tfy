// Package events fans live updates out to UI subscribers.
package events

import (
	"encoding/json"
	"sync"
)

// Topics.
const (
	// TopicGlobal carries entity changes (a unit moved, a run finished) so
	// the UI knows what to refetch.
	TopicGlobal = "global"
)

// RunTopic carries one run's stream events and status changes.
func RunTopic(runID string) string { return "run:" + runID }

// Message is one update.
type Message struct {
	Kind string          `json:"kind"` // unit, run, project, feedback, event, status
	ID   string          `json:"id"`
	Seq  int64           `json:"seq,omitempty"`
	Data json.RawMessage `json:"data,omitempty"`
}

// Subscription receives messages on C until it is closed — by the
// subscriber, or by the hub when the subscriber falls behind. A closed
// subscription means "reconnect and replay", never "some messages are gone
// silently".
type Subscription struct {
	C     <-chan Message
	c     chan Message
	topic string
	hub   *Hub
	once  sync.Once
}

// Close unsubscribes. It is safe to call more than once.
func (s *Subscription) Close() {
	s.hub.remove(s)
}

// Hub is an in-process publish/subscribe broker.
type Hub struct {
	mu   sync.Mutex
	subs map[string]map[*Subscription]struct{}
}

// NewHub creates a hub.
func NewHub() *Hub {
	return &Hub{subs: map[string]map[*Subscription]struct{}{}}
}

// Subscribe registers for a topic with a buffer of size buf.
func (h *Hub) Subscribe(topic string, buf int) *Subscription {
	c := make(chan Message, buf)
	s := &Subscription{C: c, c: c, topic: topic, hub: h}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.subs[topic] == nil {
		h.subs[topic] = map[*Subscription]struct{}{}
	}
	h.subs[topic][s] = struct{}{}
	return s
}

// Publish delivers m to every subscriber of topic without blocking.
func (h *Hub) Publish(topic string, m Message) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for s := range h.subs[topic] {
		select {
		case s.c <- m:
		default:
			// Too slow: cut it off so it reconnects and replays.
			h.removeLocked(s)
		}
	}
}

// PublishJSON marshals data into the message.
func (h *Hub) PublishJSON(topic, kind, id string, data any) {
	raw, _ := json.Marshal(data)
	h.Publish(topic, Message{Kind: kind, ID: id, Data: raw})
}

func (h *Hub) remove(s *Subscription) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.removeLocked(s)
}

func (h *Hub) removeLocked(s *Subscription) {
	if subs := h.subs[s.topic]; subs != nil {
		if _, ok := subs[s]; ok {
			delete(subs, s)
			if len(subs) == 0 {
				delete(h.subs, s.topic)
			}
		}
	}
	s.once.Do(func() { close(s.c) })
}
