package store

import (
	"context"
	"errors"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/db/sqlc"
	"github.com/jackc/pgx/v5"
)

func (s *Store) GetManagedAgentSession(ctx context.Context, clientID, sessionID string) (ManagedAgentSession, error) {
	id, err := uuid(sessionID)
	if err != nil {
		return ManagedAgentSession{}, ErrInvalidInput
	}
	row, err := sqlc.New(s.db).GetManagedAgentSession(ctx, sqlc.GetManagedAgentSessionParams{ID: id, ClientID: mustUUID(clientID)})
	if errors.Is(err, pgx.ErrNoRows) {
		return ManagedAgentSession{}, ErrManagedNotFound
	}
	if err != nil {
		return ManagedAgentSession{}, err
	}
	return ManagedAgentSession{ID: row.CID, Title: row.Title, CreatedAt: pgTime(row.CreatedAt), UpdatedAt: pgTime(row.UpdatedAt)}, nil
}

func (s *Store) ListManagedAgentSessions(ctx context.Context, clientID, before string, limit int32) ([]ManagedAgentSession, error) {
	if before != "" {
		if _, err := s.GetManagedAgentSession(ctx, clientID, before); err != nil {
			return nil, err
		}
	}
	rows, err := sqlc.New(s.db).ListManagedAgentSessions(ctx, sqlc.ListManagedAgentSessionsParams{
		ClientID: mustUUID(clientID), BeforeID: nullableUUID(before), ItemLimit: managedReadLimit(limit),
	})
	if err != nil {
		return nil, err
	}
	out := make([]ManagedAgentSession, 0, len(rows))
	for _, row := range rows {
		out = append(out, ManagedAgentSession{ID: row.CID, Title: row.Title, CreatedAt: pgTime(row.CreatedAt), UpdatedAt: pgTime(row.UpdatedAt)})
	}
	return out, nil
}

func (s *Store) ListManagedAgentMessages(ctx context.Context, clientID, sessionID, after string, limit int32) ([]ManagedAgentMessage, error) {
	if _, err := s.GetManagedAgentSession(ctx, clientID, sessionID); err != nil {
		return nil, err
	}
	if after != "" {
		if _, err := uuid(after); err != nil {
			return nil, ErrInvalidInput
		}
	}
	rows, err := sqlc.New(s.db).ListManagedAgentSessionMessages(ctx, sqlc.ListManagedAgentSessionMessagesParams{
		ClientID: mustUUID(clientID), SessionID: mustUUID(sessionID), AfterID: nullableUUID(after), ItemLimit: managedReadLimit(limit),
	})
	if err != nil {
		return nil, err
	}
	out := make([]ManagedAgentMessage, 0, len(rows))
	for _, row := range rows {
		role := row.SenderType
		if role == "agent" {
			role = "assistant"
		}
		out = append(out, ManagedAgentMessage{ID: row.MID, Role: role, Content: row.Content, CreatedAt: pgTime(row.CreatedAt)})
	}
	return out, nil
}

func (s *Store) ListManagedAgentEvents(ctx context.Context, clientID, runID string, after int64, limit int32) ([]ManagedAgentEvent, error) {
	if _, err := s.GetManagedAgentRun(ctx, clientID, runID); err != nil {
		return nil, err
	}
	if after < 0 {
		return nil, ErrInvalidInput
	}
	rows, err := sqlc.New(s.db).ListManagedAgentRunEvents(ctx, sqlc.ListManagedAgentRunEventsParams{
		ClientID: mustUUID(clientID), RunID: mustUUID(runID), AfterSequence: after, ItemLimit: managedReadLimit(limit),
	})
	if err != nil {
		return nil, err
	}
	out := make([]ManagedAgentEvent, 0, len(rows))
	for _, row := range rows {
		out = append(out, ManagedAgentEvent{Sequence: row.Sequence, Type: row.EventKind, Data: decodeJSONMap(row.Payload), OccurredAt: pgTime(row.OccurredAt)})
	}
	return out, nil
}

func (s *Store) ListManagedAgentInteractions(ctx context.Context, clientID, runID string) ([]AgentInteractionRead, error) {
	if _, err := s.GetManagedAgentRun(ctx, clientID, runID); err != nil {
		return nil, err
	}
	ids, err := sqlc.New(s.db).ListManagedAgentRunInteractionIDs(ctx, sqlc.ListManagedAgentRunInteractionIDsParams{ClientID: mustUUID(clientID), RunID: mustUUID(runID)})
	if err != nil {
		return nil, err
	}
	out := make([]AgentInteractionRead, 0, len(ids))
	for _, id := range ids {
		item, err := s.GetAgentInteraction(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, nil
}

func managedReadLimit(limit int32) int32 {
	if limit < 1 {
		return 50
	}
	if limit > 201 {
		return 201
	}
	return limit
}
