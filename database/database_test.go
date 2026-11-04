package database

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

func TestSanitizeStripsLiteralsAndKeepsStructure(t *testing.T) {
	for in, want := range map[string]string{
		"SELECT * FROM users WHERE email = 'a@b.com' AND id = 42":             "SELECT * FROM users WHERE email = ? AND id = ?",
		"UPDATE t SET note = 'it''s here' WHERE id = 7":                       "UPDATE t SET note = ? WHERE id = ?",
		`SELECT "Order".total FROM "Order" WHERE price > 9.99 LIMIT 10`:       `SELECT "Order".total FROM "Order" WHERE price > ? LIMIT ?`,
		"SELECT reserve_stock($1, $2)":                                        "SELECT reserve_stock($1, $2)",
		"SELECT * FROM t2 WHERE c = :name OR c = ?":                           "SELECT * FROM t2 WHERE c = :name OR c = ?",
		"SELECT 1 -- secret token\nFROM t /* user 99 */ WHERE x IN (1, 2, 3)": "SELECT ? FROM t WHERE x IN (?, ?, ?)",
		"INSERT INTO t VALUES (0x1F, -5, 1e3)":                                "INSERT INTO t VALUES (?, -?, ?)",
		"SELECT 'naïve ☃' FROM ünï":                                           "SELECT ? FROM ünï",
	} {
		assert.Equal(t, want, SanitizeQuery(in, "postgresql"), in)
	}
}

func TestSanitizeMySQL(t *testing.T) {
	for in, want := range map[string]string{
		// Without backslash escapes, \' would close the string and expose secret.
		`SELECT * FROM u WHERE pw = 'a\' OR name = secret' AND id = 1`: "SELECT * FROM u WHERE pw = ?",
		`SELECT * FROM u WHERE email = "jo@example.com"`:               "SELECT * FROM u WHERE email = ?",
		"SELECT `order`.id FROM `order`":                               "SELECT `order`.id FROM `order`",
		"SELECT 1 # token\nFROM t":                                     "SELECT ? FROM t",
	} {
		assert.Equal(t, want, SanitizeQuery(in, "mysql"), in)
	}
}

func TestSanitizePostgres(t *testing.T) {
	for in, want := range map[string]string{
		"SELECT E'plain' AS note":                                    "SELECT ? AS note",
		"SELECT $$ssn 123-45-6789$$, $tag$card 4111 'x'$tag$ FROM t": "SELECT ?, ? FROM t",
		"SELECT a$b FROM t WHERE id = $1":                            "SELECT a$b FROM t WHERE id = $1",
	} {
		assert.Equal(t, want, SanitizeQuery(in, "postgresql"), in)
	}
}

func TestSanitizeUnknownDialectsTreatDoubleQuotesAsStrings(t *testing.T) {
	for _, system := range []string{"", "sqlite"} {
		assert.Equal(t, "SELECT * FROM u WHERE email = ?",
			SanitizeQuery(`SELECT * FROM u WHERE email = "jo@example.com"`, system))
	}
}

func TestSanitizeFailsClosed(t *testing.T) {
	for _, tc := range [][2]string{
		{"postgresql", "SELECT * FROM t WHERE a = 'never closed secret"},
		{"postgresql", "SELECT $$ never closed secret"},
		{"postgresql", `SELECT "never closed secret`},
		{"mysql", `SELECT * FROM t WHERE a = 'ends in backslash secret\'`},
		{"mysql", "SELECT `never closed secret"},
	} {
		got := SanitizeQuery(tc[1], tc[0])
		assert.NotContains(t, got, "secret", tc[1])
		assert.True(t, strings.HasSuffix(got, "?"), got)
	}
}

func TestSanitizeUnterminatedBlockCommentHidesTheRest(t *testing.T) {
	assert.Equal(t, "SELECT ?", SanitizeQuery("SELECT /* secret", "postgresql"))
}

// Each of these once printed secret1, because a dialect read the text differently
// from the tokenizer and left it out of step.
func TestSanitizeDoesNotLeakOnDialectMismatch(t *testing.T) {
	for _, tc := range [][2]string{
		{"MySQL", `SELECT 'a\' OR name = secret1'`},
		{"PostgreSQL", `SELECT E'it\'s secret1'`},
		{"cockroachdb", `SELECT E'it\'s secret1'`},
		{"aws.redshift", `SELECT E'it\'s secret1'`},
		{"tidb", `SELECT 'it\'s secret1'`},
		{"clickhouse", `SELECT 'it\'s secret1'`},
		{"gcp.spanner", `SELECT 'it\'s secret1'`},
		{"", `SELECT 'it\'s secret1'`},
		{"oracle", `SELECT q'[it's secret1]' FROM t`},
		{"oracle", `SELECT Nq'[it's secret1]' FROM t`},
		{"postgresql", `SELECT $é$secret1$é$`},
		{"postgresql", `SELECT 4111_1111_1111_1111 secret1`[:len("SELECT 4111_1111_1111_1111")]},
		{"postgresql", `/* /* */ it's */ SELECT 'secret1'`},
		{"mssql", `/* /* */ it's */ SELECT 'secret1'`},
		{"postgresql", "SELECT E'a'\n'b\\'c secret1'"},
		{"mysql", "SELECT 5--1, 'line1\nsecret1 line2'"},
		{"mysql", `SELECT 'a\', 'secret1', 'b'`},
		{"mssql", `SELECT * FROM t WHERE salary > $120000`},
		{"mssql", `SELECT [O'Brien], 'secret1', 'z'`},
		{"sqlite", `SELECT [O'Brien], 'secret1', 'z'`},
		{"sqlite", "SELECT `it's`, 'secret1', 'z'"},
		{"mssql", `SELECT * FROM t WHERE name = "secret1"`},
	} {
		got := SanitizeQuery(tc[1], tc[0])
		for _, leak := range []string{"secret1", "1111", "120000"} {
			assert.NotContains(t, got, leak, "%s: %s", tc[0], tc[1])
		}
	}
	assert.Equal(t, "a[?:?]", SanitizeQuery("a[1:2345]", "postgresql"))
}

func FuzzSanitizeHidesQuotedText(f *testing.F) {
	for _, seed := range []string{"", "\\", "$$", `"`, "--", "/*", "E", "$a$", "`", "#", "1e", "[", "q", "\n"} {
		f.Add(seed)
	}
	systems := []string{"", "postgresql", "mysql", "mssql", "oracle", "sqlite", "clickhouse"}
	f.Fuzz(func(t *testing.T, text string) {
		// With no ' in it, this is one string in every dialect. A backslash can
		// only make the sanitizer stop sooner.
		text = strings.ReplaceAll(text, "'", "")
		for _, system := range systems {
			got := SanitizeQuery("SELECT '"+text+"XSECRETX', id FROM t", system)
			if strings.Contains(got, "XSECRETX") {
				t.Fatalf("%s leaked: %q", system, got)
			}
			SanitizeQuery(text, system) // arbitrary input must not panic
		}
	})
}

func setup(t *testing.T) (*tracetest.SpanRecorder, *sdkmetric.ManualReader) {
	t.Helper()
	recorder := tracetest.NewSpanRecorder()
	reader := sdkmetric.NewManualReader()
	previousTP, previousMP := otel.GetTracerProvider(), otel.GetMeterProvider()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder)))
	otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))
	t.Cleanup(func() {
		otel.SetTracerProvider(previousTP)
		otel.SetMeterProvider(previousMP)
	})
	return recorder, reader
}

func attrs(span sdktrace.ReadOnlySpan) map[attribute.Key]string {
	out := map[attribute.Key]string{}
	for _, kv := range span.Attributes() {
		out[kv.Key] = kv.Value.String()
	}
	return out
}

func collect(t *testing.T, reader *sdkmetric.ManualReader) map[string]metricdata.Metrics {
	t.Helper()
	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))
	out := map[string]metricdata.Metrics{}
	for _, scope := range rm.ScopeMetrics {
		for _, m := range scope.Metrics {
			out[m.Name] = m
		}
	}
	return out
}

var users = Call{System: "mysql", Operation: "SELECT", Namespace: "shop", Collection: "users"}

func TestTraceRecordsSpanAndDuration(t *testing.T) {
	recorder, reader := setup(t)
	call := users
	call.Query = `SELECT id FROM users WHERE email = "jo@example.com"`

	var inner trace.SpanContext
	require.NoError(t, Trace(context.Background(), call, func(ctx context.Context) error {
		inner = trace.SpanContextFromContext(ctx)
		return nil
	}))

	span := recorder.Ended()[0]
	assert.Equal(t, "SELECT users", span.Name())
	assert.Equal(t, trace.SpanKindClient, span.SpanKind())
	assert.Equal(t, inner.SpanID(), span.SpanContext().SpanID())
	got := attrs(span)
	assert.Equal(t, "SELECT id FROM users WHERE email = ?", got["db.query.text"])
	assert.Equal(t, "mysql", got["db.system.name"])
	assert.Equal(t, "shop", got["db.namespace"])

	histogram := collect(t, reader)["db.client.operation.duration"].Data.(metricdata.Histogram[float64])
	require.Len(t, histogram.DataPoints, 1)
	_, hasQuery := histogram.DataPoints[0].Attributes.Value("db.query.text")
	assert.False(t, hasQuery, "query text must stay off metrics")
}

func TestTraceRecordsFailure(t *testing.T) {
	recorder, reader := setup(t)
	failure := errors.New("deadlock")
	require.ErrorIs(t, Trace(context.Background(), users, func(context.Context) error { return failure }), failure)

	span := recorder.Ended()[0]
	assert.Equal(t, codes.Error, span.Status().Code)
	assert.NotContains(t, span.Status().Description, "deadlock", "drivers quote values in error text")
	assert.Empty(t, span.Events())
	assert.Equal(t, "*errors.errorString", attrs(span)["error.type"])
	histogram := collect(t, reader)["db.client.operation.duration"].Data.(metricdata.Histogram[float64])
	errorType, _ := histogram.DataPoints[0].Attributes.Value("error.type")
	assert.Equal(t, "*errors.errorString", errorType.AsString())
}

// fakeDriver is the least a *sql.DB needs, counting what a transaction does.
type fakeDriver struct {
	commits, rollbacks               atomic.Int64
	commitErr, beginErr, rollbackErr error
}

func (d *fakeDriver) Connect(context.Context) (driver.Conn, error) { return &fakeConn{d}, nil }
func (d *fakeDriver) Driver() driver.Driver                        { return d }
func (d *fakeDriver) Open(string) (driver.Conn, error)             { return &fakeConn{d}, nil }

type fakeConn struct{ d *fakeDriver }

func (c *fakeConn) Prepare(string) (driver.Stmt, error) { return nil, io.ErrUnexpectedEOF }
func (c *fakeConn) Close() error                        { return nil }
func (c *fakeConn) Begin() (driver.Tx, error) {
	if c.d.beginErr != nil {
		return nil, c.d.beginErr
	}
	return &fakeTx{c.d}, nil
}

type fakeTx struct{ d *fakeDriver }

func (t *fakeTx) Commit() error {
	t.d.commits.Add(1)
	return t.d.commitErr
}

func (t *fakeTx) Rollback() error {
	t.d.rollbacks.Add(1)
	return t.d.rollbackErr
}

func open(t *testing.T) (*sql.DB, *fakeDriver) {
	t.Helper()
	d := &fakeDriver{}
	db := sql.OpenDB(d)
	t.Cleanup(func() { _ = db.Close() })
	return db, d
}

var checkout = Call{System: "postgresql", Namespace: "shop", Collection: "checkout"}

func noop(context.Context, *sql.Tx) error { return nil }

func TestWithTransactionCommits(t *testing.T) {
	recorder, _ := setup(t)
	db, d := open(t)
	require.NoError(t, WithTransaction(context.Background(), db, checkout, noop))

	assert.EqualValues(t, 1, d.commits.Load())
	assert.Len(t, recorder.Ended(), 1)
	assert.Equal(t, "transaction checkout", recorder.Ended()[0].Name())
	assert.Equal(t, "commit", attrs(recorder.Ended()[0])["db.transaction.outcome"])
}

func TestWithTransactionRollsBackOnError(t *testing.T) {
	recorder, _ := setup(t)
	db, d := open(t)
	failure := errors.New("no stock")
	err := WithTransaction(context.Background(), db, checkout, func(context.Context, *sql.Tx) error { return failure })

	require.ErrorIs(t, err, failure)
	assert.EqualValues(t, 1, d.rollbacks.Load())
	assert.Zero(t, d.commits.Load())
	assert.Equal(t, "rollback", attrs(recorder.Ended()[0])["db.transaction.outcome"])
	assert.Equal(t, codes.Error, recorder.Ended()[0].Status().Code)
}

func TestWithTransactionReportsCommitFailure(t *testing.T) {
	recorder, _ := setup(t)
	db, d := open(t)
	d.commitErr = errors.New("serialization failure")

	require.ErrorIs(t, WithTransaction(context.Background(), db, checkout, noop), d.commitErr)
	assert.Equal(t, "commit_failed", attrs(recorder.Ended()[0])["db.transaction.outcome"])
}

func TestWithTransactionReportsBeginFailure(t *testing.T) {
	recorder, _ := setup(t)
	db, d := open(t)
	d.beginErr = errors.New("too many connections")

	require.ErrorIs(t, WithTransaction(context.Background(), db, checkout, noop), d.beginErr)
	assert.Len(t, recorder.Ended(), 1)
	assert.Equal(t, codes.Error, recorder.Ended()[0].Status().Code)
}

func TestWithTransactionRollsBackAndRepanics(t *testing.T) {
	recorder, _ := setup(t)
	db, d := open(t)

	assert.PanicsWithValue(t, "boom", func() {
		_ = WithTransaction(context.Background(), db, checkout, func(context.Context, *sql.Tx) error { panic("boom") })
	})
	assert.EqualValues(t, 1, d.rollbacks.Load())
	assert.Len(t, recorder.Ended(), 1)
	assert.Equal(t, codes.Error, recorder.Ended()[0].Status().Code)
	assert.Equal(t, "rollback", attrs(recorder.Ended()[0])["db.transaction.outcome"])
}

func TestRegisterPoolMetrics(t *testing.T) {
	_, reader := setup(t)
	db, _ := open(t)
	db.SetMaxOpenConns(4)
	conn, err := db.Conn(context.Background())
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()

	registration, err := RegisterPoolMetrics(db, "main")
	require.NoError(t, err)
	metrics := collect(t, reader)

	counts := map[string]int64{}
	for _, point := range metrics["db.client.connection.count"].Data.(metricdata.Sum[int64]).DataPoints {
		state, _ := point.Attributes.Value("db.client.connection.state")
		pool, _ := point.Attributes.Value("db.client.connection.pool.name")
		assert.Equal(t, "main", pool.AsString())
		counts[state.AsString()] = point.Value
	}
	assert.Equal(t, map[string]int64{"used": 1, "idle": 0}, counts)
	assert.Equal(t, int64(4), metrics["db.client.connection.max"].Data.(metricdata.Sum[int64]).DataPoints[0].Value)
	assert.InDelta(t, 0.25, metrics["db.client.connection.utilization"].Data.(metricdata.Gauge[float64]).DataPoints[0].Value, 0.001)
	assert.Contains(t, metrics, "autotel.db.pool.wait.count")

	require.NoError(t, registration.Unregister())
	assert.NotContains(t, collect(t, reader), "db.client.connection.count")
}

func TestPoolWithoutALimitReportsNoUtilization(t *testing.T) {
	_, reader := setup(t)
	db, _ := open(t)
	_, err := RegisterPoolMetrics(db, "main")
	require.NoError(t, err)
	assert.NotContains(t, collect(t, reader), "db.client.connection.utilization")
}

func TestTraceEndsTheSpanOnPanic(t *testing.T) {
	recorder, reader := setup(t)
	assert.PanicsWithValue(t, "boom", func() {
		_ = Trace(context.Background(), users, func(context.Context) error { panic("boom") })
	})
	require.Len(t, recorder.Ended(), 1)
	assert.Equal(t, codes.Error, recorder.Ended()[0].Status().Code)
	assert.Contains(t, collect(t, reader), "db.client.operation.duration")
}

func TestWithTransactionRollsBackOnGoexit(t *testing.T) {
	recorder, _ := setup(t)
	db, d := open(t)
	db.SetMaxOpenConns(1)

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = WithTransaction(context.Background(), db, checkout, func(context.Context, *sql.Tx) error {
			runtime.Goexit()
			return nil
		})
	}()
	<-done

	assert.EqualValues(t, 1, d.rollbacks.Load())
	assert.Zero(t, db.Stats().InUse, "the connection must go back to the pool")
	assert.Equal(t, codes.Error, recorder.Ended()[0].Status().Code)
}

func TestWithTransactionCancelledContextReportsRollback(t *testing.T) {
	recorder, _ := setup(t)
	db, _ := open(t)
	ctx, cancel := context.WithCancel(context.Background())

	err := WithTransaction(ctx, db, checkout, func(context.Context, *sql.Tx) error {
		cancel()
		return nil
	})
	require.Error(t, err)
	assert.Equal(t, "rollback", attrs(recorder.Ended()[0])["db.transaction.outcome"])
}

// When ctx ends, database/sql has already rolled back: that is no rollback failure.
func TestAlreadyRolledBackIsNotAFailedRollback(t *testing.T) {
	recorder, _ := setup(t)
	db, _ := open(t)
	ctx, cancel := context.WithCancel(context.Background())
	failure := errors.New("gave up")

	err := WithTransaction(ctx, db, checkout, func(_ context.Context, tx *sql.Tx) error {
		cancel()
		_ = tx.Rollback() // done either way, so WithTransaction's own gets ErrTxDone
		return failure
	})
	require.ErrorIs(t, err, failure)
	assert.Empty(t, recorder.Ended()[0].Events())
}

func TestWithTransactionKeepsTheCausingErrorWhenRollbackFails(t *testing.T) {
	recorder, _ := setup(t)
	db, d := open(t)
	d.rollbackErr = errors.New("connection lost")
	failure := errors.New("no stock")

	err := WithTransaction(context.Background(), db, checkout, func(context.Context, *sql.Tx) error { return failure })
	require.ErrorIs(t, err, failure)
	require.Len(t, recorder.Ended()[0].Events(), 1)
	assert.Equal(t, "db.transaction.rollback_error", recorder.Ended()[0].Events()[0].Name)
}
