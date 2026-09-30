-- name: CreateManagedAgentClient :one
INSERT INTO managed_agent_clients
  (id, workspace_id, agent_id, created_by, name, token_hash, token_prefix, allow_interactions, expires_at)
SELECT @id::uuid, a.workspace_id, a.id, wm.user_id, @name, @token_hash, @token_prefix, @allow_interactions, @expires_at
FROM agents a
JOIN workspaces w ON w.id = a.workspace_id AND w.deleted_at IS NULL
JOIN workspace_members wm ON wm.workspace_id = a.workspace_id
  AND wm.user_id = @created_by::uuid AND wm.status = 'active'
  AND wm.deleted_at IS NULL AND wm.role IN ('owner', 'admin')
WHERE a.id = @agent_id::uuid AND a.workspace_id = @workspace_id::uuid
  AND a.deleted_at IS NULL
RETURNING managed_agent_clients.*;

-- name: ListManagedAgentClients :many
SELECT * FROM managed_agent_clients
WHERE workspace_id = @workspace_id::uuid AND agent_id = @agent_id::uuid
ORDER BY created_at DESC, id DESC;

-- name: ManagedAgentExists :one
SELECT EXISTS (
  SELECT 1 FROM agents a JOIN workspaces w ON w.id = a.workspace_id AND w.deleted_at IS NULL
  WHERE a.id = @agent_id::uuid AND a.workspace_id = @workspace_id::uuid AND a.deleted_at IS NULL
);

-- name: RotateManagedAgentClient :one
UPDATE managed_agent_clients
SET token_hash = @token_hash, token_prefix = @token_prefix, expires_at = @expires_at, updated_at = now()
WHERE id = @id::uuid AND workspace_id = @workspace_id::uuid AND agent_id = @agent_id::uuid
  AND revoked_at IS NULL
RETURNING *;

-- name: RevokeManagedAgentClient :one
UPDATE managed_agent_clients SET revoked_at = coalesce(revoked_at, now()), updated_at = now()
WHERE id = @id::uuid AND workspace_id = @workspace_id::uuid AND agent_id = @agent_id::uuid
RETURNING *;

-- name: AuthenticateManagedAgentClient :one
SELECT c.* FROM managed_agent_clients c
JOIN agents a ON a.id = c.agent_id AND a.workspace_id = c.workspace_id AND a.deleted_at IS NULL
JOIN workspaces w ON w.id = c.workspace_id AND w.deleted_at IS NULL
JOIN users u ON u.id = c.created_by AND u.deleted_at IS NULL AND u.status = 'active'
JOIN workspace_members wm ON wm.workspace_id = c.workspace_id AND wm.user_id = c.created_by
  AND wm.status = 'active' AND wm.deleted_at IS NULL AND wm.role IN ('owner', 'admin')
WHERE c.token_hash = @token_hash AND c.revoked_at IS NULL AND c.expires_at > now();

-- name: TouchManagedAgentClient :exec
UPDATE managed_agent_clients SET last_used_at = now()
WHERE id = @id::uuid AND (last_used_at IS NULL OR last_used_at < now() - interval '1 minute');

-- name: LockManagedAgentClient :one
SELECT * FROM managed_agent_clients WHERE id = @id::uuid FOR UPDATE;

-- name: GetManagedAgentRequest :one
SELECT request_hash, run_id::text FROM managed_agent_requests
WHERE client_id = @client_id::uuid AND idempotency_key = @idempotency_key;

-- name: CreateManagedAgentRequest :exec
INSERT INTO managed_agent_requests(client_id, idempotency_key, request_hash, run_id)
VALUES (@client_id::uuid, @idempotency_key, @request_hash, @run_id::uuid);

-- name: CreateManagedAgentSession :exec
INSERT INTO managed_agent_sessions(conversation_id, client_id)
VALUES (@conversation_id::uuid, @client_id::uuid);

-- name: GetManagedAgentSession :one
SELECT c.id::text, c.title, c.created_at, c.updated_at
FROM conversations c JOIN managed_agent_sessions s ON s.conversation_id = c.id
WHERE c.id = @id::uuid AND s.client_id = @client_id::uuid AND c.deleted_at IS NULL AND c.status = 'active';

-- name: ListManagedAgentSessions :many
SELECT c.id::text, c.title, c.created_at, c.updated_at
FROM conversations c JOIN managed_agent_sessions s ON s.conversation_id = c.id
WHERE s.client_id = @client_id::uuid AND c.deleted_at IS NULL AND c.status = 'active'
  AND (sqlc.narg(before_id)::uuid IS NULL OR (c.created_at, c.id) < (
    SELECT p.created_at, p.id FROM conversations p WHERE p.id = sqlc.narg(before_id)::uuid))
ORDER BY c.created_at DESC, c.id DESC LIMIT @item_limit::int;

-- name: GetManagedAgentRun :one
SELECT r.id::text, r.conversation_id::text AS session_id, r.agent_id::text, r.status,
  r.failure_reason, r.created_at, r.started_at, r.finished_at,
  coalesce(m.content, '')::text AS output
FROM agent_runs r
JOIN managed_agent_requests req ON req.run_id = r.id
JOIN conversations c ON c.id = r.conversation_id AND c.deleted_at IS NULL
LEFT JOIN messages m ON m.id = r.output_message_id AND m.deleted_at IS NULL
WHERE r.id = @id::uuid AND req.client_id = @client_id::uuid;

-- name: ListManagedAgentSessionMessages :many
SELECT m.id::text, m.sender_type, m.content, m.created_at
FROM messages m JOIN managed_agent_sessions s ON s.conversation_id = m.conversation_id
WHERE s.client_id = @client_id::uuid AND m.conversation_id = @session_id::uuid AND m.deleted_at IS NULL
  AND (sqlc.narg(after_id)::uuid IS NULL OR (m.created_at, m.id) > (
    SELECT p.created_at, p.id FROM messages p
    WHERE p.id = sqlc.narg(after_id)::uuid AND p.conversation_id = @session_id::uuid AND p.deleted_at IS NULL))
ORDER BY m.created_at, m.id LIMIT @item_limit::int;

-- name: ListManagedAgentRunEvents :many
SELECT e.* FROM agent_run_events e JOIN managed_agent_requests req ON req.run_id = e.agent_run_id
WHERE req.client_id = @client_id::uuid AND req.run_id = @run_id::uuid AND e.sequence > @after_sequence::bigint
ORDER BY e.sequence LIMIT @item_limit::int;

-- name: ListManagedAgentRunInteractionIDs :many
SELECT i.id::text FROM agent_interactions i
JOIN managed_agent_requests req ON req.run_id = i.agent_run_id
WHERE req.client_id = @client_id::uuid AND req.run_id = @run_id::uuid
ORDER BY i.created_at, i.id;

-- name: ListQueuedManagedAgentRuns :many
SELECT r.id::text AS run_id, r.conversation_id::text AS conversation_id, r.connector_type
FROM agent_runs r JOIN managed_agent_requests req ON req.run_id = r.id
WHERE r.status = 'queued' AND r.connector_type = 'agent_daemon'
  AND NOT EXISTS (
    SELECT 1 FROM agent_runs pending
    WHERE pending.conversation_id = r.conversation_id AND pending.agent_id = r.agent_id
      AND (pending.status = 'running' OR (pending.status = 'queued'
        AND (pending.created_at, pending.id) < (r.created_at, r.id)))
  )
ORDER BY r.created_at, r.id LIMIT @item_limit::int;
