package main

import (
	"context"
	"errors"

	"github.com/MiniMax-AI-Dev/parsar/internal/obs/log"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/dev"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

type streamingDispatcherAdapter struct {
	runtimeStore dev.RuntimeStore
	deps         dev.StreamingDispatchDeps
	failRun      func(ctx context.Context, runID, source, reason string)
}

func (a streamingDispatcherAdapter) Start(ctx context.Context, in store.StreamingDispatchInput) {
	_, err := dev.StartConversationRun(ctx, a.runtimeStore, a.deps, in.RunID, in.ConversationID)
	if err == nil || errors.Is(err, store.ErrAgentRunNotStartable) {
		return
	}
	log.Bg().Error("streaming dispatcher: auto-start failed",
		"run_id", in.RunID, "conversation_id", in.ConversationID,
		"connector_type", in.ConnectorType, "error", err)
	if a.failRun != nil {
		a.failRun(ctx, in.RunID, in.ConnectorType, "auto-start failed: "+err.Error())
	}
}
