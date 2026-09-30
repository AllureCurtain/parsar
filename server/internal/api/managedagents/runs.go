package managedagents

import (
	"context"
	"net/http"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/connector"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
	"github.com/go-chi/chi/v5"
)

// @Summary Inspect the authenticated Agent API client
// @Tags managed-agent-api
// @Produce json
// @Param Authorization header string true "Bearer pma_ credential"
// @Success 200 {object} store.ManagedAgentClient
// @Failure 401,429,503 {object} ErrorResponse
// @Router /api/v1/managed/client [get]
func (h *handler) client(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, clientFrom(r))
}

// @Summary Invoke an exported Agent
// @Description Omit session_id to start an isolated session. Reusing an Idempotency-Key with identical input returns the existing run; changed input returns 409. Input accepts 1-32000 Unicode code points after trimming whitespace.
// @Tags managed-agent-api
// @Accept json
// @Produce json
// @Param Authorization header string true "Bearer pma_ credential"
// @Param Idempotency-Key header string true "Stable unique request key, 1-255 printable ASCII characters"
// @Param body body store.ManagedAgentRunInput true "Input and optional session"
// @Success 202 {object} store.ManagedAgentRun "New run"
// @Success 200 {object} store.ManagedAgentRun "Existing run"
// @Failure 400,401,404,409,429,503 {object} ErrorResponse
// @Router /api/v1/managed/runs [post]
func (h *handler) createRun(w http.ResponseWriter, r *http.Request) {
	var body store.ManagedAgentRunInput
	if !decodeBody(w, r, &body) {
		return
	}
	run, replay, err := h.deps.Store.CreateManagedAgentRun(r.Context(), clientFrom(r), r.Header.Get("Idempotency-Key"), body)
	if err != nil {
		writeError(w, r, err)
		return
	}
	status := http.StatusAccepted
	if replay {
		status = http.StatusOK
	}
	w.Header().Set("Location", "/api/v1/managed/runs/"+run.ID)
	writeJSON(w, status, run)
}

// @Summary Get an Agent run and its final output
// @Tags managed-agent-api
// @Produce json
// @Param Authorization header string true "Bearer pma_ credential"
// @Param runID path string true "Run UUID"
// @Success 200 {object} store.ManagedAgentRun
// @Failure 400,401,404,429,503 {object} ErrorResponse
// @Router /api/v1/managed/runs/{runID} [get]
func (h *handler) getRun(w http.ResponseWriter, r *http.Request) {
	run, err := h.deps.Store.GetManagedAgentRun(r.Context(), clientFrom(r).ID, chi.URLParam(r, "runID"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, run)
}

// @Summary Cancel an Agent run
// @Description Cancellation is durable. Repeating cancellation retries delivery to the runtime. A runtime delivery failure returns 503; completed runs are returned unchanged.
// @Tags managed-agent-api
// @Produce json
// @Param Authorization header string true "Bearer pma_ credential"
// @Param runID path string true "Run UUID"
// @Success 200 {object} store.ManagedAgentRun
// @Failure 400,401,404,429,503 {object} ErrorResponse
// @Router /api/v1/managed/runs/{runID}/cancel [post]
func (h *handler) cancelRun(w http.ResponseWriter, r *http.Request) {
	client := clientFrom(r)
	run, err := h.deps.Store.GetManagedAgentRun(r.Context(), client.ID, chi.URLParam(r, "runID"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	if run.Terminal() && run.Status != "cancelled" {
		writeJSON(w, http.StatusOK, run)
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 10*time.Second)
	defer cancel()
	if err := h.deps.Store.CancelManagedAgentRun(ctx, client, run.ID); err != nil {
		writeError(w, r, err)
		return
	}
	detail, err := h.deps.Store.GetAgentRun(ctx, run.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if detail.Status == "cancelled" && detail.StartedAt != nil {
		if h.deps.Connectors == nil {
			writeError(w, r, store.ErrManagedUnavailable)
			return
		}
		target, err := h.deps.Connectors.Get(detail.ConnectorType)
		if err != nil {
			writeError(w, r, store.ErrManagedUnavailable)
			return
		}
		if err := target.Abort(ctx, connector.AbortInput{RunID: run.ID, ConversationID: run.SessionID}); err != nil {
			writeError(w, r, store.ErrManagedUnavailable)
			return
		}
	}
	run, err = h.deps.Store.GetManagedAgentRun(ctx, client.ID, run.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, run)
}
