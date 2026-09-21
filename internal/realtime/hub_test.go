package realtime

import (
	"testing"
	"time"
)

func TestHubRoutesAndUnsubscribes(t *testing.T) {
	h := NewHub(500)
	s := h.Subscribe(2)
	defer s.Close()
	if err := s.Set(1, []string{"Speed"}); err != nil { t.Fatal(err) }
	h.Publish(ValueEvent{DeviceID: 2, TagName: "Speed", Value: 1})
	h.Publish(ValueEvent{DeviceID: 1, TagName: "Other", Value: 2})
	h.Publish(ValueEvent{DeviceID: 1, TagName: "Speed", Value: 3})
	select {
	case got := <-s.Events():
		if got.Value != 3 { t.Fatalf("event=%#v", got) }
	case <-time.After(250 * time.Millisecond): t.Fatal("missing event")
	}
	s.Remove(1, []string{"Speed"})
	h.Publish(ValueEvent{DeviceID: 1, TagName: "Speed", Value: 4})
	select { case got := <-s.Events(): t.Fatalf("unexpected event=%#v", got); case <-time.After(25*time.Millisecond): }
}

func TestHubSlowSubscriptionDoesNotBlock(t *testing.T) {
	h := NewHub(500)
	s := h.Subscribe(1)
	if err := s.Set(1, []string{"Speed"}); err != nil { t.Fatal(err) }
	h.Publish(ValueEvent{DeviceID: 1, TagName: "Speed"})
	done := make(chan struct{})
	go func() { h.Publish(ValueEvent{DeviceID: 1, TagName: "Speed"}); close(done) }()
	select { case <-done: case <-time.After(250*time.Millisecond): t.Fatal("publish blocked") }
	select { case <-s.Done(): case <-time.After(250*time.Millisecond): t.Fatal("slow subscription not closed") }
}
