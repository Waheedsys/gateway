package events

import "log"

// Bus is a buffered, in-process event bus.
//
// Internals:
//   - "ch" is a Go channel — think of it as a thread-safe queue.
//   - When a publisher calls Publish(), it drops the event into the queue.
//   - A background goroutine (started by Subscribe) reads from the queue and
//     calls the handler function for each event.
//   - Because the channel is buffered (size=bufferSize), Publish() never blocks
//     the HTTP handler unless the queue is completely full.
type Bus struct {
	ch chan Event
}

// NewBus creates a Bus with a buffered channel.
//
// bufferSize controls how many events can be queued before backpressure kicks in.
// 100 is a safe default — at typical inference rates you'd need a burst of 100
// simultaneous completions to fill it.
func NewBus(bufferSize int) *Bus {
	return &Bus{
		ch: make(chan Event, bufferSize),
	}
}

// Publish sends an event to all subscribers.
//
// This uses a "select with default" pattern:
//
//	select {
//	case b.ch <- e:   // send succeeds — channel has room
//	default:          // channel is full — drop the event, log a warning
//	}
//
// The "default" branch is what makes this non-blocking.
// Without it, a full channel would freeze the HTTP handler goroutine.
func (b *Bus) Publish(e Event) {
	select {
	case b.ch <- e:
	default:
		log.Printf("[events] bus buffer full, dropping event: %s", e.Type)
	}
}

// Subscribe registers a handler and starts a goroutine to process events.
//
// The goroutine runs "for e := range b.ch" — this is Go's idiomatic way to
// read from a channel until it is closed. Each iteration blocks until the
// next event arrives, so the goroutine uses zero CPU while idle.
//
// Call this once at startup, before any traffic arrives.
func (b *Bus) Subscribe(handler func(Event)) {
	go func() {
		for e := range b.ch {
			// Any panic inside handler must not crash the entire process
			func() {
				defer func() {
					if r := recover(); r != nil {
						log.Printf("[events] subscriber panic recovered: %v", r)
					}
				}()
				handler(e)
			}()
		}
	}()
}

// Close shuts down the bus. The subscriber goroutine will exit after
// processing any remaining events.
func (b *Bus) Close() {
	close(b.ch)
}
