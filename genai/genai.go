// Package genai records LLM calls with the OpenTelemetry GenAI semantic
// conventions. It works with any client: you wrap the call and report the usage
// the provider returned. Prompts and completions are never recorded.
package genai

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

const instrumentationName = "github.com/jagreehal/autotel-go/v2/genai"

// Call describes one GenAI operation. Fields left zero stay off the span.
// Temperature is a pointer because 0 is a setting people use.
type Call struct {
	Provider      string // gen_ai.provider.name, such as anthropic
	Operation     string // gen_ai.operation.name, such as chat
	Model         string // gen_ai.request.model
	MaxTokens     int64
	Temperature   *float64
	OutputType    string // gen_ai.output.type, such as text or json
	ServerAddress string
	ServerPort    int
}

// Chat describes a chat completion.
func Chat(provider, model string) Call {
	return Call{Provider: provider, Operation: "chat", Model: model}
}

// GenerateContent describes a generate_content call.
func GenerateContent(provider, model string) Call {
	return Call{Provider: provider, Operation: "generate_content", Model: model}
}

func (c Call) base() []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.String("gen_ai.operation.name", c.Operation),
		attribute.String("gen_ai.provider.name", c.Provider),
		attribute.String("gen_ai.request.model", c.Model),
	}
}

func (c Call) attributes() []attribute.KeyValue {
	attrs := c.base()
	if c.MaxTokens > 0 {
		attrs = append(attrs, attribute.Int64("gen_ai.request.max_tokens", c.MaxTokens))
	}
	if c.Temperature != nil {
		attrs = append(attrs, attribute.Float64("gen_ai.request.temperature", *c.Temperature))
	}
	if c.OutputType != "" {
		attrs = append(attrs, attribute.String("gen_ai.output.type", c.OutputType))
	}
	if c.ServerAddress != "" {
		attrs = append(attrs, attribute.String("server.address", c.ServerAddress))
	}
	if c.ServerPort > 0 {
		attrs = append(attrs, attribute.Int("server.port", c.ServerPort))
	}
	return attrs
}

// Usage is what a provider reports about a finished call.
type Usage struct {
	InputTokens   int64
	OutputTokens  int64
	ResponseModel string
	// FinishReasons has one entry per choice, such as "stop".
	FinishReasons []string
}

// Trace runs fn inside a CLIENT span named "chat claude-sonnet-5-5" and records
// the duration, token and count metrics. Return the provider's usage from fn.
// An error from fn marks the span as failed and is returned as is.
//
//	reply, err := genai.Trace(ctx, genai.Chat("anthropic", model),
//	    func(ctx context.Context) (Reply, genai.Usage, error) {
//	        msg, err := client.Messages.New(ctx, params)
//	        if err != nil {
//	            return Reply{}, genai.Usage{}, err
//	        }
//	        return toReply(msg), genai.Usage{
//	            InputTokens:  msg.Usage.InputTokens,
//	            OutputTokens: msg.Usage.OutputTokens,
//	        }, nil
//	    })
func Trace[T any](ctx context.Context, call Call, fn func(ctx context.Context) (T, Usage, error)) (T, error) {
	ctx, span := otel.Tracer(instrumentationName).Start(ctx, call.Operation+" "+call.Model,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(call.attributes()...),
	)
	defer span.End()

	started := time.Now()
	returned := false
	defer func() {
		if !returned {
			fail(ctx, span, call, time.Since(started), "panic")
		}
	}()

	result, usage, err := fn(ctx)
	returned = true
	elapsed := time.Since(started)

	if err != nil {
		fail(ctx, span, call, elapsed, fmt.Sprintf("%T", err))
		return result, err
	}

	RecordUsage(span, usage)
	recordSuccess(ctx, call, usage, elapsed)
	return result, nil
}

// fail records only the error's type: providers quote the request in validation
// errors, and prompts stay off spans. The caller still has the error itself.
func fail(ctx context.Context, span trace.Span, call Call, elapsed time.Duration, errorType string) {
	span.SetAttributes(attribute.String("error.type", errorType))
	span.SetStatus(codes.Error, errorType)
	recordFailure(ctx, call, elapsed, errorType)
}

// RecordUsage sets the response attributes on span. Trace calls it for you.
func RecordUsage(span trace.Span, usage Usage) {
	span.SetAttributes(
		attribute.Int64("gen_ai.usage.input_tokens", usage.InputTokens),
		attribute.Int64("gen_ai.usage.output_tokens", usage.OutputTokens),
	)
	if usage.ResponseModel != "" {
		span.SetAttributes(attribute.String("gen_ai.response.model", usage.ResponseModel))
	}
	if len(usage.FinishReasons) > 0 {
		span.SetAttributes(attribute.StringSlice("gen_ai.response.finish_reasons", usage.FinishReasons))
	}
}

// RecordEvaluation sets an evaluation score on span.
func RecordEvaluation(span trace.Span, name string, score float64, label string) {
	span.SetAttributes(
		attribute.String("gen_ai.evaluation.name", name),
		attribute.Float64("gen_ai.evaluation.score.value", score),
		attribute.String("gen_ai.evaluation.score.label", label),
	)
}

// instruments are looked up per call: the SDK caches them, and package-level
// ones would keep writing to the provider of an earlier Init.
type instruments struct {
	duration metric.Float64Histogram
	tokens   metric.Int64Histogram
	count    metric.Int64Counter
}

func lookupInstruments() (instruments, error) {
	meter := otel.Meter(instrumentationName)
	// Boundaries the GenAI conventions advise. The SDK defaults suit milliseconds
	// and would put every call in one bucket.
	duration, err := meter.Float64Histogram("gen_ai.client.operation.duration",
		metric.WithUnit("s"),
		metric.WithDescription("GenAI operation duration"),
		metric.WithExplicitBucketBoundaries(0.01, 0.02, 0.04, 0.08, 0.16, 0.32, 0.64, 1.28, 2.56, 5.12, 10.24, 20.48, 40.96, 81.92),
	)
	if err != nil {
		return instruments{}, err
	}
	tokens, err := meter.Int64Histogram("gen_ai.client.token.usage",
		metric.WithUnit("{token}"),
		metric.WithDescription("Number of tokens processed"),
		metric.WithExplicitBucketBoundaries(1, 4, 16, 64, 256, 1024, 4096, 16384, 65536, 262144, 1048576, 4194304, 16777216, 67108864),
	)
	if err != nil {
		return instruments{}, err
	}
	count, err := meter.Int64Counter("gen_ai.client.operation.count",
		metric.WithDescription("Number of GenAI operations"))
	if err != nil {
		return instruments{}, err
	}
	return instruments{duration, tokens, count}, nil
}

func recordSuccess(ctx context.Context, call Call, usage Usage, elapsed time.Duration) {
	in, err := lookupInstruments()
	if err != nil {
		otel.Handle(err)
		return
	}
	base := metric.WithAttributes(call.base()...)
	in.duration.Record(ctx, elapsed.Seconds(), base)
	in.count.Add(ctx, 1, base)
	in.tokens.Record(ctx, usage.InputTokens, base, metric.WithAttributes(attribute.String("gen_ai.token.type", "input")))
	in.tokens.Record(ctx, usage.OutputTokens, base, metric.WithAttributes(attribute.String("gen_ai.token.type", "output")))
}

func recordFailure(ctx context.Context, call Call, elapsed time.Duration, errorType string) {
	in, err := lookupInstruments()
	if err != nil {
		otel.Handle(err)
		return
	}
	attrs := metric.WithAttributes(append(call.base(), attribute.String("error.type", errorType))...)
	in.duration.Record(ctx, elapsed.Seconds(), attrs)
	in.count.Add(ctx, 1, attrs)
}
