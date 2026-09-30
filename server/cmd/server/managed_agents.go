package main

import (
	"context"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/obs/log"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/api/managedagents"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/auth"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/connector"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/interaction"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
	"github.com/go-chi/chi/v5"
)

func mountManagedAgents(r chi.Router, dbStore *store.Store, sessions *auth.Middleware, connectors *connector.Registry, interactions *interaction.Service) {
	deps := managedagents.Deps{Store: dbStore, Connectors: connectors, Interactions: interactions}
	r.Group(func(r chi.Router) {
		r.Use(sessions.Require)
		managedagents.RegisterAdminRoutes(r, deps)
	})
	managedagents.RegisterPublicRoutes(r, deps)
}

func startManagedAgentRecovery(ctx context.Context, dbStore *store.Store) {
	if dbStore == nil {
		return
	}
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			if err := dbStore.RecoverQueuedManagedAgentRuns(ctx); err != nil && ctx.Err() == nil {
				log.Warn(ctx, "managed agent queue recovery failed", "error", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}
