package managedagents

import (
	"net/http"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/interaction"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
	"github.com/go-chi/chi/v5"
)

type InteractionsResponse struct {
	Interactions []store.AgentInteractionRead `json:"interactions"`
}

type DecisionRequest struct {
	Approved  *bool                        `json:"approved,omitempty"`
	Cancelled bool                         `json:"cancelled,omitempty"`
	Note      string                       `json:"note,omitempty"`
	Answers   []interaction.QuestionAnswer `json:"answers,omitempty"`
}

type DecisionResponse struct {
	Interaction     store.AgentInteractionRead `json:"interaction"`
	Applied         bool                       `json:"applied"`
	AlreadyResolved bool                       `json:"already_resolved"`
}

// @Summary List a run's human interactions
// @Tags managed-agent-api
// @Produce json
// @Param Authorization header string true "Bearer pma_ credential"
// @Param runID path string true "Run UUID"
// @Success 200 {object} InteractionsResponse
// @Failure 400,401,404,429,503 {object} ErrorResponse
// @Router /api/v1/managed/runs/{runID}/interactions [get]
func (h *handler) interactions(w http.ResponseWriter, r *http.Request) {
	items, err := h.deps.Store.ListManagedAgentInteractions(r.Context(), clientFrom(r).ID, chi.URLParam(r, "runID"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, InteractionsResponse{Interactions: items})
}

// @Summary Resolve a run's human interaction
// @Description Requires allow_interactions on the API client. Uses the shared durable interaction lifecycle, runtime acknowledgements and secret-answer redaction.
// @Tags managed-agent-api
// @Accept json
// @Produce json
// @Param Authorization header string true "Bearer pma_ credential"
// @Param runID path string true "Run UUID"
// @Param interactionID path string true "Interaction UUID"
// @Param body body DecisionRequest true "Approval or question answers"
// @Success 200 {object} DecisionResponse
// @Failure 400,401,403,404,409,410,429,503 {object} ErrorResponse
// @Router /api/v1/managed/runs/{runID}/interactions/{interactionID}/resolve [post]
func (h *handler) resolveInteraction(w http.ResponseWriter, r *http.Request) {
	client := clientFrom(r)
	if !client.AllowInteractions {
		writeError(w, r, store.ErrManagedForbidden)
		return
	}
	var body DecisionRequest
	if !decodeBody(w, r, &body) {
		return
	}
	items, err := h.deps.Store.ListManagedAgentInteractions(r.Context(), client.ID, chi.URLParam(r, "runID"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	found := false
	for _, item := range items {
		if item.ID == chi.URLParam(r, "interactionID") {
			found = true
			break
		}
	}
	if !found {
		writeError(w, r, store.ErrManagedNotFound)
		return
	}
	if h.deps.Interactions == nil {
		writeError(w, r, store.ErrManagedUnavailable)
		return
	}
	result, err := h.deps.Interactions.Resolve(r.Context(), interaction.ResolveRequest{
		WorkspaceID: client.WorkspaceID, AgentRunID: chi.URLParam(r, "runID"), InteractionID: chi.URLParam(r, "interactionID"),
		Actor:    interaction.Actor{UserID: client.CreatedBy, ActorID: client.ID, Source: "managed_api", ActorType: "api_client"},
		Decision: interaction.Decision{Approved: body.Approved, Cancelled: body.Cancelled, Note: body.Note, QuestionAnswers: body.Answers},
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, DecisionResponse{Interaction: result.Interaction, Applied: result.Applied, AlreadyResolved: result.AlreadyResolved})
}
