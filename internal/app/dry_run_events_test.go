package app

import (
	"errors"
	"fmt"
	"testing"

	"paperless/internal/progress"
)

func TestRunSnapshotsCoalesceWithoutLosingCompletion(t *testing.T) {
	runs := newRunRegistry()
	updates, stop := runs.changes.subscribe()
	defer stop()
	<-updates
	for i := 0; i < 10; i++ {
		state := runs.create(fmt.Sprintf("run-%d", i), fmt.Sprintf("client-%d", i))
		// More than the legacy stream buffer, all before a subscriber drains.
		for n := 0; n < 200; n++ {
			state.publish(progress.Event{Percent: n / 2})
		}
		state.finish(nil)
		state.finish(errors.New("ignored repeated finish"))
	}
	if len(updates) != 1 {
		t.Fatalf("pending notifications = %d", len(updates))
	}
	<-updates
	cursor := make(map[string]int)
	snapshots := runs.snapshotsSince(cursor)
	if len(snapshots) != 10 {
		t.Fatalf("snapshots = %d", len(snapshots))
	}
	for _, snapshot := range snapshots {
		if len(snapshot.Events) != 201 || !snapshot.Events[200].Done || snapshot.Events[200].Level != "info" {
			t.Fatalf("lost completion for %s", snapshot.RunID)
		}
		if snapshot.ClientID == "" {
			t.Fatal("missing client correlation")
		}
	}
	if len(runs.snapshotsSince(cursor)) != 0 {
		t.Fatal("resent unchanged runs")
	}
	if len(runs.snapshotsSince(make(map[string]int))) != 10 {
		t.Fatal("reconnect did not replay completed runs")
	}
}

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
