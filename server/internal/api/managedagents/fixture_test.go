package managedagents_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/api/managedagents"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/auth"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/connector"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/db/sqlc"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/dev"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/interaction"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type fixture struct {
	db         *pgxpool.Pool
	store      *store.Store
	ids        store.DevFixtureIDs
	credential store.ManagedAgentCredential
	router     http.Handler
	connectors *connector.Registry
	delivery   *runtimeDelivery
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	dsn := os.Getenv("PARSAR_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("PARSAR_TEST_DATABASE_URL is not set")
	}
	db, err := pgxpool.New(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	lock, err := db.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lock.Exec(t.Context(), "select pg_advisory_lock(8675309)"); err != nil {
		lock.Release()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = lock.Exec(context.Background(), "select pg_advisory_unlock(8675309)")
		lock.Release()
	})
	if _, err := db.Exec(t.Context(), "truncate users, workspaces, secrets restart identity cascade"); err != nil {
		t.Fatal(err)
	}
	f := &fixture{db: db, store: store.New(db), ids: store.DefaultDevFixtureIDs(), connectors: connector.NewRegistry(), delivery: &runtimeDelivery{}}
	if _, err := f.store.InsertDevFixture(t.Context(), f.ids); err != nil {
		t.Fatal(err)
	}
	f.credential = f.newClient(t, false)
	sessions := auth.NewMiddleware(auth.NewPostgresSessionStore(sqlc.New(db))).WithDevAuth(true)
	r := chi.NewRouter()
	deps := managedagents.Deps{Store: f.store, Connectors: f.connectors, PollInterval: 5 * time.Millisecond,
		Interactions: interaction.NewService(f.store, f.delivery, nil)}
	dev.RegisterRoutesWithStore(r, f.store, dev.WithAuthMiddleware(sessions))
	r.Group(func(r chi.Router) {
		r.Use(sessions.Require)
		managedagents.RegisterAdminRoutes(r, deps)
	})
	managedagents.RegisterPublicRoutes(r, deps)
	r.With(auth.RunnerCredential(f.store, auth.RunnerCredentialOptions{})).Get("/test-runtime", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	f.router = r
	return f
}

func (f *fixture) newClient(t *testing.T, interactions bool) store.ManagedAgentCredential {
	t.Helper()
	c, err := f.store.CreateManagedAgentClient(t.Context(), store.ManagedAgentClientInput{
		WorkspaceID: f.ids.WorkspaceID, AgentID: f.ids.ProductAgentID, ActorID: f.ids.UserID,
		Name: "Test product", AllowInteractions: interactions,
	})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func (f *fixture) request(t *testing.T, method, path, token, key, body string, status int) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	if key != "" {
		r.Header.Set("Idempotency-Key", key)
	}
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, r)
	if w.Code != status {
		t.Fatalf("%s %s: status %d, want %d: %s", method, path, w.Code, status, w.Body.String())
	}
	return w
}

func (f *fixture) run(t *testing.T, key, session string) store.ManagedAgentRun {
	t.Helper()
	body, err := json.Marshal(store.ManagedAgentRunInput{Input: "hello", SessionID: session})
	if err != nil {
		t.Fatal(err)
	}
	w := f.request(t, "POST", "/api/v1/managed/runs", f.credential.Token, key, string(body), 202)
	return decode[store.ManagedAgentRun](t, w)
}

func decode[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	var result T
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

type runtimeDelivery struct {
	err         error
	permissions []connector.PermissionDecision
	answers     []connector.PromptForUserChoiceDecision
}

func (d *runtimeDelivery) SubmitPermission(_ context.Context, _ string, decision connector.PermissionDecision) error {
	if d.err != nil {
		return d.err
	}
	d.permissions = append(d.permissions, decision)
	return nil
}
func (d *runtimeDelivery) SubmitPromptForUserChoice(_ context.Context, _ string, decision connector.PromptForUserChoiceDecision) error {
	if d.err != nil {
		return d.err
	}
	d.answers = append(d.answers, decision)
	return nil
}
