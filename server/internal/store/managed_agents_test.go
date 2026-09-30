package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func managedFixture(t *testing.T) (*pgxpool.Pool, *Store, DevFixtureIDs, ManagedAgentCredential) {
	t.Helper()
	db := openTestDB(t)
	s, ids := New(db), DefaultDevFixtureIDs()
	if _, err := s.InsertDevFixture(t.Context(), ids); err != nil {
		t.Fatal(err)
	}
	credential, err := s.CreateManagedAgentClient(t.Context(), ManagedAgentClientInput{
		WorkspaceID: ids.WorkspaceID, AgentID: ids.ProductAgentID, ActorID: ids.UserID, Name: "External product",
	})
	if err != nil {
		t.Fatal(err)
	}
	return db, s, ids, credential
}

func TestManagedCredentialLifecycleAndIsolation(t *testing.T) {
	db, s, ids, credential := managedFixture(t)
	ctx := t.Context()
	if len(credential.Token) != 56 || !strings.HasPrefix(credential.Token, "pma_") {
		t.Fatal("unexpected credential format")
	}
	var storedHash string
	if err := db.QueryRow(ctx, "select token_hash from managed_agent_clients where id=$1", credential.Client.ID).Scan(&storedHash); err != nil {
		t.Fatal(err)
	}
	if storedHash == credential.Token || storedHash != managedHash(credential.Token) {
		t.Fatal("plaintext credential persisted")
	}
	client, err := s.AuthenticateManagedAgentClient(ctx, credential.Token)
	if err != nil || client.ID != credential.Client.ID {
		t.Fatalf("authenticate: %v", err)
	}
	admin := ManagedAgentClientInput{WorkspaceID: ids.WorkspaceID, AgentID: ids.ProductAgentID, ActorID: ids.UserID, ClientID: client.ID}
	listed, err := s.ListManagedAgentClients(ctx, admin)
	if err != nil || len(listed) != 1 {
		t.Fatalf("list: %v, %d", err, len(listed))
	}
	encoded, err := json.Marshal(listed)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), credential.Token) || strings.Contains(string(encoded), storedHash) {
		t.Fatal("credential secret exposed by client DTO")
	}
	if listed[0].LastUsedAt == nil {
		t.Fatal("last-used timestamp was not recorded")
	}
	if _, err := s.AuthenticateManagedAgentClient(ctx, "rtc_"+credential.Token[4:]); !errors.Is(err, ErrManagedUnauthorized) {
		t.Fatalf("runner credential accepted: %v", err)
	}
	foreign := admin
	foreign.AgentID = ids.BackendAgentID
	if _, err := s.RotateManagedAgentClient(ctx, foreign); !errors.Is(err, ErrManagedNotFound) {
		t.Fatalf("cross-agent rotation: %v", err)
	}
	rotated, err := s.RotateManagedAgentClient(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	if rotated.Token == credential.Token || rotated.Client.ID != client.ID {
		t.Fatal("rotation changed identity or retained token")
	}
	if _, err := s.AuthenticateManagedAgentClient(ctx, credential.Token); !errors.Is(err, ErrManagedUnauthorized) {
		t.Fatalf("old credential survived rotation: %v", err)
	}
	if _, err := s.AuthenticateManagedAgentClient(ctx, rotated.Token); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RevokeManagedAgentClient(ctx, admin); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RevokeManagedAgentClient(ctx, admin); err != nil {
		t.Fatalf("revoke retry: %v", err)
	}
	if _, err := s.AuthenticateManagedAgentClient(ctx, rotated.Token); !errors.Is(err, ErrManagedUnauthorized) {
		t.Fatalf("revoked credential accepted: %v", err)
	}
	if _, err := s.RotateManagedAgentClient(ctx, admin); !errors.Is(err, ErrManagedNotFound) {
		t.Fatalf("revoked client reactivated: %v", err)
	}
}

func TestManagedCredentialAuthorityIsRechecked(t *testing.T) {
	for _, mutation := range []struct{ name, query string }{
		{"expired", "update managed_agent_clients set expires_at=now()-interval '1 second'"},
		{"issuer demoted", "update workspace_members set role='viewer'"},
		{"issuer disabled", "update users set status='disabled'"},
		{"workspace removed", "update workspaces set deleted_at=now()"},
		{"agent removed", "update agents set deleted_at=now()"},
	} {
		t.Run(mutation.name, func(t *testing.T) {
			db, s, _, credential := managedFixture(t)
			client, err := s.AuthenticateManagedAgentClient(t.Context(), credential.Token)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(t.Context(), mutation.query); err != nil {
				t.Fatal(err)
			}
			if _, err := s.AuthenticateManagedAgentClient(t.Context(), credential.Token); !errors.Is(err, ErrManagedUnauthorized) {
				t.Fatalf("credential still accepted: %v", err)
			}
			if _, _, err := s.CreateManagedAgentRun(t.Context(), client, "stale-auth", ManagedAgentRunInput{Input: "hello"}); !errors.Is(err, ErrManagedUnauthorized) {
				t.Fatalf("stale authentication reused at write: %v", err)
			}
		})
	}
}

func TestManagedClientCreationRequiresAdministrator(t *testing.T) {
	db, s, ids, _ := managedFixture(t)
	if _, err := db.Exec(t.Context(), "update workspace_members set role='member'"); err != nil {
		t.Fatal(err)
	}
	in := ManagedAgentClientInput{WorkspaceID: ids.WorkspaceID, AgentID: ids.ProductAgentID, ActorID: ids.UserID, Name: "another"}
	if _, err := s.CreateManagedAgentClient(t.Context(), in); !errors.Is(err, ErrManagedForbidden) {
		t.Fatalf("member created API client: %v", err)
	}
	if _, err := s.ListManagedAgentClients(t.Context(), in); !errors.Is(err, ErrManagedForbidden) {
		t.Fatalf("member read API clients: %v", err)
	}
}

func TestManagedRunConcurrentIdempotencyAndRestart(t *testing.T) {
	db, s, _, credential := managedFixture(t)
	const callers = 16
	type result struct {
		run    ManagedAgentRun
		replay bool
		err    error
	}
	results := make(chan result, callers)
	start := make(chan struct{})
	var group sync.WaitGroup
	for range callers {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			run, replay, err := s.CreateManagedAgentRun(t.Context(), credential.Client, "shared-request", ManagedAgentRunInput{Input: "hello"})
			results <- result{run, replay, err}
		}()
	}
	close(start)
	group.Wait()
	close(results)
	created, runID := 0, ""
	for item := range results {
		if item.err != nil {
			t.Fatal(item.err)
		}
		if !item.replay {
			created++
		}
		if runID != "" && item.run.ID != runID {
			t.Fatal("concurrent retry created another run")
		}
		runID = item.run.ID
	}
	if created != 1 {
		t.Fatalf("created %d runs", created)
	}
	for _, table := range []string{"managed_agent_requests", "managed_agent_sessions", "agent_runs", "messages"} {
		var count int
		if err := db.QueryRow(t.Context(), "select count(*) from "+table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("%s contains %d rows, want 1", table, count)
		}
	}
	restarted := New(db)
	run, replay, err := restarted.CreateManagedAgentRun(t.Context(), credential.Client, "shared-request", ManagedAgentRunInput{Input: "hello"})
	if err != nil || !replay || run.ID != runID {
		t.Fatalf("restart replay: %+v %v %v", run, replay, err)
	}
	if _, _, err := restarted.CreateManagedAgentRun(t.Context(), credential.Client, "shared-request", ManagedAgentRunInput{Input: "different"}); !errors.Is(err, ErrManagedConflict) {
		t.Fatalf("changed retry: %v", err)
	}
}

func TestManagedSessionsAreClientScoped(t *testing.T) {
	_, s, ids, credential := managedFixture(t)
	ctx := t.Context()
	first, _, err := s.CreateManagedAgentRun(ctx, credential.Client, "first", ManagedAgentRunInput{Input: "first question"})
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := s.CreateManagedAgentRun(ctx, credential.Client, "second", ManagedAgentRunInput{Input: "follow-up", SessionID: first.SessionID})
	if err != nil || first.SessionID != second.SessionID {
		t.Fatalf("session continuation: %v", err)
	}
	other, err := s.CreateManagedAgentClient(ctx, ManagedAgentClientInput{WorkspaceID: ids.WorkspaceID, AgentID: ids.ProductAgentID, ActorID: ids.UserID, Name: "other product"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetManagedAgentRun(ctx, other.Client.ID, first.ID); !errors.Is(err, ErrManagedNotFound) {
		t.Fatalf("foreign run exposed: %v", err)
	}
	if _, err := s.GetManagedAgentSession(ctx, other.Client.ID, first.SessionID); !errors.Is(err, ErrManagedNotFound) {
		t.Fatalf("foreign session exposed: %v", err)
	}
	if _, _, err := s.CreateManagedAgentRun(ctx, other.Client, "first", ManagedAgentRunInput{Input: "injection", SessionID: first.SessionID}); !errors.Is(err, ErrManagedNotFound) {
		t.Fatalf("foreign session accepted: %v", err)
	}
	if _, _, err := s.CreateManagedAgentRun(ctx, credential.Client, "existing-web", ManagedAgentRunInput{Input: "injection", SessionID: ids.ConversationID}); !errors.Is(err, ErrManagedNotFound) {
		t.Fatalf("web conversation accepted: %v", err)
	}
	messages, err := s.ListManagedAgentMessages(ctx, credential.Client.ID, first.SessionID, "", 1)
	if err != nil || len(messages) != 1 || messages[0].Content != "first question" {
		t.Fatalf("first message page: %+v %v", messages, err)
	}
	next, err := s.ListManagedAgentMessages(ctx, credential.Client.ID, first.SessionID, messages[0].ID, 1)
	if err != nil || len(next) != 1 || next[0].Content != "follow-up" {
		t.Fatalf("second message page: %+v %v", next, err)
	}
	sessions, err := s.ListManagedAgentSessions(ctx, other.Client.ID, "", 20)
	if err != nil || len(sessions) != 0 {
		t.Fatalf("foreign sessions listed: %+v %v", sessions, err)
	}
	rotated, err := s.RotateManagedAgentClient(ctx, ManagedAgentClientInput{WorkspaceID: ids.WorkspaceID, AgentID: ids.ProductAgentID, ActorID: ids.UserID, ClientID: credential.Client.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetManagedAgentSession(ctx, rotated.Client.ID, first.SessionID); err != nil {
		t.Fatalf("rotation lost session: %v", err)
	}
}

func TestManagedRunValidationAndAtomicRollback(t *testing.T) {
	db, s, ids, credential := managedFixture(t)
	ctx := t.Context()
	for _, input := range []ManagedAgentRunInput{{}, {Input: " \n"}, {Input: strings.Repeat("x", 32001)}, {Input: "x", SessionID: "invalid"}, {Input: string([]byte{0xff})}} {
		if _, _, err := s.CreateManagedAgentRun(ctx, credential.Client, "bad", input); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("invalid input accepted: %v", err)
		}
	}
	for _, key := range []string{"", "with space", "with\nnewline", strings.Repeat("x", 256)} {
		if _, _, err := s.CreateManagedAgentRun(ctx, credential.Client, key, ManagedAgentRunInput{Input: "test"}); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("invalid key accepted: %v", err)
		}
	}
	if _, _, err := s.CreateManagedAgentRun(ctx, credential.Client, "unicode", ManagedAgentRunInput{Input: strings.Repeat("\u4e2d", 32000)}); err != nil {
		t.Fatalf("valid Unicode message: %v", err)
	}
	if _, err := db.Exec(ctx, "update agents set status='disabled' where id=$1", ids.ProductAgentID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.CreateManagedAgentRun(ctx, credential.Client, "disabled", ManagedAgentRunInput{Input: "test"}); !errors.Is(err, ErrManagedUnavailable) {
		t.Fatalf("disabled agent invoked: %v", err)
	}
	if _, err := db.Exec(ctx, "update agents set status='active' where id=$1", ids.ProductAgentID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, "alter table managed_agent_requests add constraint reject_test_request check (idempotency_key <> 'rollback')"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(context.Background(), "alter table managed_agent_requests drop constraint reject_test_request")
	})
	if _, _, err := s.CreateManagedAgentRun(ctx, credential.Client, "rollback", ManagedAgentRunInput{Input: "must roll back"}); err == nil {
		t.Fatal("injected persistence error did not fail")
	}
	var count int
	if err := db.QueryRow(ctx, "select count(*) from messages where content='must roll back'").Scan(&count); err != nil || count != 0 {
		t.Fatalf("partial message persisted: %d %v", count, err)
	}
	if err := db.QueryRow(ctx, "select count(*) from managed_agent_sessions").Scan(&count); err != nil || count != 1 {
		t.Fatalf("partial session persisted: %d %v", count, err)
	}
}

type managedDispatchRecorder struct{ runs []StreamingDispatchInput }

func (d *managedDispatchRecorder) Start(_ context.Context, in StreamingDispatchInput) {
	d.runs = append(d.runs, in)
}

func TestManagedQueuedRunRecoveryUsesOldestUnblockedRun(t *testing.T) {
	db, s, ids, credential := managedFixture(t)
	ctx := t.Context()
	if _, err := db.Exec(ctx, "update agents set connector_type='agent_daemon' where id=$1", ids.ProductAgentID); err != nil {
		t.Fatal(err)
	}
	first, _, err := s.CreateManagedAgentRun(ctx, credential.Client, "first", ManagedAgentRunInput{Input: "first"})
	if err != nil {
		t.Fatal(err)
	}
	for n := range 3 {
		if _, _, err := s.CreateManagedAgentRun(ctx, credential.Client, fmt.Sprint(n), ManagedAgentRunInput{Input: "next", SessionID: first.SessionID}); err != nil {
			t.Fatal(err)
		}
	}
	restarted, recorder := New(db), &managedDispatchRecorder{}
	restarted.SetStreamingDispatcher(recorder)
	if err := restarted.RecoverQueuedManagedAgentRuns(ctx); err != nil {
		t.Fatal(err)
	}
	if len(recorder.runs) != 1 || recorder.runs[0].RunID != first.ID {
		t.Fatalf("recovered %+v", recorder.runs)
	}
	if _, err := restarted.MarkAgentRunRunning(ctx, first.ID, first.SessionID); err != nil {
		t.Fatal(err)
	}
	recorder.runs = nil
	if err := restarted.RecoverQueuedManagedAgentRuns(ctx); err != nil {
		t.Fatal(err)
	}
	if len(recorder.runs) != 0 {
		t.Fatal("recovery bypassed the active run")
	}
	if _, err := db.Exec(ctx, "update agent_runs set status='completed', finished_at=$2 where id=$1", first.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := restarted.RecoverQueuedManagedAgentRuns(ctx); err != nil {
		t.Fatal(err)
	}
	if len(recorder.runs) != 1 || recorder.runs[0].RunID == first.ID {
		t.Fatalf("next queued run was not recovered: %+v", recorder.runs)
	}
}
