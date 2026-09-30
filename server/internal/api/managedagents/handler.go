package managedagents

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/obs/log"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/connector"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/interaction"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/httprate"
)

type Deps struct {
	Store        *store.Store
	Connectors   *connector.Registry
	Interactions *interaction.Service
	PollInterval time.Duration
}

type handler struct{ deps Deps }
type clientKey struct{}

type ErrorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

var errInvalidRequest = errors.New("invalid request")

// RegisterAdminRoutes requires the caller's browser-session middleware.
func RegisterAdminRoutes(r chi.Router, deps Deps) {
	h := &handler{deps: deps}
	r.Route("/api/v1/workspaces/{workspaceID}/agents/{agentID}/api-clients", func(r chi.Router) {
		r.Get("/", h.listClients)
		r.Post("/", h.createClient)
		r.Post("/{clientID}/rotate", h.rotateClient)
		r.Delete("/{clientID}", h.revokeClient)
	})
}

// RegisterPublicRoutes authenticates only managed-agent bearer credentials.
func RegisterPublicRoutes(r chi.Router, deps Deps) {
	if deps.PollInterval <= 0 {
		deps.PollInterval = 500 * time.Millisecond
	}
	h := &handler{deps: deps}
	r.Route("/api/v1/managed", func(r chi.Router) {
		r.Use(httprate.LimitBy(240, time.Minute, httprate.KeyByIP))
		r.Use(h.authenticate)
		r.Get("/client", h.client)
		r.Post("/runs", h.createRun)
		r.Get("/runs/{runID}", h.getRun)
		r.Post("/runs/{runID}/cancel", h.cancelRun)
		r.Get("/runs/{runID}/events", h.events)
		r.Get("/runs/{runID}/stream", h.stream)
		r.Get("/runs/{runID}/interactions", h.interactions)
		r.Post("/runs/{runID}/interactions/{interactionID}/resolve", h.resolveInteraction)
		r.Get("/sessions", h.sessions)
		r.Get("/sessions/{sessionID}", h.session)
		r.Get("/sessions/{sessionID}/messages", h.messages)
	})
}

func (h *handler) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h.deps.Store == nil {
			writeError(w, r, store.ErrManagedUnavailable)
			return
		}
		parts := strings.Fields(r.Header.Get("Authorization"))
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			writeError(w, r, store.ErrManagedUnauthorized)
			return
		}
		client, err := h.deps.Store.AuthenticateManagedAgentClient(r.Context(), parts[1])
		if err != nil {
			writeError(w, r, err)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), clientKey{}, client)))
	})
}

func clientFrom(r *http.Request) store.ManagedAgentClient {
	return r.Context().Value(clientKey{}).(store.ManagedAgentClient)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, r *http.Request, err error) {
	status, code, message := http.StatusInternalServerError, "internal_error", "The request could not be completed."
	switch {
	case errors.Is(err, store.ErrInvalidInput), errors.Is(err, errInvalidRequest), errors.Is(err, interaction.ErrInvalidDecision):
		status, code, message = http.StatusBadRequest, "invalid_request", "Check the request body, identifiers, and pagination parameters."
	case errors.Is(err, store.ErrManagedUnauthorized):
		status, code, message = http.StatusUnauthorized, "unauthenticated", "A valid credential is required."
		w.Header().Set("WWW-Authenticate", `Bearer realm="managed-agent"`)
	case errors.Is(err, store.ErrManagedForbidden):
		status, code, message = http.StatusForbidden, "forbidden", "This credential or account cannot perform the operation."
	case errors.Is(err, store.ErrManagedNotFound), errors.Is(err, store.ErrUnknownAgentRun), errors.Is(err, interaction.ErrNotFound):
		status, code, message = http.StatusNotFound, "not_found", "The resource was not found."
	case errors.Is(err, store.ErrManagedConflict):
		status, code, message = http.StatusConflict, "idempotency_conflict", "This Idempotency-Key was already used with different input."
	case errors.Is(err, interaction.ErrAlreadyResolving):
		status, code, message = http.StatusConflict, "interaction_resolving", "The interaction is being resolved. Retry later."
	case errors.Is(err, interaction.ErrExpired), errors.Is(err, interaction.ErrRuntimeGone):
		status, code, message = http.StatusGone, "interaction_closed", "The interaction is no longer pending."
	case errors.Is(err, store.ErrManagedUnavailable), errors.Is(err, interaction.ErrRuntimeUnavailable):
		status, code, message = http.StatusServiceUnavailable, "unavailable", "The agent or runtime is currently unavailable."
	default:
		log.Error(r.Context(), "managed agent request failed", "error", err)
	}
	writeJSON(w, status, ErrorResponse{Error: code, Message: message})
}

func decodeBody(w http.ResponseWriter, r *http.Request, out any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		writeError(w, r, errInvalidRequest)
		return false
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		writeError(w, r, errInvalidRequest)
		return false
	}
	return true
}

func pageLimit(r *http.Request) (int32, error) {
	value := r.URL.Query().Get("limit")
	if value == "" {
		return 50, nil
	}
	n, err := strconv.ParseInt(value, 10, 32)
	if err != nil || n < 1 || n > 200 {
		return 0, errInvalidRequest
	}
	return int32(n), nil
}

func eventCursor(r *http.Request) (int64, error) {
	value := r.URL.Query().Get("after")
	if resume := r.Header.Get("Last-Event-ID"); resume != "" {
		value = resume
	}
	if value == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil || n < 0 {
		return 0, errInvalidRequest
	}
	return n, nil
}
