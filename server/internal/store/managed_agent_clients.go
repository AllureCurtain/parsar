package store

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/db/sqlc"
	"github.com/jackc/pgx/v5"
)

func (s *Store) CreateManagedAgentClient(ctx context.Context, in ManagedAgentClientInput) (ManagedAgentCredential, error) {
	in.Name = strings.TrimSpace(in.Name)
	if utf8.RuneCountInString(in.Name) < 1 || utf8.RuneCountInString(in.Name) > 100 {
		return ManagedAgentCredential{}, ErrInvalidInput
	}
	expiresAt, err := managedExpiry(in.ExpiresInDays)
	if err != nil {
		return ManagedAgentCredential{}, err
	}
	token := ManagedAgentTokenPrefix + rand.Text() + rand.Text()
	client, err := s.mutateManagedClient(ctx, in, "managed_agent.client.created", func(q *sqlc.Queries) (sqlc.ManagedAgentClient, error) {
		return q.CreateManagedAgentClient(ctx, sqlc.CreateManagedAgentClientParams{
			ID: mustUUID(newID()), WorkspaceID: mustUUID(in.WorkspaceID), AgentID: mustUUID(in.AgentID),
			CreatedBy: mustUUID(in.ActorID), Name: in.Name, TokenHash: managedHash(token), TokenPrefix: token[:12],
			AllowInteractions: in.AllowInteractions, ExpiresAt: timestamptz(expiresAt),
		})
	})
	if err != nil {
		return ManagedAgentCredential{}, err
	}
	return ManagedAgentCredential{Client: client, Token: token}, nil
}

func (s *Store) RotateManagedAgentClient(ctx context.Context, in ManagedAgentClientInput) (ManagedAgentCredential, error) {
	expiresAt, err := managedExpiry(in.ExpiresInDays)
	if err != nil {
		return ManagedAgentCredential{}, err
	}
	token := ManagedAgentTokenPrefix + rand.Text() + rand.Text()
	client, err := s.mutateManagedClient(ctx, in, "managed_agent.client.rotated", func(q *sqlc.Queries) (sqlc.ManagedAgentClient, error) {
		return q.RotateManagedAgentClient(ctx, sqlc.RotateManagedAgentClientParams{
			ID: mustUUID(in.ClientID), WorkspaceID: mustUUID(in.WorkspaceID), AgentID: mustUUID(in.AgentID),
			TokenHash: managedHash(token), TokenPrefix: token[:12], ExpiresAt: timestamptz(expiresAt),
		})
	})
	if err != nil {
		return ManagedAgentCredential{}, err
	}
	return ManagedAgentCredential{Client: client, Token: token}, nil
}

func (s *Store) RevokeManagedAgentClient(ctx context.Context, in ManagedAgentClientInput) (ManagedAgentClient, error) {
	return s.mutateManagedClient(ctx, in, "managed_agent.client.revoked", func(q *sqlc.Queries) (sqlc.ManagedAgentClient, error) {
		return q.RevokeManagedAgentClient(ctx, sqlc.RevokeManagedAgentClientParams{
			ID: mustUUID(in.ClientID), WorkspaceID: mustUUID(in.WorkspaceID), AgentID: mustUUID(in.AgentID),
		})
	})
}

func (s *Store) ListManagedAgentClients(ctx context.Context, in ManagedAgentClientInput) ([]ManagedAgentClient, error) {
	q := sqlc.New(s.db)
	if err := requireManagedAdmin(ctx, q, in); err != nil {
		return nil, err
	}
	rows, err := q.ListManagedAgentClients(ctx, sqlc.ListManagedAgentClientsParams{
		WorkspaceID: mustUUID(in.WorkspaceID), AgentID: mustUUID(in.AgentID),
	})
	if err != nil {
		return nil, err
	}
	clients := make([]ManagedAgentClient, 0, len(rows))
	for _, row := range rows {
		clients = append(clients, managedClient(row))
	}
	return clients, nil
}

func (s *Store) AuthenticateManagedAgentClient(ctx context.Context, token string) (ManagedAgentClient, error) {
	if !strings.HasPrefix(token, ManagedAgentTokenPrefix) || len(token) != 56 {
		return ManagedAgentClient{}, ErrManagedUnauthorized
	}
	q := sqlc.New(s.db)
	row, err := q.AuthenticateManagedAgentClient(ctx, managedHash(token))
	if errors.Is(err, pgx.ErrNoRows) {
		return ManagedAgentClient{}, ErrManagedUnauthorized
	}
	if err != nil {
		return ManagedAgentClient{}, err
	}
	if err := q.TouchManagedAgentClient(ctx, row.ID); err != nil {
		return ManagedAgentClient{}, err
	}
	return managedClient(row), nil
}

func (s *Store) mutateManagedClient(ctx context.Context, in ManagedAgentClientInput, event string, mutate func(*sqlc.Queries) (sqlc.ManagedAgentClient, error)) (ManagedAgentClient, error) {
	tx, err := beginTx(ctx, s.db)
	if err != nil {
		return ManagedAgentClient{}, err
	}
	defer tx.Rollback(ctx)
	q := sqlc.New(tx)
	if err := requireManagedAdmin(ctx, q, in); err != nil {
		return ManagedAgentClient{}, err
	}
	row, err := mutate(q)
	if errors.Is(err, pgx.ErrNoRows) {
		return ManagedAgentClient{}, ErrManagedNotFound
	}
	if err != nil {
		return ManagedAgentClient{}, err
	}
	client := managedClient(row)
	if err := managedAudit(ctx, q, client, "user", in.ActorID, event, "managed_agent_client", client.ID); err != nil {
		return ManagedAgentClient{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ManagedAgentClient{}, err
	}
	return client, nil
}

func requireManagedAdmin(ctx context.Context, q *sqlc.Queries, in ManagedAgentClientInput) error {
	for _, id := range []string{in.WorkspaceID, in.AgentID, in.ActorID} {
		if _, err := uuid(id); err != nil {
			return ErrInvalidInput
		}
	}
	if in.ClientID != "" {
		if _, err := uuid(in.ClientID); err != nil {
			return ErrInvalidInput
		}
	}
	role, err := q.GetWorkspaceMemberRole(ctx, sqlc.GetWorkspaceMemberRoleParams{
		WorkspaceID: mustUUID(in.WorkspaceID), UserID: mustUUID(in.ActorID),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrManagedForbidden
	}
	if err != nil {
		return err
	}
	if role != "owner" && role != "admin" {
		return ErrManagedForbidden
	}
	exists, err := q.ManagedAgentExists(ctx, sqlc.ManagedAgentExistsParams{
		AgentID: mustUUID(in.AgentID), WorkspaceID: mustUUID(in.WorkspaceID),
	})
	if err != nil {
		return err
	}
	if !exists {
		return ErrManagedNotFound
	}
	return nil
}

func managedExpiry(days int) (time.Time, error) {
	if days == 0 {
		days = 90
	}
	if days < 1 || days > 365 {
		return time.Time{}, ErrInvalidInput
	}
	return time.Now().UTC().Add(time.Duration(days) * 24 * time.Hour), nil
}

func managedClient(row sqlc.ManagedAgentClient) ManagedAgentClient {
	return ManagedAgentClient{
		ID: pgUUIDString(row.ID), WorkspaceID: pgUUIDString(row.WorkspaceID), AgentID: pgUUIDString(row.AgentID),
		CreatedBy: pgUUIDString(row.CreatedBy), Name: row.Name, TokenPrefix: row.TokenPrefix,
		AllowInteractions: row.AllowInteractions, ExpiresAt: pgTime(row.ExpiresAt), RevokedAt: pgOptionalTime(row.RevokedAt),
		LastUsedAt: pgOptionalTime(row.LastUsedAt), CreatedAt: pgTime(row.CreatedAt), CredentialHash: row.TokenHash,
	}
}

func managedAudit(ctx context.Context, q *sqlc.Queries, client ManagedAgentClient, actorType, actorID, event, targetType, targetID string) error {
	payload, err := json.Marshal(map[string]string{"client_id": client.ID, "agent_id": client.AgentID})
	if err != nil {
		return err
	}
	return q.InsertAuditRecord(ctx, sqlc.InsertAuditRecordParams{
		OccurredAt: timestamptz(time.Now().UTC()), Source: "api", EventType: event,
		ActorType: actorType, ActorID: mustUUID(actorID), TargetType: targetType, TargetID: mustUUID(targetID),
		WorkspaceID: mustUUID(client.WorkspaceID), Payload: payload,
	})
}
