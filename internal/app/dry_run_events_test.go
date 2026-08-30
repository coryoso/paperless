package app

import (
	"errors"
	"testing"

	"paperless/internal/progress"
)

func TestRunStatePublishesSnapshotAndDone(t *testing.T) {
	state := newRunRegistry().create("abc123")
	state.publish(progress.Event{Level: "info", Phase: "ocr", Step: "render", Message: "Rendering.", Percent: 14})

	snapshot, events, done := state.subscribe()
	if done {
		t.Fatal("state should not be done")
	}
	if len(snapshot) != 1 || snapshot[0].Step != "render" {
		t.Fatalf("snapshot = %#v", snapshot)
	}

	state.finish(errors.New("ocr failed"))
	event, ok := <-events
	if !ok {
		t.Fatal("expected done event before channel close")
	}
	if !event.Done || event.Level != "error" {
		t.Fatalf("event = %#v", event)
	}
	if event.Percent != 14 {
		t.Fatalf("error percent = %d, want 14", event.Percent)
	}
	if _, ok := <-events; ok {
		t.Fatal("expected channel to close after done event")
	}
}
