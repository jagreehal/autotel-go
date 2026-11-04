// Package database traces database calls made through database/sql or any other
// client, following the OpenTelemetry database conventions.
//
// It adds no driver wrapper and no dependency: you say where a call starts and
// ends. To instrument every statement at the driver instead, use a wrapper such
// as github.com/XSAM/otelsql, and keep WithTransaction and RegisterPoolMetrics
// from here if they help.
package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

const instrumentationName = "github.com/jagreehal/autotel-go/v2/database"

// Call describes one database operation.
type Call struct {
	// System is the db.system.name value: postgresql, mysql, sqlite and so on. It
	// also picks the dialect SanitizeQuery uses.
	System string
	// Operation is the db.operation.name value, such as SELECT.
	Operation string
	// Namespace is the database name.
	Namespace string
	// Collection is the table, such as users.
	Collection string
	// Query is recorded as db.query.text with its literals removed.
	Query string
}

func (c Call) spanName() string {
	name := strings.TrimSpace(c.Operation + " " + c.Collection)
	if name == "" {
		return c.System
	}
	return name
}

// metricAttrs are bounded by the application's own schema. Query text isn't one
// of them, so it stays on the span.
func (c Call) metricAttrs() []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.String("db.system.name", c.System),
		attribute.String("db.namespace", c.Namespace),
		attribute.String("db.operation.name", c.Operation),
		attribute.String("db.collection.name", c.Collection),
	}
}

// Trace runs fn inside a CLIENT span and records db.client.operation.duration.
// An error from fn marks the span as failed and is returned as is.
//
//	err := database.Trace(ctx, database.Call{
//	    System: "postgresql", Operation: "SELECT", Namespace: "shop", Collection: "users",
//	    Query: query,
//	}, func(ctx context.Context) error {
//	    return db.QueryRowContext(ctx, query, id).Scan(&user.Name)
//	})
func Trace(ctx context.Context, call Call, fn func(ctx context.Context) error) (err error) {
	ctx, finish := start(ctx, call)
	returned := false
	defer func() {
		if !returned {
			err = errAbandoned
		}
		finish(err)
	}()
	err = fn(ctx)
	returned = true
	return err
}

// errAbandoned stands in when fn never returned: it panicked or called
// runtime.Goexit, and either way carries on past the deferred call that sees it.
var errAbandoned = errors.New("abandoned: panic or Goexit")

// start opens the span. Calling the returned function records the outcome.
//
// The text of an error stays off the span, because drivers quote values in it:
// MySQL's "Duplicate entry 'jo@example.com'" would undo SanitizeQuery. Its type
// goes on as error.type; the caller still has the error itself.
func start(ctx context.Context, call Call) (context.Context, func(error)) {
	attrs := call.metricAttrs()
	if call.Query != "" {
		attrs = append(attrs, attribute.String("db.query.text", SanitizeQuery(call.Query, call.System)))
	}
	ctx, span := otel.Tracer(instrumentationName).Start(ctx, call.spanName(),
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(attrs...),
	)
	started := time.Now()

	return ctx, func(err error) {
		metricAttrs := call.metricAttrs()
		if err != nil {
			errorType := fmt.Sprintf("%T", err)
			metricAttrs = append(metricAttrs, attribute.String("error.type", errorType))
			span.SetAttributes(attribute.String("error.type", errorType))
			span.SetStatus(codes.Error, errorType)
		}
		recordDuration(ctx, time.Since(started), metricAttrs)
		span.End()
	}
}

func recordDuration(ctx context.Context, elapsed time.Duration, attrs []attribute.KeyValue) {
	// Looked up per call: the SDK caches instruments, and a package-level one
	// would keep writing to the provider of an earlier Init.
	histogram, err := otel.Meter(instrumentationName).Float64Histogram("db.client.operation.duration",
		metric.WithUnit("s"),
		metric.WithDescription("Duration of database client operations"),
		metric.WithExplicitBucketBoundaries(0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1, 5, 10),
	)
	if err != nil {
		otel.Handle(err)
		return
	}
	histogram.Record(ctx, elapsed.Seconds(), metric.WithAttributes(attrs...))
}

// WithTransaction runs fn in a transaction inside one span. It commits when fn
// returns nil and rolls back otherwise, recording the result in
// db.transaction.outcome as commit, commit_failed or rollback. A panic or
// runtime.Goexit in fn rolls back too, and carries on.
//
//	err := database.WithTransaction(ctx, db, database.Call{
//	    System: "postgresql", Namespace: "shop", Collection: "checkout",
//	}, func(ctx context.Context, tx *sql.Tx) error {
//	    _, err := tx.ExecContext(ctx, "UPDATE stock SET n = n - 1 WHERE id = $1", id)
//	    return err
//	})
func WithTransaction(ctx context.Context, db *sql.DB, call Call, fn func(ctx context.Context, tx *sql.Tx) error) (err error) {
	if call.Operation == "" {
		call.Operation = "transaction"
	}
	ctx, finish := start(ctx, call)
	outcome := func(value string) {
		trace.SpanFromContext(ctx).SetAttributes(attribute.String("db.transaction.outcome", value))
	}

	// Not recover: a flag sees Goexit as well as panic, and leaves the stack alone.
	var tx *sql.Tx
	settled := false
	defer func() {
		if !settled {
			if tx != nil {
				rollback(ctx, tx)
				outcome("rollback")
			}
			err = errAbandoned
		}
		finish(err)
	}()

	tx, err = db.BeginTx(ctx, nil)
	if err != nil {
		settled = true
		return err
	}
	if err = fn(ctx, tx); err != nil {
		rollback(ctx, tx)
		outcome("rollback")
		settled = true
		return err
	}

	err = tx.Commit()
	settled = true
	switch {
	case err == nil:
		outcome("commit")
	case ctx.Err() != nil || errors.Is(err, sql.ErrTxDone):
		// database/sql already rolled back when ctx ended.
		outcome("rollback")
	default:
		outcome("commit_failed")
	}
	return err
}

// rollback keeps the error that caused it as the one the caller sees, and notes
// a failed rollback on the span.
func rollback(ctx context.Context, tx *sql.Tx) {
	if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
		trace.SpanFromContext(ctx).AddEvent("db.transaction.rollback_error",
			trace.WithAttributes(attribute.String("error.type", fmt.Sprintf("%T", err))))
	}
}

// RegisterPoolMetrics reports db's connection pool on every metric collection,
// tagged db.client.connection.pool.name so several pools can coexist:
//
//   - db.client.connection.count, with db.client.connection.state used or idle
//   - db.client.connection.max, when SetMaxOpenConns set one
//   - db.client.connection.utilization, used over max, from 0 to 1
//   - autotel.db.pool.wait.count and autotel.db.pool.wait.duration, the totals
//     database/sql keeps for callers that waited for a connection
//
// Call it after autotel.Init. Unregister the result if the pool closes before
// the process does.
func RegisterPoolMetrics(db *sql.DB, poolName string) (metric.Registration, error) {
	meter := otel.Meter(instrumentationName)
	pool := attribute.String("db.client.connection.pool.name", poolName)

	count, err := meter.Int64ObservableUpDownCounter("db.client.connection.count",
		metric.WithUnit("{connection}"),
		metric.WithDescription("Connections in the pool, by state"))
	if err != nil {
		return nil, err
	}
	limit, err := meter.Int64ObservableUpDownCounter("db.client.connection.max",
		metric.WithUnit("{connection}"),
		metric.WithDescription("Most open connections the pool allows"))
	if err != nil {
		return nil, err
	}
	utilization, err := meter.Float64ObservableGauge("db.client.connection.utilization",
		metric.WithUnit("1"),
		metric.WithDescription("Connections in use as a share of the maximum"))
	if err != nil {
		return nil, err
	}
	waitCount, err := meter.Int64ObservableCounter("autotel.db.pool.wait.count",
		metric.WithUnit("{wait}"),
		metric.WithDescription("Times a caller waited for a connection"))
	if err != nil {
		return nil, err
	}
	waitDuration, err := meter.Float64ObservableCounter("autotel.db.pool.wait.duration",
		metric.WithUnit("s"),
		metric.WithDescription("Total time callers waited for a connection"))
	if err != nil {
		return nil, err
	}

	used := metric.WithAttributes(pool, attribute.String("db.client.connection.state", "used"))
	idle := metric.WithAttributes(pool, attribute.String("db.client.connection.state", "idle"))
	poolOnly := metric.WithAttributes(pool)

	return meter.RegisterCallback(func(_ context.Context, o metric.Observer) error {
		stats := db.Stats()
		o.ObserveInt64(count, int64(stats.InUse), used)
		o.ObserveInt64(count, int64(stats.Idle), idle)
		o.ObserveInt64(waitCount, stats.WaitCount, poolOnly)
		o.ObserveFloat64(waitDuration, stats.WaitDuration.Seconds(), poolOnly)
		// Zero means unlimited, which has no meaningful ratio.
		if stats.MaxOpenConnections > 0 {
			o.ObserveInt64(limit, int64(stats.MaxOpenConnections), poolOnly)
			o.ObserveFloat64(utilization, float64(stats.InUse)/float64(stats.MaxOpenConnections), poolOnly)
		}
		return nil
	}, count, limit, utilization, waitCount, waitDuration)
}
