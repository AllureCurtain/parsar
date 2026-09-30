package managedagents

import (
	"net/http"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/auth"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
	"github.com/go-chi/chi/v5"
)

type ClientRequest struct {
	Name              string `json:"name"`
	ExpiresInDays     int    `json:"expires_in_days"`
	AllowInteractions bool   `json:"allow_interactions"`
}

type RotateRequest struct {
	ExpiresInDays int `json:"expires_in_days"`
}

type ClientsResponse struct {
	Clients []store.ManagedAgentClient `json:"clients"`
}

func (h *handler) adminInput(w http.ResponseWriter, r *http.Request) (store.ManagedAgentClientInput, bool) {
	actorID := auth.UserIDFromContext(r.Context())
	if actorID == "" {
		writeError(w, r, store.ErrManagedUnauthorized)
		return store.ManagedAgentClientInput{}, false
	}
	if h.deps.Store == nil {
		writeError(w, r, store.ErrManagedUnavailable)
		return store.ManagedAgentClientInput{}, false
	}
	return store.ManagedAgentClientInput{
		ActorID: actorID, WorkspaceID: chi.URLParam(r, "workspaceID"),
		AgentID: chi.URLParam(r, "agentID"), ClientID: chi.URLParam(r, "clientID"),
	}, true
}

// @Summary List an Agent's API clients
// @Tags managed-agent-admin
// @Produce json
// @Param workspaceID path string true "Workspace UUID"
// @Param agentID path string true "Agent UUID"
// @Success 200 {object} ClientsResponse
// @Failure 400,401,403,503 {object} ErrorResponse
// @Router /api/v1/workspaces/{workspaceID}/agents/{agentID}/api-clients [get]
func (h *handler) listClients(w http.ResponseWriter, r *http.Request) {
	in, ok := h.adminInput(w, r)
	if !ok {
		return
	}
	clients, err := h.deps.Store.ListManagedAgentClients(r.Context(), in)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, ClientsResponse{Clients: clients})
}

// @Summary Create an expiring Agent API client
// @Description Requires a workspace owner or admin. The token is disclosed only in this response. Expiry defaults to 90 days, maximum 365.
// @Tags managed-agent-admin
// @Accept json
// @Produce json
// @Param workspaceID path string true "Workspace UUID"
// @Param agentID path string true "Agent UUID"
// @Param body body ClientRequest true "Client settings"
// @Success 201 {object} store.ManagedAgentCredential
// @Failure 400,401,403,404,503 {object} ErrorResponse
// @Router /api/v1/workspaces/{workspaceID}/agents/{agentID}/api-clients [post]
func (h *handler) createClient(w http.ResponseWriter, r *http.Request) {
	in, ok := h.adminInput(w, r)
	if !ok {
		return
	}
	var body ClientRequest
	if !decodeBody(w, r, &body) {
		return
	}
	in.Name, in.ExpiresInDays, in.AllowInteractions = body.Name, body.ExpiresInDays, body.AllowInteractions
	credential, err := h.deps.Store.CreateManagedAgentClient(r.Context(), in)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, credential)
}

// @Summary Rotate an Agent API credential
// @Description Invalidates the old token immediately and preserves client sessions. The replacement token is disclosed once.
// @Tags managed-agent-admin
// @Accept json
// @Produce json
// @Param workspaceID path string true "Workspace UUID"
// @Param agentID path string true "Agent UUID"
// @Param clientID path string true "Client UUID"
// @Param body body RotateRequest true "New expiry"
// @Success 200 {object} store.ManagedAgentCredential
// @Failure 400,401,403,404,503 {object} ErrorResponse
// @Router /api/v1/workspaces/{workspaceID}/agents/{agentID}/api-clients/{clientID}/rotate [post]
func (h *handler) rotateClient(w http.ResponseWriter, r *http.Request) {
	in, ok := h.adminInput(w, r)
	if !ok {
		return
	}
	var body RotateRequest
	if !decodeBody(w, r, &body) {
		return
	}
	in.ExpiresInDays = body.ExpiresInDays
	credential, err := h.deps.Store.RotateManagedAgentClient(r.Context(), in)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, credential)
}

// @Summary Revoke an Agent API client
// @Tags managed-agent-admin
// @Produce json
// @Param workspaceID path string true "Workspace UUID"
// @Param agentID path string true "Agent UUID"
// @Param clientID path string true "Client UUID"
// @Success 200 {object} store.ManagedAgentClient
// @Failure 400,401,403,404,503 {object} ErrorResponse
// @Router /api/v1/workspaces/{workspaceID}/agents/{agentID}/api-clients/{clientID} [delete]
func (h *handler) revokeClient(w http.ResponseWriter, r *http.Request) {
	in, ok := h.adminInput(w, r)
	if !ok {
		return
	}
	client, err := h.deps.Store.RevokeManagedAgentClient(r.Context(), in)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, client)
}
