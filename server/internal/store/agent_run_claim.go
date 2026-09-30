package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/db/sqlc"
	"github.com/jackc/pgx/v5"
)

func (s *Store) MarkAgentRunRunning(ctx context.Context, runID, conversationID string) (MarkAgentRunRunningResult, error) {
	runUUID, err := uuid(runID)
	if err != nil {
		return MarkAgentRunRunningResult{}, err
	}
	conversationUUID, err := uuid(conversationID)
	if err != nil {
		return MarkAgentRunRunningResult{}, err
	}
	tx, err := beginTx(ctx, s.db)
	if err != nil {
		return MarkAgentRunRunningResult{}, err
	}
	defer tx.Rollback(ctx)
	q := sqlc.New(tx)
	// A separate lock statement gives the claim a fresh snapshot after a competing lane claim commits.
	if err := q.LockAgentRunLane(ctx, sqlc.LockAgentRunLaneParams{ID: runUUID, ConversationID: conversationUUID}); err != nil {
		return MarkAgentRunRunningResult{}, err
	}
	row, err := q.ClaimStreamingAgentRun(ctx, sqlc.ClaimStreamingAgentRunParams{
		ID: runUUID, ConversationID: conversationUUID, Now: timestamptz(time.Now().UTC()),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		state, readErr := q.GetAgentRunClaimState(ctx, runUUID)
		if errors.Is(readErr, pgx.ErrNoRows) {
			return MarkAgentRunRunningResult{}, fmt.Errorf("%w: %s", ErrUnknownAgentRun, runID)
		}
		if readErr != nil {
			return MarkAgentRunRunningResult{}, readErr
		}
		if state.ConversationID == conversationID && state.Status == "queued" {
			return MarkAgentRunRunningResult{}, fmt.Errorf("%w: %s", ErrAgentRunBlockedByQueue, runID)
		}
		return MarkAgentRunRunningResult{}, fmt.Errorf("%w: %s", ErrAgentRunNotStartable, runID)
	}
	if err != nil {
		return MarkAgentRunRunningResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return MarkAgentRunRunningResult{}, err
	}
	return MarkAgentRunRunningResult{RunID: row.RunID, WorkspaceID: row.WorkspaceID,
		ConversationID: row.ConversationID, Status: row.Status, StartedAt: pgTime(row.StartedAt)}, nil
}
