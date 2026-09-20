package app

import (
	"sync"
	"time"

	"paperless/internal/progress"
)

type runRegistry struct {
	mu      sync.Mutex
	runs    map[string]*runState
	changes dashboardEvents
}

type runState struct {
	id          string
	clientID    string
	changed     func()
	createdAt   time.Time
	events      []progress.Event
	err         string
	done        bool
	subscribers map[chan progress.Event]struct{}
	mu          sync.Mutex
}

func newRunRegistry() *runRegistry {
	return &runRegistry{runs: map[string]*runState{}}
}

func (r *runRegistry) create(id string, clientID ...string) *runState {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pruneLocked()
	state := &runState{
		id:          id,
		changed:     r.changes.publish,
		createdAt:   time.Now().UTC(),
		subscribers: map[chan progress.Event]struct{}{},
	}
	if len(clientID) > 0 {
		state.clientID = clientID[0]
	}
	r.runs[id] = state
	return state
}

func (r *runRegistry) get(id string) (*runState, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	state, ok := r.runs[id]
	return state, ok
}

func (r *runRegistry) pruneLocked() {
	const maxRuns = 32
	if len(r.runs) < maxRuns {
		return
	}
	cutoff := time.Now().UTC().Add(-2 * time.Hour)
	for id, state := range r.runs {
		if state.createdAt.Before(cutoff) {
			delete(r.runs, id)
		}
	}
}

func (s *runState) reporter() progress.Reporter {
	return progress.Reporter(func(event progress.Event) {
		s.publish(event)
	})
}

func (s *runState) publish(event progress.Event) {
	if event.At.IsZero() {
		event.At = time.Now().UTC()
	}
	if event.Level == "" {
		event.Level = "info"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.done {
		return
	}
	s.events = append(s.events, event)
	s.changed()
	for ch := range s.subscribers {
		select {
		case ch <- event:
		default:
		}
	}
}

func (s *runState) finish(err error) {
	event := progress.Event{
		At:      time.Now().UTC(),
		Level:   "info",
		Phase:   "complete",
		Step:    "done",
		Message: "Analysis complete.",
		Percent: 100,
		Done:    true,
	}
	if err != nil {
		event.Level = "error"
		event.Phase = "failed"
		event.Step = "error"
		event.Message = err.Error()
	}

	s.mu.Lock()
	if s.done {
		s.mu.Unlock()
		return
	}
	if err != nil && len(s.events) > 0 {
		event.Percent = s.events[len(s.events)-1].Percent
	}
	if err != nil {
		s.err = err.Error()
	}
	s.done = true
	s.events = append(s.events, event)
	s.changed()
	for ch := range s.subscribers {
		select {
		case ch <- event:
		default:
		}
		close(ch)
		delete(s.subscribers, ch)
	}
	s.mu.Unlock()
}

type uploadSnapshot struct {
	RunID    string           `json:"run_id"`
	ClientID string           `json:"client_upload_id"`
	Events   []progress.Event `json:"events"`
}

// Each connection tracks the last event count sent for each run. A reconnect
// starts with an empty cursor and receives full histories, including completion.
func (r *runRegistry) snapshotsSince(sent map[string]int) []uploadSnapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	var snapshots []uploadSnapshot
	for id, state := range r.runs {
		state.mu.Lock()
		if len(state.events) > sent[id] {
			snapshots = append(snapshots, uploadSnapshot{RunID: id, ClientID: state.clientID, Events: append([]progress.Event(nil), state.events...)})
			sent[id] = len(state.events)
		}
		state.mu.Unlock()
	}
	for id := range sent {
		if _, ok := r.runs[id]; !ok {
			delete(sent, id)
		}
	}
	return snapshots
}

func (s *runState) subscribe() ([]progress.Event, chan progress.Event, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	snapshot := append([]progress.Event(nil), s.events...)
	if s.done {
		return snapshot, nil, true
	}
	ch := make(chan progress.Event, 128)
	s.subscribers[ch] = struct{}{}
	return snapshot, ch, false
}

func (s *runState) unsubscribe(ch chan progress.Event) {
	if ch == nil {
		return
	}
	s.mu.Lock()
	if _, exists := s.subscribers[ch]; exists {
		delete(s.subscribers, ch)
		close(ch)
	}
	s.mu.Unlock()
}
