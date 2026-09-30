package managedagents_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/api/managedagents"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/auth"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/httprunner"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

func TestManagedAuthBoundariesAndAdminRoutes(t *testing.T) {
	f := newFixture(t)
	adminPath := "/api/v1/workspaces/" + f.ids.WorkspaceID + "/agents/" + f.ids.ProductAgentID + "/api-clients"
	for _, path := range []string{adminPath, "/api/v1/workspaces/" + f.ids.WorkspaceID + "/agents", "/test-runtime"} {
		f.request(t, "GET", path, f.credential.Token, "", "", 401)
	}
	for _, token := range []string{"", "session-token", "rtc_" + f.credential.Token[4:]} {
		f.request(t, "GET", "/api/v1/managed/client", token, "", "", 401)
	}
	f.request(t, "GET", "/api/v1/managed/client", f.credential.Token, "", "", 200)
	admin := func(method, path, body string, status int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set(auth.DevUserHeader, f.ids.UserID)
		w := httptest.NewRecorder()
		f.router.ServeHTTP(w, r)
		if w.Code != status {
			t.Fatalf("admin %s %s: %d, want %d: %s", method, path, w.Code, status, w.Body.String())
		}
		return w
	}
	admin("GET", "/api/v1/workspaces/"+f.ids.WorkspaceID+"/agents", "", 200)
	created := decode[store.ManagedAgentCredential](t, admin("POST", adminPath, `{"name":"Created via browser","allow_interactions":true,"expires_in_days":7}`, 201))
	if created.Token == "" || !created.Client.AllowInteractions {
		t.Fatal("missing issued credential")
	}
	listed := admin("GET", adminPath, "", 200)
	if strings.Contains(listed.Body.String(), created.Token) {
		t.Fatal("list disclosed credential")
	}
	rotated := decode[store.ManagedAgentCredential](t, admin("POST", adminPath+"/"+created.Client.ID+"/rotate", `{}`, 200))
	f.request(t, "GET", "/api/v1/managed/client", created.Token, "", "", 401)
	f.request(t, "GET", "/api/v1/managed/client", rotated.Token, "", "", 200)
	admin("DELETE", adminPath+"/"+created.Client.ID, "", 200)
	f.request(t, "GET", "/api/v1/managed/client", rotated.Token, "", "", 401)
	admin("GET", strings.Replace(adminPath, f.ids.ProductAgentID, "00000000-0000-0000-0000-0000feedf00d", 1), "", 404)
	if _, err := f.db.Exec(t.Context(), "update workspace_members set role='member'"); err != nil {
		t.Fatal(err)
	}
	admin("POST", adminPath, `{"name":"Forbidden"}`, 403)
	admin("GET", adminPath, "", 403)
}

func TestManagedRunIdempotencyValidationAndIsolation(t *testing.T) {
	f := newFixture(t)
	run := f.run(t, "first", "")
	replay := decode[store.ManagedAgentRun](t, f.request(t, "POST", "/api/v1/managed/runs", f.credential.Token, "first", `{"input":"hello"}`, 200))
	if replay.ID != run.ID {
		t.Fatal("HTTP retry created another run")
	}
	f.request(t, "POST", "/api/v1/managed/runs", f.credential.Token, "first", `{"input":"changed"}`, 409)
	for _, body := range []string{`{}`, `{"input":""}`, `{"input":"ok","admin":true}`, `{"input":"ok"} {}`, `null`, `{"input":"x","session_id":"bad"}`, `{"input":"` + strings.Repeat("x", 1<<20) + `"}`} {
		f.request(t, "POST", "/api/v1/managed/runs", f.credential.Token, "invalid", body, 400)
	}
	f.request(t, "POST", "/api/v1/managed/runs", f.credential.Token, "", `{"input":"hello"}`, 400)
	f.request(t, "GET", "/api/v1/managed/runs/invalid", f.credential.Token, "", "", 400)
	for _, suffix := range []string{"/sessions?limit=0", "/sessions?limit=201", "/sessions?limit=x", "/runs/" + run.ID + "/events?after=-1"} {
		f.request(t, "GET", "/api/v1/managed"+suffix, f.credential.Token, "", "", 400)
	}
	other := f.newClient(t, true)
	for _, suffix := range []string{"/runs/" + run.ID, "/runs/" + run.ID + "/events", "/runs/" + run.ID + "/stream", "/runs/" + run.ID + "/interactions", "/sessions/" + run.SessionID, "/sessions/" + run.SessionID + "/messages"} {
		f.request(t, "GET", "/api/v1/managed"+suffix, other.Token, "", "", 404)
	}
	f.request(t, "POST", "/api/v1/managed/runs/"+run.ID+"/cancel", other.Token, "", "", 404)
	f.request(t, "POST", "/api/v1/managed/runs", other.Token, "inject", `{"input":"hello","session_id":"`+run.SessionID+`"}`, 404)
	continued := f.run(t, "second", run.SessionID)
	if continued.SessionID != run.SessionID {
		t.Fatal("continuation lost session")
	}
	page := decode[managedagents.MessagesResponse](t, f.request(t, "GET", "/api/v1/managed/sessions/"+run.SessionID+"/messages?limit=1", f.credential.Token, "", "", 200))
	if len(page.Messages) != 1 || page.NextAfter == "" {
		t.Fatalf("missing next page: %+v", page)
	}
	next := decode[managedagents.MessagesResponse](t, f.request(t, "GET", "/api/v1/managed/sessions/"+run.SessionID+"/messages?limit=1&after="+page.NextAfter, f.credential.Token, "", "", 200))
	if len(next.Messages) != 1 || next.Messages[0].ID == page.Messages[0].ID || next.NextAfter != "" {
		t.Fatalf("invalid continuation page: %+v", next)
	}
}

func TestManagedHTTPAgentExecutionAndContinuation(t *testing.T) {
	f := newFixture(t)
	requests := make(chan map[string]any, 2)
	runtime := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		requests <- body
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"content":"Agent reply","usage":{"provider":"test","input_tokens":3,"output_tokens":2}}`))
	}))
	t.Cleanup(runtime.Close)
	config, _ := json.Marshal(map[string]string{"endpoint": runtime.URL})
	if _, err := f.db.Exec(t.Context(), "update agents set connector_type='http', config=$2 where id=$1", f.ids.ProductAgentID, config); err != nil {
		t.Fatal(err)
	}
	session := ""
	for _, key := range []string{"initial", "followup"} {
		run := f.run(t, key, session)
		result, err := httprunner.RunOnce(t.Context(), f.store, runtime.Client())
		if err != nil || !result.Claimed || result.Failed {
			t.Fatalf("runtime execution: %+v %v", result, err)
		}
		var body map[string]any
		select {
		case body = <-requests:
		default:
			t.Fatal("claimed run did not invoke HTTP Agent")
		}
		if body["run_id"] != run.ID || body["trigger_message_content"] != "hello" {
			t.Fatalf("invalid runtime input: %+v", body)
		}
		finished := decode[store.ManagedAgentRun](t, f.request(t, "GET", "/api/v1/managed/runs/"+run.ID, f.credential.Token, "", "", 200))
		if finished.Status != "completed" || finished.Output != "Agent reply" {
			t.Fatalf("output missing: %+v", finished)
		}
		if session != "" && finished.SessionID != session {
			t.Fatal("session identity changed")
		}
		session = finished.SessionID
	}
	page := decode[managedagents.MessagesResponse](t, f.request(t, "GET", "/api/v1/managed/sessions/"+session+"/messages", f.credential.Token, "", "", 200))
	if len(page.Messages) != 4 || page.Messages[1].Role != "assistant" || page.Messages[3].Content != "Agent reply" {
		t.Fatalf("persisted transcript: %+v", page)
	}
}
