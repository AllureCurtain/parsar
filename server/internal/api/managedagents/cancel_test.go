package managedagents_test

import (
	"context"
	"errors"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/connector"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

type abortConnector struct {
	connector.AgentConnector
	err   error
	calls []connector.AbortInput
}

func (*abortConnector) Type() string { return "agent_daemon" }
func (*abortConnector) Capabilities() connector.Capabilities {
	return connector.Capabilities{Sync: true, Cancellation: true}
}
func (c *abortConnector) Abort(_ context.Context, in connector.AbortInput) error {
	c.calls = append(c.calls, in)
	return c.err
}

func TestManagedCancellationQueuedRetryAndRunningDelivery(t *testing.T) {
	f := newFixture(t)
	queued := f.run(t, "queued", "")
	path := "/api/v1/managed/runs/" + queued.ID + "/cancel"
	for range 2 {
		run := decode[store.ManagedAgentRun](t, f.request(t, "POST", path, f.credential.Token, "", "", 200))
		if run.Status != "cancelled" || run.StartedAt != nil {
			t.Fatalf("queued cancellation: %+v", run)
		}
	}
	running := f.run(t, "running", "")
	if _, err := f.store.MarkAgentRunRunning(t.Context(), running.ID, running.SessionID); err != nil {
		t.Fatal(err)
	}
	aborter := &abortConnector{err: errors.New("runtime disconnected")}
	f.connectors.MustRegister(aborter)
	path = "/api/v1/managed/runs/" + running.ID + "/cancel"
	f.request(t, "POST", path, f.credential.Token, "", "", 503)
	run, err := f.store.GetManagedAgentRun(t.Context(), f.credential.Client.ID, running.ID)
	if err != nil || run.Status != "cancelled" {
		t.Fatalf("cancel not durable: %+v %v", run, err)
	}
	aborter.err = nil
	f.request(t, "POST", path, f.credential.Token, "", "", 200)
	if len(aborter.calls) != 2 || aborter.calls[1].RunID != running.ID {
		t.Fatalf("delivery retry: %+v", aborter.calls)
	}
	var audit, events int
	if err := f.db.QueryRow(t.Context(), "select count(*) from audit_records where event_type='managed_agent.run.cancelled' and target_id=$1", running.ID).Scan(&audit); err != nil {
		t.Fatal(err)
	}
	if err := f.db.QueryRow(t.Context(), "select count(*) from agent_run_events where event_kind='run.cancelled' and agent_run_id=$1", running.ID).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if audit != 1 || events != 1 {
		t.Fatalf("duplicate cancellation records: audit=%d events=%d", audit, events)
	}
}
