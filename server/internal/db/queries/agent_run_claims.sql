-- name: LockAgentRunLane :exec
SELECT pg_advisory_xact_lock(hashtextextended(
  'agent-run-lane:' || conversation_id::text || ':' || agent_id::text, 0))
FROM agent_runs WHERE id = @id::uuid AND conversation_id = @conversation_id::uuid;

-- name: ClaimStreamingAgentRun :one
UPDATE agent_runs
SET status = 'running', started_at = coalesce(started_at, @now), updated_at = @now,
    metadata = metadata || jsonb_build_object('started_by', 'conversation_stream')
WHERE id = @id::uuid AND conversation_id = @conversation_id::uuid AND status = 'queued'
  AND NOT EXISTS (
    SELECT 1 FROM agent_runs sibling
    WHERE sibling.conversation_id = @conversation_id::uuid
      AND sibling.agent_id = (SELECT agent_id FROM agent_runs WHERE id = @id::uuid)
      AND sibling.status = 'running' AND sibling.id != @id::uuid
  )
RETURNING id::text AS run_id, workspace_id::text, conversation_id::text, status, started_at;

-- name: GetAgentRunClaimState :one
SELECT conversation_id::text, status FROM agent_runs WHERE id = @id::uuid;
