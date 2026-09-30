package managedagents

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

// @Summary Stream persisted Agent run events
// @Description Emits run_event with a durable sequence id and the event DTO. Reconnect with Last-Event-ID to resume. The done event contains the terminal run. Streams close after five minutes or credential revocation; disconnecting never cancels the run.
// @Tags managed-agent-api
// @Produce text/event-stream
// @Param Authorization header string true "Bearer pma_ credential"
// @Param runID path string true "Run UUID"
// @Param Last-Event-ID header string false "Last consumed event sequence"
// @Param after query int false "Fallback sequence when Last-Event-ID is absent"
// @Success 200 {string} string "SSE: run_event, done, error"
// @Failure 400,401,404,429,503 {object} ErrorResponse
// @Router /api/v1/managed/runs/{runID}/stream [get]
func (h *handler) stream(w http.ResponseWriter, r *http.Request) {
	after, err := eventCursor(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	client, runID := clientFrom(r), chi.URLParam(r, "runID")
	if _, err := h.deps.Store.GetManagedAgentRun(r.Context(), client.ID, runID); err != nil {
		writeError(w, r, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	control := http.NewResponseController(w)
	if err := control.Flush(); err != nil {
		return
	}
	poll := time.NewTicker(h.deps.PollInterval)
	defer poll.Stop()
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		parts := strings.Fields(r.Header.Get("Authorization"))
		if _, err := h.deps.Store.AuthenticateManagedAgentClient(ctx, parts[1]); err != nil {
			_ = writeStreamEvent(w, "error", 0, ErrorResponse{Error: "stream_closed", Message: "Reconnect with a valid credential."})
			return
		}
		run, err := h.deps.Store.GetManagedAgentRun(ctx, client.ID, runID)
		if err != nil {
			return
		}
		events, err := h.deps.Store.ListManagedAgentEvents(ctx, client.ID, runID, after, 200)
		if err != nil {
			_ = writeStreamEvent(w, "error", 0, ErrorResponse{Error: "stream_unavailable", Message: "Reconnect using the last event ID."})
			return
		}
		for _, event := range events {
			if err := writeStreamEvent(w, "run_event", event.Sequence, event); err != nil {
				return
			}
			after = event.Sequence
		}
		if len(events) == 200 {
			continue
		}
		if run.Terminal() {
			_ = writeStreamEvent(w, "done", 0, run)
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-poll.C:
		case <-heartbeat.C:
			_ = control.SetWriteDeadline(time.Now().Add(5 * time.Second))
			if _, err := fmt.Fprint(w, ": keep-alive\n\n"); err != nil {
				return
			}
			if err := control.Flush(); err != nil {
				return
			}
		}
	}
}

func writeStreamEvent(w http.ResponseWriter, kind string, sequence int64, value any) error {
	body, err := json.Marshal(value)
	if err != nil {
		return err
	}
	control := http.NewResponseController(w)
	_ = control.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if sequence > 0 {
		if _, err := fmt.Fprintf(w, "id: %d\n", sequence); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", kind, body); err != nil {
		return err
	}
	return control.Flush()
}
