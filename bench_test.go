package autotel

import (
	"context"
	"testing"

	autoteltesting "github.com/jagreehal/autotel-go/v2/testing"
)

func BenchmarkStart(b *testing.B) {
	_, cleanup := autoteltesting.SetupTest(b)
	defer cleanup()

	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, span := Start(ctx, "benchmark")
		span.End()
	}
}

func BenchmarkTrace(b *testing.B) {
	_, cleanup := autoteltesting.SetupTest(b)
	defer cleanup()

	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, _ = Trace(ctx, "benchmark", func(ctx context.Context, span Span) (int, error) {
			return 42, nil
		})
	}
}

func BenchmarkSetAttribute(b *testing.B) {
	_, cleanup := autoteltesting.SetupTest(b)
	defer cleanup()

	ctx := context.Background()
	_, span := Start(ctx, "benchmark")
	defer span.End()

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		span.SetAttribute("key", "value")
	}
}

func BenchmarkSetAttribute_Int(b *testing.B) {
	_, cleanup := autoteltesting.SetupTest(b)
	defer cleanup()

	ctx := context.Background()
	_, span := Start(ctx, "benchmark")
	defer span.End()

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		span.SetAttribute("key", i)
	}
}
