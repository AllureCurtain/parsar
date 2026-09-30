package store

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"time"
)

var (
	ErrManagedUnauthorized = errors.New("invalid or expired managed agent credential")
	ErrManagedForbidden    = errors.New("managed agent access denied")
	ErrManagedNotFound     = errors.New("managed agent resource not found")
	ErrManagedConflict     = errors.New("idempotency key already used with different input")
	ErrManagedUnavailable  = errors.New("agent is not available for invocation")
)

const ManagedAgentTokenPrefix = "pma_"

type ManagedAgentClient struct {
	ID                string     `json:"id"`
	WorkspaceID       string     `json:"workspace_id"`
	AgentID           string     `json:"agent_id"`
	CreatedBy         string     `json:"created_by"`
	Name              string     `json:"name"`
	TokenPrefix       string     `json:"token_prefix"`
	AllowInteractions bool       `json:"allow_interactions"`
	ExpiresAt         time.Time  `json:"expires_at"`
	RevokedAt         *time.Time `json:"revoked_at,omitempty"`
	LastUsedAt        *time.Time `json:"last_used_at,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	CredentialHash    string     `json:"-"`
}

type ManagedAgentClientInput struct {
	WorkspaceID       string
	AgentID           string
	ActorID           string
	ClientID          string
	Name              string
	ExpiresInDays     int
	AllowInteractions bool
}

type ManagedAgentCredential struct {
	Client ManagedAgentClient `json:"client"`
	Token  string             `json:"token"`
}

type ManagedAgentRunInput struct {
	Input     string `json:"input"`
	SessionID string `json:"session_id,omitempty"`
}

type ManagedAgentRun struct {
	ID         string     `json:"id"`
	SessionID  string     `json:"session_id"`
	AgentID    string     `json:"agent_id"`
	Status     string     `json:"status"`
	Output     string     `json:"output"`
	Error      string     `json:"error,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

func (r ManagedAgentRun) Terminal() bool {
	switch r.Status {
	case "completed", "failed", "cancelled", "interrupted":
		return true
	default:
		return false
	}
}

type ManagedAgentSession struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type ManagedAgentMessage struct {
	ID        string    `json:"id"`
	Role      string    `json:"role"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

type ManagedAgentEvent struct {
	Sequence   int64          `json:"sequence"`
	Type       string         `json:"type"`
	Data       map[string]any `json:"data"`
	OccurredAt time.Time      `json:"occurred_at"`
}

func managedHash(value string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(value)))
}
