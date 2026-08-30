package progress

import "time"

type Event struct {
	At      time.Time `json:"at"`
	Level   string    `json:"level"`
	Phase   string    `json:"phase"`
	Step    string    `json:"step"`
	Message string    `json:"message"`
	Current int       `json:"current,omitempty"`
	Total   int       `json:"total,omitempty"`
	Percent int       `json:"percent"`
	Done    bool      `json:"done,omitempty"`
}

type Reporter func(Event)

func (r Reporter) Info(phase, step, message string, current, total, percent int) {
	r.Report(Event{
		Level:   "info",
		Phase:   phase,
		Step:    step,
		Message: message,
		Current: current,
		Total:   total,
		Percent: percent,
	})
}

func (r Reporter) Warn(phase, step, message string, current, total, percent int) {
	r.Report(Event{
		Level:   "warn",
		Phase:   phase,
		Step:    step,
		Message: message,
		Current: current,
		Total:   total,
		Percent: percent,
	})
}

func (r Reporter) Error(phase, step, message string, percent int) {
	r.Report(Event{
		Level:   "error",
		Phase:   phase,
		Step:    step,
		Message: message,
		Percent: percent,
		Done:    true,
	})
}

func (r Reporter) Done(message string) {
	r.Report(Event{
		Level:   "info",
		Phase:   "complete",
		Step:    "done",
		Message: message,
		Percent: 100,
		Done:    true,
	})
}

func (r Reporter) Report(event Event) {
	if r == nil {
		return
	}
	if event.At.IsZero() {
		event.At = time.Now().UTC()
	}
	if event.Level == "" {
		event.Level = "info"
	}
	event.Percent = clamp(event.Percent, 0, 100)
	r(event)
}

func PercentRange(start, end, current, total int) int {
	if total <= 0 {
		return start
	}
	if current < 0 {
		current = 0
	}
	if current > total {
		current = total
	}
	return start + (end-start)*current/total
}

func clamp(value, min, max int) int {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}
