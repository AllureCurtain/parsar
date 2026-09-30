package managedagents

import (
	"net/http"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
	"github.com/go-chi/chi/v5"
)

type SessionsResponse struct {
	Sessions   []store.ManagedAgentSession `json:"sessions"`
	NextBefore string                      `json:"next_before,omitempty"`
}

type MessagesResponse struct {
	Messages  []store.ManagedAgentMessage `json:"messages"`
	NextAfter string                      `json:"next_after,omitempty"`
}

type EventsResponse struct {
	Events    []store.ManagedAgentEvent `json:"events"`
	NextAfter int64                     `json:"next_after"`
	HasMore   bool                      `json:"has_more"`
}

// @Summary List the client's isolated sessions
// @Tags managed-agent-api
// @Produce json
// @Param Authorization header string true "Bearer pma_ credential"
// @Param before query string false "Session UUID cursor"
// @Param limit query int false "Page size, 1-200, default 50"
// @Success 200 {object} SessionsResponse
// @Failure 400,401,404,429,503 {object} ErrorResponse
// @Router /api/v1/managed/sessions [get]
func (h *handler) sessions(w http.ResponseWriter, r *http.Request) {
	limit, err := pageLimit(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	items, err := h.deps.Store.ListManagedAgentSessions(r.Context(), clientFrom(r).ID, r.URL.Query().Get("before"), limit+1)
	if err != nil {
		writeError(w, r, err)
		return
	}
	out := SessionsResponse{Sessions: items}
	if len(items) > int(limit) {
		out.Sessions, out.NextBefore = items[:limit], items[limit-1].ID
	}
	writeJSON(w, http.StatusOK, out)
}

// @Summary Get a client-owned session
// @Tags managed-agent-api
// @Produce json
// @Param Authorization header string true "Bearer pma_ credential"
// @Param sessionID path string true "Session UUID"
// @Success 200 {object} store.ManagedAgentSession
// @Failure 400,401,404,429,503 {object} ErrorResponse
// @Router /api/v1/managed/sessions/{sessionID} [get]
func (h *handler) session(w http.ResponseWriter, r *http.Request) {
	item, err := h.deps.Store.GetManagedAgentSession(r.Context(), clientFrom(r).ID, chi.URLParam(r, "sessionID"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

// @Summary Read a client-owned session's messages
// @Tags managed-agent-api
// @Produce json
// @Param Authorization header string true "Bearer pma_ credential"
// @Param sessionID path string true "Session UUID"
// @Param after query string false "Message UUID cursor"
// @Param limit query int false "Page size, 1-200, default 50"
// @Success 200 {object} MessagesResponse
// @Failure 400,401,404,429,503 {object} ErrorResponse
// @Router /api/v1/managed/sessions/{sessionID}/messages [get]
func (h *handler) messages(w http.ResponseWriter, r *http.Request) {
	limit, err := pageLimit(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	items, err := h.deps.Store.ListManagedAgentMessages(r.Context(), clientFrom(r).ID, chi.URLParam(r, "sessionID"), r.URL.Query().Get("after"), limit+1)
	if err != nil {
		writeError(w, r, err)
		return
	}
	out := MessagesResponse{Messages: items}
	if len(items) > int(limit) {
		out.Messages, out.NextAfter = items[:limit], items[limit-1].ID
	}
	writeJSON(w, http.StatusOK, out)
}

// @Summary Replay persisted Agent run events
// @Tags managed-agent-api
// @Produce json
// @Param Authorization header string true "Bearer pma_ credential"
// @Param runID path string true "Run UUID"
// @Param after query int false "Last consumed event sequence"
// @Param limit query int false "Page size, 1-200, default 50"
// @Success 200 {object} EventsResponse
// @Failure 400,401,404,429,503 {object} ErrorResponse
// @Router /api/v1/managed/runs/{runID}/events [get]
func (h *handler) events(w http.ResponseWriter, r *http.Request) {
	limit, err := pageLimit(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	after, err := eventCursor(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	items, err := h.deps.Store.ListManagedAgentEvents(r.Context(), clientFrom(r).ID, chi.URLParam(r, "runID"), after, limit+1)
	if err != nil {
		writeError(w, r, err)
		return
	}
	out := EventsResponse{Events: items, NextAfter: after, HasMore: len(items) > int(limit)}
	if out.HasMore {
		out.Events = items[:limit]
	}
	if len(out.Events) > 0 {
		out.NextAfter = out.Events[len(out.Events)-1].Sequence
	}
	writeJSON(w, http.StatusOK, out)
}
