package app

import (
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"time"
)

// Dashboard events are invalidations, not a history. One pending notification
// per subscriber is enough, and a slow browser never holds up processing.
type dashboardEvents struct {
	mu          sync.Mutex
	subscribers map[chan struct{}]struct{}
	closed      bool
}

func (e *dashboardEvents) subscribe() (<-chan struct{}, func()) {
	e.mu.Lock()
	defer e.mu.Unlock()
	ch := make(chan struct{}, 1)
	if e.closed {
		close(ch)
		return ch, func() {}
	}
	if e.subscribers == nil {
		e.subscribers = make(map[chan struct{}]struct{})
	}
	e.subscribers[ch] = struct{}{}
	// Every connection, including reconnects, must fetch a fresh snapshot.
	ch <- struct{}{}
	return ch, func() {
		e.mu.Lock()
		defer e.mu.Unlock()
		delete(e.subscribers, ch)
	}
}

func (e *dashboardEvents) publish() {
	e.mu.Lock()
	defer e.mu.Unlock()
	for ch := range e.subscribers {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

func (e *dashboardEvents) close() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.closed = true
	for ch := range e.subscribers {
		close(ch)
		delete(e.subscribers, ch)
	}
}

func (p *Processor) handleDashboardEvents(w http.ResponseWriter, r *http.Request) {
	if _, ok := w.(http.Flusher); !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	updates, unsubscribe := p.dashboardEvents.subscribe()
	defer unsubscribe()
	var uploadUpdates <-chan struct{}
	if p.runs != nil {
		var stopUploads func()
		uploadUpdates, stopUploads = p.runs.changes.subscribe()
		defer stopUploads()
	}
	sentUploads := make(map[string]int)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("X-Accel-Buffering", "no")
	controller := http.NewResponseController(w)
	write := func(message string) error {
		// Bound writes so disconnected/slow clients cannot delay shutdown.
		_ = controller.SetWriteDeadline(time.Now().Add(5 * time.Second))
		if _, err := io.WriteString(w, message); err != nil {
			return err
		}
		return controller.Flush()
	}
	if err := write("retry: 1000\n\n"); err != nil {
		return
	}
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case _, ok := <-updates:
			if !ok {
				return
			}
			if err := write("event: dashboard\ndata: {}\n\n"); err != nil {
				return
			}
		case <-heartbeat.C:
			if err := write(": keep-alive\n\n"); err != nil {
				return
			}
		case _, ok := <-uploadUpdates:
			if !ok {
				return
			}
			snapshots := p.runs.snapshotsSince(sentUploads)
			if len(snapshots) == 0 {
				continue
			}
			payload, err := json.Marshal(snapshots)
			if err != nil {
				return
			}
			if err := write("event: uploads\ndata: " + string(payload) + "\n\n"); err != nil {
				return
			}
		}
	}
}
