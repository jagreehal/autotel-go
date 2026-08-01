package workflow

import (
	"context"
	"errors"
	"testing"

	"go.opentelemetry.io/otel/trace"
)

func TestWorkflow_SuccessfulExecution(t *testing.T) {
	ctx := context.Background()

	var executionOrder []string

	wf := New("test-workflow", ctx)
	wf.Step("step1", func(ctx context.Context, span trace.Span) error {
		executionOrder = append(executionOrder, "step1")
		return nil
	})
	wf.Step("step2", func(ctx context.Context, span trace.Span) error {
		executionOrder = append(executionOrder, "step2")
		return nil
	})
	wf.Step("step3", func(ctx context.Context, span trace.Span) error {
		executionOrder = append(executionOrder, "step3")
		return nil
	})

	err := wf.Run(ctx)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if wf.State() != StateCompleted {
		t.Errorf("expected state %s, got %s", StateCompleted, wf.State())
	}

	expectedOrder := []string{"step1", "step2", "step3"}
	if len(executionOrder) != len(expectedOrder) {
		t.Fatalf("expected %d steps, got %d", len(expectedOrder), len(executionOrder))
	}
	for i, step := range expectedOrder {
		if executionOrder[i] != step {
			t.Errorf("step %d: expected %s, got %s", i, step, executionOrder[i])
		}
	}
}

func TestWorkflow_FailureWithCompensation(t *testing.T) {
	ctx := context.Background()

	var executionOrder []string

	wf := New("test-workflow", ctx, WithCompensationMode(CompensateOnFailure))
	wf.Step("step1", func(ctx context.Context, span trace.Span) error {
		executionOrder = append(executionOrder, "step1")
		return nil
	}, WithCompensation(func(ctx context.Context, span trace.Span) error {
		executionOrder = append(executionOrder, "comp1")
		return nil
	}))
	wf.Step("step2", func(ctx context.Context, span trace.Span) error {
		executionOrder = append(executionOrder, "step2")
		return nil
	}, WithCompensation(func(ctx context.Context, span trace.Span) error {
		executionOrder = append(executionOrder, "comp2")
		return nil
	}))
	wf.Step("step3", func(ctx context.Context, span trace.Span) error {
		executionOrder = append(executionOrder, "step3")
		return errors.New("step3 failed")
	}, WithCompensation(func(ctx context.Context, span trace.Span) error {
		executionOrder = append(executionOrder, "comp3")
		return nil
	}))

	err := wf.Run(ctx)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	if wf.State() != StateCompensated {
		t.Errorf("expected state %s, got %s", StateCompensated, wf.State())
	}

	// Compensation should run in reverse order, skipping the failed step
	// Expected: step1, step2, step3(fail), comp2, comp1
	expectedOrder := []string{"step1", "step2", "step3", "comp2", "comp1"}
	if len(executionOrder) != len(expectedOrder) {
		t.Fatalf("expected %d operations, got %d: %v", len(expectedOrder), len(executionOrder), executionOrder)
	}
	for i, step := range expectedOrder {
		if executionOrder[i] != step {
			t.Errorf("operation %d: expected %s, got %s", i, step, executionOrder[i])
		}
	}
}

func TestWorkflow_FailureWithoutCompensation(t *testing.T) {
	ctx := context.Background()

	var executionOrder []string

	wf := New("test-workflow", ctx, WithCompensationMode(CompensateNever))
	wf.Step("step1", func(ctx context.Context, span trace.Span) error {
		executionOrder = append(executionOrder, "step1")
		return nil
	}, WithCompensation(func(ctx context.Context, span trace.Span) error {
		executionOrder = append(executionOrder, "comp1")
		return nil
	}))
	wf.Step("step2", func(ctx context.Context, span trace.Span) error {
		executionOrder = append(executionOrder, "step2")
		return errors.New("step2 failed")
	})

	err := wf.Run(ctx)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	// Compensation should NOT run
	expectedOrder := []string{"step1", "step2"}
	if len(executionOrder) != len(expectedOrder) {
		t.Fatalf("expected %d operations, got %d: %v", len(expectedOrder), len(executionOrder), executionOrder)
	}
}

func TestWorkflow_ManualCompensation(t *testing.T) {
	ctx := context.Background()

	var executionOrder []string

	wf := New("test-workflow", ctx, WithCompensationMode(CompensateManual))
	wf.Step("step1", func(ctx context.Context, span trace.Span) error {
		executionOrder = append(executionOrder, "step1")
		return nil
	}, WithCompensation(func(ctx context.Context, span trace.Span) error {
		executionOrder = append(executionOrder, "comp1")
		return nil
	}))
	wf.Step("step2", func(ctx context.Context, span trace.Span) error {
		executionOrder = append(executionOrder, "step2")
		return errors.New("step2 failed")
	})

	err := wf.Run(ctx)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	// Compensation not run yet
	if len(executionOrder) != 2 {
		t.Fatalf("expected 2 operations before manual compensation, got %d", len(executionOrder))
	}

	// Manually trigger compensation
	err = wf.CompensateAll(ctx)
	if err != nil {
		t.Fatalf("expected compensation to succeed, got %v", err)
	}

	// Now compensation should have run
	expectedOrder := []string{"step1", "step2", "comp1"}
	if len(executionOrder) != len(expectedOrder) {
		t.Fatalf("expected %d operations, got %d: %v", len(expectedOrder), len(executionOrder), executionOrder)
	}
}

func TestWorkflow_CompensationFailure(t *testing.T) {
	ctx := context.Background()

	wf := New("test-workflow", ctx)
	wf.Step("step1", func(ctx context.Context, span trace.Span) error {
		return nil
	}, WithCompensation(func(ctx context.Context, span trace.Span) error {
		return errors.New("compensation failed")
	}))
	wf.Step("step2", func(ctx context.Context, span trace.Span) error {
		return errors.New("step2 failed")
	})

	err := wf.Run(ctx)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	// State should remain failed due to compensation error
	if wf.State() != StateFailed {
		t.Errorf("expected state %s, got %s", StateFailed, wf.State())
	}
}

func TestWorkflow_DoubleRun(t *testing.T) {
	ctx := context.Background()

	wf := New("test-workflow", ctx)
	wf.Step("step1", func(ctx context.Context, span trace.Span) error {
		return nil
	})

	err := wf.Run(ctx)
	if err != nil {
		t.Fatalf("first run should succeed, got %v", err)
	}

	err = wf.Run(ctx)
	if err == nil {
		t.Fatal("second run should fail")
	}
}

func TestWorkflow_EmptyWorkflow(t *testing.T) {
	ctx := context.Background()

	wf := New("empty-workflow", ctx)

	err := wf.Run(ctx)
	if err != nil {
		t.Fatalf("empty workflow should succeed, got %v", err)
	}

	if wf.State() != StateCompleted {
		t.Errorf("expected state %s, got %s", StateCompleted, wf.State())
	}
}

func TestWorkflow_FirstStepFails(t *testing.T) {
	ctx := context.Background()

	var compensationRan bool

	wf := New("test-workflow", ctx)
	wf.Step("step1", func(ctx context.Context, span trace.Span) error {
		return errors.New("immediate failure")
	}, WithCompensation(func(ctx context.Context, span trace.Span) error {
		compensationRan = true
		return nil
	}))

	err := wf.Run(ctx)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	// Compensation should NOT run for the failed step itself
	if compensationRan {
		t.Error("compensation should not run for failed step")
	}
}

func TestWorkflow_StepAttributes(t *testing.T) {
	ctx := context.Background()

	var capturedSpan trace.Span

	wf := New("test-workflow", ctx)
	wf.Step("step-with-attrs", func(ctx context.Context, span trace.Span) error {
		capturedSpan = span
		return nil
	}, WithStepAttributes())

	err := wf.Run(ctx)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if capturedSpan == nil {
		t.Fatal("span was not captured")
	}
}

func TestWorkflow_ChainedSteps(t *testing.T) {
	ctx := context.Background()

	var executionOrder []string

	wf := New("chained-workflow", ctx).
		Step("a", func(ctx context.Context, span trace.Span) error {
			executionOrder = append(executionOrder, "a")
			return nil
		}).
		Step("b", func(ctx context.Context, span trace.Span) error {
			executionOrder = append(executionOrder, "b")
			return nil
		}).
		Step("c", func(ctx context.Context, span trace.Span) error {
			executionOrder = append(executionOrder, "c")
			return nil
		})

	err := wf.Run(ctx)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	expected := []string{"a", "b", "c"}
	for i, step := range expected {
		if executionOrder[i] != step {
			t.Errorf("step %d: expected %s, got %s", i, step, executionOrder[i])
		}
	}
}
