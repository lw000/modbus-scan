package kafkapub

import (
	"context"
	"sync"
	"sync/atomic"
)

type messageQueue struct {
	mu        sync.Mutex
	items     []Message
	head      int
	size      int
	accepting bool
	wake      chan struct{}
	dropped   atomic.Uint64
}

func newMessageQueue(capacity int) *messageQueue {
	if capacity < 1 {
		capacity = 1
	}
	return &messageQueue{items: make([]Message, capacity), accepting: true, wake: make(chan struct{}, 1)}
}

func (q *messageQueue) push(message Message) (uint64, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if !q.accepting {
		return q.dropped.Load(), false
	}
	didDrop := q.size == len(q.items)
	if didDrop {
		q.items[q.head] = message
		q.head = (q.head + 1) % len(q.items)
		q.dropped.Add(1)
	} else {
		index := (q.head + q.size) % len(q.items)
		q.items[index] = message
		q.size++
	}
	select {
	case q.wake <- struct{}{}:
	default:
	}
	return q.dropped.Load(), didDrop
}

func (q *messageQueue) next(ctx context.Context) (Message, error) {
	for {
		q.mu.Lock()
		if q.size > 0 {
			message := q.items[q.head]
			q.items[q.head] = Message{}
			q.head = (q.head + 1) % len(q.items)
			q.size--
			q.mu.Unlock()
			return message, nil
		}
		q.mu.Unlock()
		select {
		case <-ctx.Done():
			return Message{}, ctx.Err()
		case <-q.wake:
		}
	}
}

func (q *messageQueue) stopAccepting() {
	q.mu.Lock()
	q.accepting = false
	q.mu.Unlock()
}

func (q *messageQueue) discardAll() {
	q.mu.Lock()
	for i := range q.items {
		q.items[i] = Message{}
	}
	q.head, q.size = 0, 0
	q.mu.Unlock()
}

func (q *messageQueue) droppedCount() uint64 { return q.dropped.Load() }
