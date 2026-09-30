package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/db/sqlc"
)

func cancelAgentRunInTx(ctx context.Context, db sqlc.DBTX, runID, reason string) (bool, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "user_clicked_cancel"
	}
	id, err := uuid(runID)
	if err != nil {
		return false, err
	}
	now := timestamptz(time.Now().UTC())
	tag, err := db.Exec(ctx, `update agent_runs set status='cancelled', failure_reason=$2, finished_at=$3, updated_at=$3,
metadata = metadata || jsonb_build_object('cancel_reason', $2::text)
where id=$1::uuid and status not in ('completed','failed','cancelled')`, id, reason, now)
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() == 1 {
		return true, nil
	}
	var exists bool
	if err := db.QueryRow(ctx, `select exists(select 1 from agent_runs where id=$1::uuid)`, id).Scan(&exists); err != nil {
		return false, err
	}
	if !exists {
		return false, fmt.Errorf("%w: %s", ErrUnknownAgentRun, runID)
	}
	return false, nil
}

func (s *Store) CancelAgentRun(ctx context.Context, runID, reason string) (bool, error) {
	tx, err := beginTx(ctx, s.db)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	changed, err := cancelAgentRunInTx(ctx, tx, runID, reason)
	if err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	if changed {
		s.dispatchNextQueuedRunAfter(ctx, runID)
	}
	return changed, nil
}

func (s *Store) CancelManagedAgentRun(ctx context.Context, client ManagedAgentClient, runID string) error {
	tx, err := beginTx(ctx, s.db)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	q, txStore := sqlc.New(tx), New(tx)
	if _, err := txStore.GetManagedAgentRun(ctx, client.ID, runID); err != nil {
		return err
	}
	// Nested store transactions use savepoints; cancellation, events and audit commit together.
	changed, err := cancelAgentRunInTx(ctx, tx, runID, "managed_api_cancel")
	if err != nil {
		return err
	}
	if changed {
		if err := txStore.RecordAgentRunEvent(ctx, RecordAgentRunEventInput{
			RunID: runID, EventKind: "run.cancelled",
			Payload: map[string]any{"source": "managed_api", "client_id": client.ID},
		}); err != nil {
			return err
		}
		if err := managedAudit(ctx, q, client, "api_client", client.ID, "managed_agent.run.cancelled", "agent_run", runID); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	if changed {
		s.dispatchNextQueuedRunAfter(ctx, runID)
	}
	return nil
}
