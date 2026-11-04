package genai

import (
	"context"
	"errors"
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

func attrs(span sdktrace.ReadOnlySpan) map[attribute.Key]attribute.Value {
	out := map[attribute.Key]attribute.Value{}
	for _, kv := range span.Attributes() {
		out[kv.Key] = kv.Value
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

func TestTraceRecordsSpanAndMetrics(t *testing.T) {
	recorder, reader := setup(t)
	zero := 0.0
	call := Chat("anthropic", "claude-sonnet-5-5")
	call.MaxTokens = 1024
	call.Temperature = &zero
	call.OutputType = "text"
	call.ServerAddress = "api.anthropic.com"
	call.ServerPort = 443

	reply, err := Trace(context.Background(), call, func(context.Context) (string, Usage, error) {
		return "hi", Usage{InputTokens: 12, OutputTokens: 3, ResponseModel: "claude-sonnet-5-5-1", FinishReasons: []string{"stop"}}, nil
	})
	require.NoError(t, err)
	assert.Equal(t, "hi", reply)

	span := recorder.Ended()[0]
	assert.Equal(t, "chat claude-sonnet-5-5", span.Name())
	assert.Equal(t, trace.SpanKindClient, span.SpanKind())
	got := attrs(span)
	assert.Equal(t, "anthropic", got["gen_ai.provider.name"].AsString())
	assert.Equal(t, int64(1024), got["gen_ai.request.max_tokens"].AsInt64())
	assert.Equal(t, attribute.FLOAT64, got["gen_ai.request.temperature"].Type(), "0 is a real temperature")
	assert.Equal(t, "text", got["gen_ai.output.type"].AsString())
	assert.Equal(t, int64(443), got["server.port"].AsInt64())
	assert.Equal(t, int64(12), got["gen_ai.usage.input_tokens"].AsInt64())
	assert.Equal(t, []string{"stop"}, got["gen_ai.response.finish_reasons"].AsStringSlice())

	metrics := collect(t, reader)
	tokens := map[string]int64{}
	for _, point := range metrics["gen_ai.client.token.usage"].Data.(metricdata.Histogram[int64]).DataPoints {
		kind, _ := point.Attributes.Value("gen_ai.token.type")
		tokens[kind.AsString()] = point.Sum
	}
	assert.Equal(t, map[string]int64{"input": 12, "output": 3}, tokens)
	assert.Equal(t, "s", metrics["gen_ai.client.operation.duration"].Unit)
	assert.Equal(t, int64(1), metrics["gen_ai.client.operation.count"].Data.(metricdata.Sum[int64]).DataPoints[0].Value)
}

func TestOptionalFieldsStayOff(t *testing.T) {
	recorder, _ := setup(t)
	_, err := Trace(context.Background(), GenerateContent("google", "gemini"), func(context.Context) (int, Usage, error) {
		return 0, Usage{}, nil
	})
	require.NoError(t, err)
	got := attrs(recorder.Ended()[0])
	assert.Equal(t, "generate_content", got["gen_ai.operation.name"].AsString())
	for _, key := range []attribute.Key{"gen_ai.request.temperature", "gen_ai.request.max_tokens", "server.address", "gen_ai.response.model"} {
		assert.NotContains(t, got, key)
	}
}

func TestTraceRecordsFailure(t *testing.T) {
	recorder, reader := setup(t)
	failure := errors.New("rate limited")
	_, err := Trace(context.Background(), Chat("openai", "gpt"), func(context.Context) (string, Usage, error) {
		return "", Usage{}, failure
	})
	require.ErrorIs(t, err, failure)

	span := recorder.Ended()[0]
	assert.Equal(t, codes.Error, span.Status().Code)
	assert.NotContains(t, attrs(span), attribute.Key("gen_ai.usage.input_tokens"))
	assert.NotContains(t, span.Status().Description, "rate limited", "providers quote the request in errors")

	metrics := collect(t, reader)
	assert.NotContains(t, metrics, "gen_ai.client.token.usage")
	point := metrics["gen_ai.client.operation.count"].Data.(metricdata.Sum[int64]).DataPoints[0]
	errorType, _ := point.Attributes.Value("error.type")
	assert.Equal(t, "*errors.errorString", errorType.AsString())
}

func TestRecordEvaluation(t *testing.T) {
	recorder, _ := setup(t)
	_, span := otel.Tracer("t").Start(context.Background(), "judge")
	RecordEvaluation(span, "relevance", 0.9, "good")
	span.End()
	got := attrs(recorder.Ended()[0])
	assert.Equal(t, "relevance", got["gen_ai.evaluation.name"].AsString())
	assert.InDelta(t, 0.9, got["gen_ai.evaluation.score.value"].AsFloat64(), 0.001)
	assert.Equal(t, "good", got["gen_ai.evaluation.score.label"].AsString())
}

func TestTraceRecordsPanic(t *testing.T) {
	recorder, reader := setup(t)
	assert.PanicsWithValue(t, "boom", func() {
		_, _ = Trace(context.Background(), Chat("openai", "gpt"), func(context.Context) (string, Usage, error) { panic("boom") })
	})
	require.Len(t, recorder.Ended(), 1)
	assert.Equal(t, codes.Error, recorder.Ended()[0].Status().Code)
	assert.Contains(t, collect(t, reader), "gen_ai.client.operation.count")
}
