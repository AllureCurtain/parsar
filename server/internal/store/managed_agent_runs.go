package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/db/sqlc"
	"github.com/jackc/pgx/v5"
)

func (s *Store) CreateManagedAgentRun(ctx context.Context, client ManagedAgentClient, key string, in ManagedAgentRunInput) (ManagedAgentRun, bool, error) {
	in.Input = strings.TrimSpace(in.Input)
	if in.Input == "" || utf8.RuneCountInString(in.Input) > 32000 || !utf8.ValidString(in.Input) || len(key) == 0 || len(key) > 255 {
		return ManagedAgentRun{}, false, ErrInvalidInput
	}
	for _, c := range key {
		if c < 33 || c > 126 {
			return ManagedAgentRun{}, false, ErrInvalidInput
		}
	}
	if in.SessionID != "" {
		if _, err := uuid(in.SessionID); err != nil {
			return ManagedAgentRun{}, false, ErrInvalidInput
		}
	}
	encoded, err := json.Marshal(in)
	if err != nil {
		return ManagedAgentRun{}, false, err
	}
	requestHash := managedHash(string(encoded))
	tx, err := beginTx(ctx, s.db)
	if err != nil {
		return ManagedAgentRun{}, false, err
	}
	defer tx.Rollback(ctx)
	q := sqlc.New(tx)
	// The client lock serializes retries with creation, rotation and revocation.
	if _, err := q.LockManagedAgentClient(ctx, mustUUID(client.ID)); err != nil {
		return ManagedAgentRun{}, false, err
	}
	if _, err := q.AuthenticateManagedAgentClient(ctx, client.CredentialHash); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			err = ErrManagedUnauthorized
		}
		return ManagedAgentRun{}, false, err
	}
	prior, err := q.GetManagedAgentRequest(ctx, sqlc.GetManagedAgentRequestParams{ClientID: mustUUID(client.ID), IdempotencyKey: key})
	if err == nil {
		if prior.RequestHash != requestHash {
			return ManagedAgentRun{}, false, ErrManagedConflict
		}
		run, err := managedRunFromQuery(ctx, q, client.ID, prior.RunID)
		return run, true, err
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return ManagedAgentRun{}, false, err
	}
	agent, err := q.GetAgentForUpdate(ctx, mustUUID(client.AgentID))
	if errors.Is(err, pgx.ErrNoRows) {
		return ManagedAgentRun{}, false, ErrManagedUnavailable
	}
	if err != nil {
		return ManagedAgentRun{}, false, err
	}
	if agent.WorkspaceID != client.WorkspaceID || agent.Status != "active" || (agent.ConnectorType != "agent_daemon" && agent.ConnectorType != "http") {
		return ManagedAgentRun{}, false, ErrManagedUnavailable
	}
	now := time.Now().UTC()
	sessionID := in.SessionID
	if sessionID == "" {
		sessionID = newID()
		metadata, _ := json.Marshal(map[string]string{"source": "managed_api", "primary_agent_id": client.AgentID})
		if _, err := q.CreateWorkspaceConversation(ctx, sqlc.CreateWorkspaceConversationParams{
			ID: mustUUID(sessionID), WorkspaceID: mustUUID(client.WorkspaceID), Surface: "api", Form: "thread",
			Title: agent.Name + " API", Metadata: metadata, Now: timestamptz(now),
		}); err != nil {
			return ManagedAgentRun{}, false, err
		}
		if err := q.CreateManagedAgentSession(ctx, sqlc.CreateManagedAgentSessionParams{
			ConversationID: mustUUID(sessionID), ClientID: mustUUID(client.ID),
		}); err != nil {
			return ManagedAgentRun{}, false, err
		}
	} else {
		if _, err := q.GetManagedAgentSession(ctx, sqlc.GetManagedAgentSessionParams{ID: mustUUID(sessionID), ClientID: mustUUID(client.ID)}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				err = ErrManagedNotFound
			}
			return ManagedAgentRun{}, false, err
		}
	}
	messageID, runID := newID(), newID()
	metadata, _ := json.Marshal(map[string]string{"source": "managed_api", "api_client_id": client.ID})
	if err := q.CreateMessage(ctx, sqlc.CreateMessageParams{
		ID: mustUUID(messageID), WorkspaceID: mustUUID(client.WorkspaceID), ConversationID: mustUUID(sessionID),
		SenderType: "user", SenderID: mustUUID(client.CreatedBy), Content: in.Input, Metadata: metadata, Now: timestamptz(now),
	}); err != nil {
		return ManagedAgentRun{}, false, err
	}
	if err := q.CreateAgentRun(ctx, sqlc.CreateAgentRunParams{
		ID: mustUUID(runID), WorkspaceID: mustUUID(client.WorkspaceID), ConversationID: mustUUID(sessionID),
		TriggerMessageID: mustUUID(messageID), TriggerChannel: "api", RequestedByID: mustUUID(client.CreatedBy),
		AgentID: mustUUID(client.AgentID), ConnectorType: agent.ConnectorType, Metadata: metadata, Now: timestamptz(now),
	}); err != nil {
		return ManagedAgentRun{}, false, err
	}
	if err := q.CreateManagedAgentRequest(ctx, sqlc.CreateManagedAgentRequestParams{
		ClientID: mustUUID(client.ID), IdempotencyKey: key, RequestHash: requestHash, RunID: mustUUID(runID),
	}); err != nil {
		return ManagedAgentRun{}, false, err
	}
	if err := managedAudit(ctx, q, client, "api_client", client.ID, "managed_agent.run.created", "agent_run", runID); err != nil {
		return ManagedAgentRun{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ManagedAgentRun{}, false, err
	}
	if connectorNeedsStreamingDispatch(agent.ConnectorType) {
		s.dispatchPendingStreaming(ctx, []StreamingDispatchInput{{RunID: runID, ConversationID: sessionID, ConnectorType: agent.ConnectorType}})
	}
	run, err := s.GetManagedAgentRun(ctx, client.ID, runID)
	return run, false, err
}

func (s *Store) GetManagedAgentRun(ctx context.Context, clientID, runID string) (ManagedAgentRun, error) {
	if _, err := uuid(runID); err != nil {
		return ManagedAgentRun{}, ErrInvalidInput
	}
	return managedRunFromQuery(ctx, sqlc.New(s.db), clientID, runID)
}

func managedRunFromQuery(ctx context.Context, q *sqlc.Queries, clientID, runID string) (ManagedAgentRun, error) {
	row, err := q.GetManagedAgentRun(ctx, sqlc.GetManagedAgentRunParams{ID: mustUUID(runID), ClientID: mustUUID(clientID)})
	if errors.Is(err, pgx.ErrNoRows) {
		return ManagedAgentRun{}, ErrManagedNotFound
	}
	if err != nil {
		return ManagedAgentRun{}, err
	}
	reason := ""
	if row.FailureReason != "" {
		reason = MapUserFacingReason(row.FailureReason)
	}
	return ManagedAgentRun{ID: row.RID, SessionID: row.SessionID, AgentID: row.RAgentID, Status: row.Status,
		Output: row.Output, Error: reason, CreatedAt: pgTime(row.CreatedAt), StartedAt: pgOptionalTime(row.StartedAt), FinishedAt: pgOptionalTime(row.FinishedAt)}, nil
}

func (s *Store) RecoverQueuedManagedAgentRuns(ctx context.Context) error {
	rows, err := sqlc.New(s.db).ListQueuedManagedAgentRuns(ctx, 100)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		s.dispatchPendingStreaming(ctx, []StreamingDispatchInput{{RunID: row.RunID, ConversationID: row.ConversationID, ConnectorType: row.ConnectorType}})
	}
	return nil
}
