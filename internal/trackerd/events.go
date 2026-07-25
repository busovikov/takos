package trackerd

import "sync"

// eventHub fans a workspace's "something changed" signal out to the SSE
// subscribers of /api/ws/{name}/events. The signal carries no payload: the
// web UI is read-first, so a ping just means "re-fetch the board/card". One
// hub lives per instance for the daemon's lifetime, so a subscriber can
// connect before the instance is running and start receiving once it is.
type eventHub struct {
	mu   sync.Mutex
	subs map[chan struct{}]struct{}
}

func newEventHub() *eventHub {
	return &eventHub{subs: map[chan struct{}]struct{}{}}
}

// subscribe registers a listener. The channel is buffered by one so a
// broadcast never blocks and repeated pings between reads coalesce.
func (h *eventHub) subscribe() chan struct{} {
	ch := make(chan struct{}, 1)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

func (h *eventHub) unsubscribe(ch chan struct{}) {
	h.mu.Lock()
	delete(h.subs, ch)
	h.mu.Unlock()
}

// broadcast wakes every subscriber; a subscriber with a ping already
// pending is left as-is (the pending ping subsumes this one).
func (h *eventHub) broadcast() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}
