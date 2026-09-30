package store

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestStreamingClaimSerializesConcurrentRunsInSameLane(t *testing.T) {
	db, s, _, credential := managedFixture(t)
	const count = 16
	runs := make([]ManagedAgentRun, 0, count)
	sessionID := ""
	for i := range count {
		run, _, err := s.CreateManagedAgentRun(t.Context(), credential.Client, fmt.Sprint(i), ManagedAgentRunInput{Input: "next", SessionID: sessionID})
		if err != nil {
			t.Fatal(err)
		}
		sessionID = run.SessionID
		runs = append(runs, run)
	}
	start, results := make(chan struct{}), make(chan error, count)
	for _, run := range runs {
		go func() {
			<-start
			_, err := s.MarkAgentRunRunning(t.Context(), run.ID, run.SessionID)
			results <- err
		}()
	}
	close(start)
	claimed := 0
	for range count {
		err := <-results
		if err == nil {
			claimed++
		} else if !errors.Is(err, ErrAgentRunBlockedByQueue) {
			t.Errorf("claim: %v", err)
		}
	}
	var running int
	if err := db.QueryRow(t.Context(), "select count(*) from agent_runs where status='running'").Scan(&running); err != nil {
		t.Fatal(err)
	}
	if claimed != 1 || running != 1 {
		t.Fatalf("claimed=%d running=%d, want exactly one", claimed, running)
	}
}

func TestManagedCancellationRollsBackIfEventCannotPersist(t *testing.T) {
	db, s, _, credential := managedFixture(t)
	run, _, err := s.CreateManagedAgentRun(t.Context(), credential.Client, "cancel", ManagedAgentRunInput{Input: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(t.Context(), "alter table agent_run_events add constraint test_reject_cancel check (event_kind != 'run.cancelled')"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(context.Background(), "alter table agent_run_events drop constraint test_reject_cancel")
	})
	if err := s.CancelManagedAgentRun(t.Context(), credential.Client, run.ID); err == nil {
		t.Fatal("expected event persistence failure")
	}
	persisted, err := s.GetManagedAgentRun(t.Context(), credential.Client.ID, run.ID)
	if err != nil || persisted.Status != "queued" {
		t.Fatalf("cancellation partially committed: %+v %v", persisted, err)
	}
}
