# Databases and LLM calls

Wrap a database or LLM call and it appears in your trace with its timing, and with metrics you can graph. Neither package needs a driver wrapper or a particular client.

## Trace database calls

`database.Trace` is in the [README](../README.md#trace-database-and-llm-calls). It records a CLIENT span (a call your service makes to something else), the query as `db.query.text`, and the `db.client.operation.duration` histogram.

To trace a transaction, or report on your connection pool:

```go
err := database.WithTransaction(ctx, db, database.Call{
	System: "postgresql", Namespace: "shop", Collection: "checkout",
}, func(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, "UPDATE stock SET n = n - 1 WHERE id = $1", id)
	return err
})

registration, err := database.RegisterPoolMetrics(db, "main") // call after Init
```

`WithTransaction` commits when your function returns `nil`. It rolls back on an error, a panic or `runtime.Goexit`, and records the result in `db.transaction.outcome`. Call `registration.Unregister()` if the pool closes before the process does.

`SanitizeQuery` removes literal values before a query reaches the span. SQL dialects disagree about where a literal ends, so at anything they read differently (a backslash in a string, a quote in a comment, an Oracle `q'[…]'`) it hides the rest of the query. Those queries lose detail. This is best effort, so keep secrets in bind parameters.

A failed call records `error.type` and leaves the error's text off the span, because drivers quote values in it. Your code still gets the error.

To trace every statement at the driver instead, use [`otelsql`](https://github.com/XSAM/otelsql).

## Trace LLM calls

`genai.Trace` works with any client. It follows the OpenTelemetry GenAI conventions and never records prompts or completions.

```go
import "github.com/jagreehal/autotel-go/v2/genai"

reply, err := genai.Trace(ctx, genai.Chat("anthropic", model),
	func(ctx context.Context) (string, genai.Usage, error) {
		text, in, out, err := callModel(ctx)
		return text, genai.Usage{InputTokens: in, OutputTokens: out}, err
	})
```

Each call gets a CLIENT span named `chat <model>`, plus the `gen_ai.client.operation.duration`, `gen_ai.client.token.usage` and `gen_ai.client.operation.count` metrics. A failed call records `error.type` only, because providers quote the request in error text. To put a quality score on a span, call `genai.RecordEvaluation`.
