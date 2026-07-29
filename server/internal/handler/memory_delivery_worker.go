package handler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/featureflags"
)

const (
	memoryDeliveryWorkerPollInterval = time.Second
	memoryDeliveryWorkerConcurrency  = 2
	memoryDeliveryWorkerBatchSize    = 100
)

// MemoryDeliveryWorker owns durable memory-provider outbox dispatch. Provider
// rows live in Postgres and are claimed with SKIP LOCKED, so process restarts
// and multiple API replicas can safely resume due deliveries.
type MemoryDeliveryWorker struct {
	h    *Handler
	done chan struct{}
}

func NewMemoryDeliveryWorker(h *Handler) *MemoryDeliveryWorker {
	return &MemoryDeliveryWorker{h: h, done: make(chan struct{})}
}

func (w *MemoryDeliveryWorker) Run(ctx context.Context) {
	if w == nil {
		return
	}
	defer close(w.done)
	if w.h == nil || w.h.DB == nil || w.h.MemoryService == nil {
		return
	}
	var workers sync.WaitGroup
	workers.Add(memoryDeliveryWorkerConcurrency)
	for range memoryDeliveryWorkerConcurrency {
		go func() {
			defer workers.Done()
			w.runLoop(ctx)
		}()
	}
	workers.Wait()
}

func (w *MemoryDeliveryWorker) runLoop(ctx context.Context) {
	ticker := time.NewTicker(memoryDeliveryWorkerPollInterval)
	defer ticker.Stop()
	for {
		worked, err := w.ProcessNext(ctx)
		if err != nil && !errors.Is(err, context.Canceled) {
			slog.Error("memory delivery worker: process due deliveries", "error", err)
		}
		if worked {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (w *MemoryDeliveryWorker) WaitWithTimeout(timeout time.Duration) bool {
	if w == nil {
		return true
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-w.done:
		return true
	case <-timer.C:
		return false
	}
}

func (w *MemoryDeliveryWorker) ProcessNext(ctx context.Context) (bool, error) {
	if w == nil || w.h == nil || w.h.DB == nil || w.h.MemoryService == nil {
		return false, nil
	}
	if !featureflags.MemoryGatewayEnabled(ctx, w.h.FeatureFlags) {
		return false, nil
	}
	workspaces, err := w.dueWorkspaces(ctx, memoryDeliveryWorkerConcurrency)
	if err != nil {
		return false, err
	}
	worked := false
	for _, workspaceID := range workspaces {
		results, err := w.h.MemoryService.DispatchDueMemoryProviderDeliveries(ctx, workspaceID, memoryDeliveryWorkerBatchSize)
		if err != nil {
			return worked, fmt.Errorf("dispatch memory provider deliveries: %w", err)
		}
		if len(results) > 0 {
			worked = true
		}
	}
	return worked, nil
}

func (w *MemoryDeliveryWorker) dueWorkspaces(ctx context.Context, limit int) ([]pgtype.UUID, error) {
	if limit <= 0 {
		limit = memoryDeliveryWorkerConcurrency
	}
	now := time.Now().UTC()
	leaseTimeout := w.h.MemoryService.DeliveryLeaseTimeout
	if leaseTimeout <= 0 {
		leaseTimeout = 2 * time.Minute
	}
	rows, err := w.h.DB.Query(ctx, `
		SELECT memory_provider_delivery.workspace_id
		FROM memory_provider_delivery
		JOIN memory_workspace_config
		  ON memory_workspace_config.workspace_id = memory_provider_delivery.workspace_id
		 AND memory_workspace_config.enabled = true
		WHERE (
		  (memory_provider_delivery.status IN ('queued', 'retry') AND memory_provider_delivery.next_attempt_at <= $1)
		  OR (memory_provider_delivery.status = 'delivering' AND memory_provider_delivery.updated_at <= $2)
		)
		GROUP BY memory_provider_delivery.workspace_id
		ORDER BY min(memory_provider_delivery.next_attempt_at) ASC, min(memory_provider_delivery.created_at) ASC
		LIMIT $3
	`, pgtype.Timestamptz{Time: now, Valid: true}, pgtype.Timestamptz{Time: now.Add(-leaseTimeout), Valid: true}, int32(limit))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list due memory workspaces: %w", err)
	}
	defer rows.Close()
	var out []pgtype.UUID
	for rows.Next() {
		var workspaceID pgtype.UUID
		if err := rows.Scan(&workspaceID); err != nil {
			return nil, err
		}
		out = append(out, workspaceID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
