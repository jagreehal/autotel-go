package autotel

import (
	"reflect"
	"testing"
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// Regression: mergeConfigs rebuilt the config from defaults and copied only an
// explicitly enumerated list of fields, so any option whose field was not added
// to that list was silently discarded. WithSpanFilter and WithTailSampling both
// shipped with no entry in the list and therefore did nothing at all.
func TestMergeKeepsPipelineOptions(t *testing.T) {
	explicit := defaultConfig()
	WithSpanFilter(func(sdktrace.ReadOnlySpan) bool { return true })(explicit)
	WithTailSampling(true)(explicit)

	merged := mergeConfigs(explicit, nil, nil)

	if merged.SpanFilter == nil {
		t.Error("SpanFilter was dropped by the merge, so WithSpanFilter has no effect")
	}
	if !merged.TailSamplingEnabled {
		t.Error("TailSamplingEnabled was dropped by the merge, so WithTailSampling has no effect")
	}
}

// mergeableFields also come from YAML or environment variables, so the merge
// resolves them across layers rather than carrying them verbatim. Every other
// exported field must survive the merge unchanged.
var mergeableFields = map[string]bool{
	"ServiceName":        true,
	"ServiceVersion":     true,
	"Environment":        true,
	"Endpoint":           true,
	"Protocol":           true,
	"Headers":            true,
	"ResourceAttributes": true,
	"Debug":              true,
}

// Guard: adding a Config field must not silently drop it in the merge. This fails
// on the next field added without wiring it through, which is exactly how
// WithSpanFilter and WithTailSampling ended up dead on arrival.
func TestMergePreservesEveryExplicitField(t *testing.T) {
	explicit := defaultConfig()

	// Give every non-mergeable exported field a recognisably non-default value.
	value := reflect.ValueOf(explicit).Elem()
	configType := value.Type()
	for i := range configType.NumField() {
		field := configType.Field(i)
		if !field.IsExported() || mergeableFields[field.Name] {
			continue
		}
		if !setDistinctive(value.Field(i)) {
			t.Logf("skipping %s (%s): no distinctive value available", field.Name, field.Type)
		}
	}

	// Snapshot before merging: comparing against the values actually set catches a
	// dropped field even when its default is non-zero (BatchTimeout, MaxQueueSize).
	want := *explicit

	merged := mergeConfigs(explicit, nil, nil)

	wantValue := reflect.ValueOf(&want).Elem()
	mergedValue := reflect.ValueOf(merged).Elem()
	for i := range configType.NumField() {
		field := configType.Field(i)
		if !field.IsExported() || mergeableFields[field.Name] {
			continue
		}
		if !sameValue(wantValue.Field(i), mergedValue.Field(i)) {
			t.Errorf("Config.%s was dropped or altered by mergeConfigs; wire it through applyExplicitLayer",
				field.Name)
		}
	}
}

// sameValue reports whether a merged field still holds what was set on the
// explicit config. Funcs are compared by identity since they are not comparable.
func sameValue(want, got reflect.Value) bool {
	if want.Kind() == reflect.Func {
		return want.Pointer() == got.Pointer()
	}
	return reflect.DeepEqual(want.Interface(), got.Interface())
}

// setDistinctive gives a field a non-zero value, reporting whether it managed to.
func setDistinctive(field reflect.Value) bool {
	if !field.CanSet() {
		return false
	}

	switch field.Kind() {
	case reflect.Bool:
		field.SetBool(true)
	case reflect.String:
		field.SetString("set")
	case reflect.Int, reflect.Int64:
		// time.Duration is an int64; any non-default value works for both.
		field.SetInt(int64(7 * time.Second))
	case reflect.Float64:
		field.SetFloat(1)
	case reflect.Map:
		field.Set(reflect.MakeMapWithSize(field.Type(), 0))
		if field.Type().Key().Kind() == reflect.String && field.Type().Elem().Kind() == reflect.String {
			field.SetMapIndex(reflect.ValueOf("k"), reflect.ValueOf("v"))
		}
	case reflect.Slice:
		field.Set(reflect.MakeSlice(field.Type(), 1, 1))
	case reflect.Pointer:
		field.Set(reflect.New(field.Type().Elem()))
	case reflect.Func:
		field.Set(reflect.MakeFunc(field.Type(), func([]reflect.Value) []reflect.Value {
			results := make([]reflect.Value, field.Type().NumOut())
			for i := range results {
				results[i] = reflect.Zero(field.Type().Out(i))
			}
			return results
		}))
	case reflect.Interface:
		return false // no concrete value to install
	default:
		return false
	}
	return true
}
