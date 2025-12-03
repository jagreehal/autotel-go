package autotel

import (
	"context"
	"reflect"
)

// TraceContext provides access to trace information and span operations
// within a traced function.
type TraceContext interface {
	// Context returns the underlying context.Context for creating child spans
	Context() context.Context
	// SetAttribute sets an attribute on the current span
	SetAttribute(key string, value any)
	// SetAttributes sets multiple attributes on the current span
	SetAttributes(attrs map[string]any)
	// TraceID returns the current trace ID
	TraceID() string
	// SpanID returns the current span ID
	SpanID() string
}

// traceContextImpl implements TraceContext
type traceContextImpl struct {
	span Span
	ctx  context.Context
}

func (tc *traceContextImpl) Context() context.Context {
	return tc.ctx
}

func (tc *traceContextImpl) SetAttribute(key string, value any) {
	tc.span.SetAttribute(key, value)
}

func (tc *traceContextImpl) SetAttributes(attrs map[string]any) {
	for k, v := range attrs {
		tc.span.SetAttribute(k, v)
	}
}

func (tc *traceContextImpl) TraceID() string {
	return GetTraceID(tc.ctx)
}

func (tc *traceContextImpl) SpanID() string {
	return GetSpanID(tc.ctx)
}

// TraceFunc is a functional API that supports both factory and immediate execution patterns.
//
// Factory pattern: Returns a wrapped function
//   - trace(func(ctx TraceContext) func(...args) (T, error)) -> func(...args) (T, error)
//
// Immediate execution: Executes immediately
//   - trace(func(ctx TraceContext) (T, error)) -> (T, error)
//
// IMPORTANT: Pattern detection uses reflection to inspect function signatures WITHOUT
// executing the function. This avoids side effects that could occur if the function
// starts goroutines or performs work during pattern detection (similar to the Node.js
// async function bug where calling async functions for pattern detection would cause
// them to start executing synchronously until the first await).
//
// The fix: We use reflect.Type to inspect the function signature and determine if it's
// a factory pattern (returns a function) or immediate execution (returns a value),
// without ever calling the function during detection.
func TraceFunc(ctx context.Context, name string, fn any) any {
	fnValue := reflect.ValueOf(fn)
	fnType := fnValue.Type()

	// Validate that fn is a function
	if fnType.Kind() != reflect.Func {
		panic("TraceFunc: fn must be a function")
	}

	// Pattern detection using reflection (no function execution)
	// Check if it's a factory pattern: func(TraceContext) -> func(...) -> T
	// vs immediate execution: func(TraceContext) -> T
	if fnType.NumIn() == 1 && fnType.NumOut() > 0 {
		// Check if first parameter is TraceContext-compatible
		firstParamType := fnType.In(0)
		if isTraceContextType(firstParamType) {
			// Check if return type is a function (factory pattern)
			// This inspection happens WITHOUT calling the function
			firstReturnType := fnType.Out(0)
			if firstReturnType.Kind() == reflect.Func {
				// Factory pattern: return a wrapped function
				return wrapFactory(ctx, name, fnValue, fnType)
			}
			// Immediate execution pattern: execute immediately
			return executeImmediately(ctx, name, fnValue, fnType)
		}
	}

	// If it doesn't match the expected patterns, treat as immediate execution
	// This handles plain functions without TraceContext parameter
	return executeImmediately(ctx, name, fnValue, fnType)
}

// isTraceContextType checks if a type is compatible with TraceContext
func isTraceContextType(t reflect.Type) bool {
	// Check if it's the TraceContext interface
	if t.Implements(reflect.TypeOf((*TraceContext)(nil)).Elem()) {
		return true
	}
	// Also accept context.Context as it's commonly used
	if t == reflect.TypeOf((*context.Context)(nil)).Elem() {
		return true
	}
	return false
}

// wrapFactory wraps a factory function that returns another function
func wrapFactory(ctx context.Context, name string, fnValue reflect.Value, fnType reflect.Type) any {
	// Get the return type (which should be a function)
	returnType := fnType.Out(0)

	// Extract input and output types from the returned function
	var inTypes []reflect.Type
	var outTypes []reflect.Type
	if returnType.NumIn() > 0 {
		inTypes = make([]reflect.Type, returnType.NumIn())
		for i := 0; i < returnType.NumIn(); i++ {
			inTypes[i] = returnType.In(i)
		}
	}
	if returnType.NumOut() > 0 {
		outTypes = make([]reflect.Type, returnType.NumOut())
		for i := 0; i < returnType.NumOut(); i++ {
			outTypes[i] = returnType.Out(i)
		}
	}

	// Create a wrapper function with the same signature as the returned function
	wrapperType := reflect.FuncOf(inTypes, outTypes, returnType.IsVariadic())

	wrapper := reflect.MakeFunc(wrapperType, func(args []reflect.Value) []reflect.Value {
		// Create trace context
		traceCtx, span := Start(ctx, name)
		defer span.End()

		// Create TraceContext implementation
		tc := &traceContextImpl{span: span, ctx: traceCtx}

		// Call the factory function with TraceContext
		// Note: Pattern detection already happened using reflection (no execution),
		// so we know this is a factory pattern. We only call it here to get the
		// returned function, not for pattern detection.
		factoryResult := fnValue.Call([]reflect.Value{reflect.ValueOf(tc)})
		if len(factoryResult) == 0 {
			panic("TraceFunc: factory function returned no values")
		}

		// Get the returned function
		returnedFn := factoryResult[0]
		if returnedFn.Kind() != reflect.Func {
			panic("TraceFunc: factory function did not return a function")
		}

		// Call the returned function with the provided arguments
		result := returnedFn.Call(args)

		// Handle errors if the function returns an error
		if len(result) > 0 {
			if errVal := result[len(result)-1]; errVal.IsValid() && !errVal.IsNil() {
				if err, ok := errVal.Interface().(error); ok {
					span.RecordError(err)
				}
			}
		}

		return result
	})

	return wrapper.Interface()
}

// executeImmediately executes a function immediately within a trace
func executeImmediately(ctx context.Context, name string, fnValue reflect.Value, fnType reflect.Type) any {
	traceCtx, span := Start(ctx, name)
	defer span.End()

	// Create TraceContext implementation
	tc := &traceContextImpl{span: span, ctx: traceCtx}

	// Prepare arguments
	var args []reflect.Value
	if fnType.NumIn() > 0 {
		firstParamType := fnType.In(0)
		if isTraceContextType(firstParamType) {
			args = append(args, reflect.ValueOf(tc))
		} else {
			// Plain function without TraceContext - just pass context
			args = append(args, reflect.ValueOf(traceCtx))
		}
	}

	// Call the function
	result := fnValue.Call(args)

	// Handle errors if the function returns an error
	if len(result) > 0 {
		if errVal := result[len(result)-1]; errVal.IsValid() && !errVal.IsNil() {
			if err, ok := errVal.Interface().(error); ok {
				span.RecordError(err)
			}
		}
	}

	// Return the result
	if len(result) == 0 {
		return nil
	}
	if len(result) == 1 {
		return result[0].Interface()
	}
	// Multiple return values - handle common (T, error) pattern
	if len(result) == 2 {
		// Check if last value is an error
		errVal := result[1]
		if errVal.IsValid() && !errVal.IsNil() {
			if _, ok := errVal.Interface().(error); ok {
				// If error is not nil, return just the error
				return errVal.Interface()
			}
		}
		// If error is nil, return just the first value (the result)
		return result[0].Interface()
	}
	// More than 2 return values - return as slice (caller should type assert)
	results := make([]any, len(result))
	for i, v := range result {
		results[i] = v.Interface()
	}
	return results
}
