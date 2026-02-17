package sync

import (
	"context"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/trace"
)

const (
	otelScope       = "reminderrelay/sync"
	spanReconcile   = "sync.reconcile"
	metricCreated   = "reminderrelay.sync.items.created"
	metricUpdated   = "reminderrelay.sync.items.updated"
	metricDeleted   = "reminderrelay.sync.items.deleted"
	metricConflicts = "reminderrelay.sync.conflicts"
	metricErrors    = "reminderrelay.sync.errors"
)

// HAConnector provides WebSocket lifecycle methods for the Engine.
// Implemented by [homeassistant.Adapter].
type HAConnector interface {
	HASource
	Connect(ctx context.Context) error
	Close() error
	SubscribeChanges(ctx context.Context, entityIDs []string, callback func(entityID string)) error
}

// Engine orchestrates the sync lifecycle: polling loop + optional WebSocket
// listener for instant HA updates. Create one with [NewEngine] and start it
// with [Engine.Run].
type Engine struct {
	reconciler   *Reconciler
	haConn       HAConnector
	listMappings map[string]string
	pollInterval time.Duration
	log          *slog.Logger

	// OTel instruments — always non-nil (no-op when telemetry is disabled).
	tracer     trace.Tracer
	cntCreated metric.Int64Counter
	cntUpdated metric.Int64Counter
	cntDeleted metric.Int64Counter
	cntConflicts metric.Int64Counter
	cntErrors  metric.Int64Counter
}

// NewEngine creates an Engine. If haConn is nil, WebSocket subscriptions are
// skipped and the engine runs polling-only.
func NewEngine(reconciler *Reconciler, haConn HAConnector, listMappings map[string]string, pollInterval time.Duration, logger *slog.Logger) *Engine {
	tracer := otel.Tracer(otelScope)
	meter := otel.Meter(otelScope)

	mustCounter := func(name, desc string) metric.Int64Counter {
		c, err := meter.Int64Counter(name, metric.WithDescription(desc))
		if err != nil {
			logger.Error("creating OTel counter", "name", name, "error", err)
			return noop.Int64Counter{}
		}
		return c
	}

	return &Engine{
		reconciler:   reconciler,
		haConn:       haConn,
		listMappings: listMappings,
		pollInterval: pollInterval,
		log:          logger,

		tracer:       tracer,
		cntCreated:   mustCounter(metricCreated, "Number of items created during sync"),
		cntUpdated:   mustCounter(metricUpdated, "Number of items updated during sync"),
		cntDeleted:   mustCounter(metricDeleted, "Number of items deleted during sync"),
		cntConflicts: mustCounter(metricConflicts, "Number of conflict resolutions during sync"),
		cntErrors:    mustCounter(metricErrors, "Number of errors encountered during sync"),
	}
}

// reconcile runs one full reconcile pass, recording a trace span and metrics.
func (e *Engine) reconcile(ctx context.Context) (Stats, error) {
	ctx, span := e.tracer.Start(ctx, spanReconcile)
	defer span.End()

	stats, err := e.reconciler.Run(ctx, e.listMappings)

	// Record counters — these are always safe even if the span is a no-op.
	if stats.Created > 0 {
		e.cntCreated.Add(ctx, int64(stats.Created))
	}
	if stats.Updated > 0 {
		e.cntUpdated.Add(ctx, int64(stats.Updated))
	}
	if stats.Deleted > 0 {
		e.cntDeleted.Add(ctx, int64(stats.Deleted))
	}
	if stats.Conflicts > 0 {
		e.cntConflicts.Add(ctx, int64(stats.Conflicts))
	}
	if stats.Errors > 0 {
		e.cntErrors.Add(ctx, int64(stats.Errors))
	}

	span.SetAttributes(
		attribute.Int("sync.created", stats.Created),
		attribute.Int("sync.updated", stats.Updated),
		attribute.Int("sync.deleted", stats.Deleted),
		attribute.Int("sync.conflicts", stats.Conflicts),
		attribute.Int("sync.errors", stats.Errors),
	)
	if err != nil {
		span.RecordError(err)
	}
	return stats, err
}

// RunOnce performs a single reconciliation pass and returns.
func (e *Engine) RunOnce(ctx context.Context) (Stats, error) {
	return e.reconcile(ctx)
}

// Run starts the polling loop and optional WebSocket listener. It blocks until
// ctx is cancelled.
func (e *Engine) Run(ctx context.Context) error {
	// Start WS listener if available.
	if e.haConn != nil {
		if err := e.haConn.Connect(ctx); err != nil {
			e.log.Error("WebSocket connection failed, falling back to polling-only", "error", err)
		} else {
			defer func() { _ = e.haConn.Close() }()

			entityIDs := make([]string, 0, len(e.listMappings))
			for _, id := range e.listMappings {
				entityIDs = append(entityIDs, id)
			}

			// Build reverse mapping: entityID → listName.
			entityToList := make(map[string]string, len(e.listMappings))
			for listName, entityID := range e.listMappings {
				entityToList[entityID] = listName
			}

			go func() {
				err := e.haConn.SubscribeChanges(ctx, entityIDs, func(entityID string) {
					listName, ok := entityToList[entityID]
					if !ok {
						return
					}
					e.log.Info("WS event triggered reconcile", "entity_id", entityID)
					if _, err := e.reconciler.ReconcileEntity(ctx, listName, entityID); err != nil {
						e.log.Error("WS-triggered reconcile failed", "entity_id", entityID, "error", err)
					}
				})
				if err != nil && ctx.Err() == nil {
					e.log.Error("WS subscription ended unexpectedly", "error", err)
				}
			}()
		}
	}

	// Polling loop.
	ticker := time.NewTicker(e.pollInterval)
	defer ticker.Stop()

	// Run an immediate first pass.
	if _, err := e.reconcile(ctx); err != nil {
		e.log.Error("initial reconcile failed", "error", err)
	}

	for {
		select {
		case <-ctx.Done():
			e.log.Info("sync engine shutting down")
			return ctx.Err()
		case <-ticker.C:
			if _, err := e.reconcile(ctx); err != nil {
				e.log.Error("reconcile failed", "error", err)
			}
		}
	}
}
