package app

import (
	"sync"
	"time"

	"paperless/internal/progress"
)

type runRegistry struct {
	mu   sync.Mutex
	runs map[string]*runState
}

type runState struct {
	id          string
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

func (r *runRegistry) create(id string) *runState {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pruneLocked()
	state := &runState{
		id:          id,
		createdAt:   time.Now().UTC(),
		subscribers: map[chan progress.Event]struct{}{},
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
	if err != nil && len(s.events) > 0 {
		event.Percent = s.events[len(s.events)-1].Percent
	}
	if err != nil {
		s.err = err.Error()
	}
	s.done = true
	s.events = append(s.events, event)
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
