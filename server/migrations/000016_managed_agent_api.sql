-- +goose Up

CREATE TABLE managed_agent_clients (
  id uuid PRIMARY KEY,
  workspace_id uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  agent_id uuid NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
  created_by uuid NOT NULL REFERENCES users(id),
  name text NOT NULL CHECK (length(name) BETWEEN 1 AND 100),
  token_hash text NOT NULL UNIQUE,
  token_prefix text NOT NULL,
  allow_interactions boolean NOT NULL DEFAULT false,
  expires_at timestamptz NOT NULL,
  revoked_at timestamptz,
  last_used_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX managed_agent_clients_agent ON managed_agent_clients(workspace_id, agent_id, created_at);

CREATE TABLE managed_agent_sessions (
  conversation_id uuid PRIMARY KEY REFERENCES conversations(id) ON DELETE CASCADE,
  client_id uuid NOT NULL REFERENCES managed_agent_clients(id) ON DELETE CASCADE,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX managed_agent_sessions_client ON managed_agent_sessions(client_id, created_at, conversation_id);

CREATE TABLE managed_agent_requests (
  client_id uuid NOT NULL REFERENCES managed_agent_clients(id) ON DELETE CASCADE,
  idempotency_key text NOT NULL CHECK (octet_length(idempotency_key) BETWEEN 1 AND 255),
  request_hash text NOT NULL,
  run_id uuid NOT NULL UNIQUE REFERENCES agent_runs(id) ON DELETE CASCADE,
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (client_id, idempotency_key)
);

-- +goose Down
DROP TABLE managed_agent_requests;
DROP TABLE managed_agent_sessions;
DROP TABLE managed_agent_clients;
