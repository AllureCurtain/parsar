package main

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/dev"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

type claimLossStore struct {
	dev.RuntimeStore
	err error
}

func (s claimLossStore) MarkAgentRunRunning(context.Context, string, string) (store.MarkAgentRunRunningResult, error) {
	return store.MarkAgentRunRunningResult{}, s.err
}
func (claimLossStore) GetAgentRunInvocation(context.Context, string) (store.AgentRunInvocation, error) {
	panic("a lost claim must not dispatch")
}
func (claimLossStore) RecordAgentRunEvent(context.Context, store.RecordAgentRunEventInput) error {
	return nil
}
func (claimLossStore) SendAssistantMessageFromRun(context.Context, store.SendAssistantMessageFromRunInput) (store.CompleteAgentRunResult, error) {
	panic("a lost claim must not dispatch")
}

func TestStreamingDispatcherClaimLossDoesNotFailRun(t *testing.T) {
	for _, tc := range []struct {
		name        string
		err         error
		wantFailure bool
	}{
		{"duplicate dispatch", fmt.Errorf("claim: %w", store.ErrAgentRunNotStartable), false},
		{"busy lane", store.ErrAgentRunBlockedByQueue, false},
		{"database failure", errors.New("database unavailable"), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			failed := false
			adapter := streamingDispatcherAdapter{
				runtimeStore: claimLossStore{err: tc.err},
				failRun:      func(context.Context, string, string, string) { failed = true },
			}
			adapter.Start(t.Context(), store.StreamingDispatchInput{RunID: "run", ConversationID: "conversation"})
			if failed != tc.wantFailure {
				t.Fatalf("failed = %v, want %v", failed, tc.wantFailure)
			}
		})
	}
}
