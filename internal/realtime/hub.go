// Package realtime routes collected values to interested subscribers.
package realtime

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// ValueEvent is one collected point observation.
type ValueEvent struct {
	DeviceID  int64     `json:"device_id"`
	TagName   string    `json:"tag_name"`
	Value     any       `json:"value"`
	UpdatedAt time.Time `json:"updated_at"`
}
type key struct {
	deviceID int64
	tag      string
}

// Hub owns the subscription index.
type Hub struct {
	mu            sync.RWMutex
	max           int
	subscriptions map[key]map[*Subscription]struct{}
}

// Subscription is one bounded event consumer.
type Subscription struct {
	hub    *Hub
	events chan ValueEvent
	done   chan struct{}
	once   sync.Once
	keys   map[key]struct{}
}

// NewHub creates a Hub with a per-connection subscription limit.
func NewHub(max int) *Hub {
	return &Hub{max: max, subscriptions: make(map[key]map[*Subscription]struct{})}
}

// Subscribe registers a consumer with the supplied queue capacity.
func (h *Hub) Subscribe(buffer int) *Subscription {
	if buffer < 1 {
		buffer = 1
	}
	return &Subscription{hub: h, events: make(chan ValueEvent, buffer), done: make(chan struct{}), keys: make(map[key]struct{})}
}

// Events returns collected observations.
func (s *Subscription) Events() <-chan ValueEvent { return s.events }

// Done closes when the subscription is closed.
func (s *Subscription) Done() <-chan struct{} { return s.done }

// Set adds exact device/tag subscriptions.
func (s *Subscription) Set(deviceID int64, tags []string) error {
	if deviceID < 1 {
		return fmt.Errorf("device ID must be positive")
	}
	clean := make([]string, 0, len(tags))
	requested := make(map[string]struct{}, len(tags))
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			return fmt.Errorf("tag must not be empty")
		}
		if _, exists := requested[tag]; exists {
			continue
		}
		requested[tag] = struct{}{}
		clean = append(clean, tag)
	}
	s.hub.mu.Lock()
	defer s.hub.mu.Unlock()
	newKeys := 0
	for _, tag := range clean {
		if _, ok := s.keys[key{deviceID, tag}]; !ok {
			newKeys++
		}
	}
	if len(s.keys)+newKeys > s.hub.max {
		return fmt.Errorf("subscription limit exceeded")
	}
	for _, tag := range clean {
		k := key{deviceID, tag}
		s.keys[k] = struct{}{}
		set := s.hub.subscriptions[k]
		if set == nil {
			set = make(map[*Subscription]struct{})
			s.hub.subscriptions[k] = set
		}
		set[s] = struct{}{}
	}
	return nil
}

// Remove deletes exact subscriptions.
func (s *Subscription) Remove(deviceID int64, tags []string) {
	s.hub.mu.Lock()
	defer s.hub.mu.Unlock()
	for _, tag := range tags {
		k := key{deviceID, strings.TrimSpace(tag)}
		delete(s.keys, k)
		if set := s.hub.subscriptions[k]; set != nil {
			delete(set, s)
			if len(set) == 0 {
				delete(s.hub.subscriptions, k)
			}
		}
	}
}

// Close unregisters the consumer.
func (s *Subscription) Close() {
	s.once.Do(func() {
		s.hub.mu.Lock()
		for k := range s.keys {
			delete(s.hub.subscriptions[k], s)
			if len(s.hub.subscriptions[k]) == 0 {
				delete(s.hub.subscriptions, k)
			}
		}
		s.keys = make(map[key]struct{})
		close(s.done)
		s.hub.mu.Unlock()
	})
}

// Publish delivers without blocking the producer; slow consumers are closed.
func (h *Hub) Publish(event ValueEvent) {
	h.mu.RLock()
	targets := make([]*Subscription, 0, len(h.subscriptions[key{event.DeviceID, event.TagName}]))
	for s := range h.subscriptions[key{event.DeviceID, event.TagName}] {
		targets = append(targets, s)
	}
	h.mu.RUnlock()
	for _, s := range targets {
		select {
		case <-s.done:
		case s.events <- event:
		default:
			s.Close()
		}
	}
}
