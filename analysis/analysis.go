// Package analysis runs the core analysis loop in code.
//
// Debugging from first principles: take the events you care about, take a
// comparable normal population, and ask which recorded field separates them.
// The loop is mechanical, so run it in code instead of clicking through a
// dashboard.
//
// CompareCohorts scores every field/value pair by how much more often it
// appears in the outlier group than in the baseline. The top result names the
// cohort behind a regression, which is the hypothesis you then verify against
// individual traces.
//
//	slow, normal := split(events, func(e analysis.Event) bool { return e["duration_ms"].(float64) >= 800 })
//	top := analysis.CompareCohorts(analysis.Options{Outlier: slow, Baseline: normal})[0]
//	// {Field: "payment.provider", Value: "bank-beta", Difference: 0.94, ...}
package analysis

import (
	"cmp"
	"fmt"
	"math"
	"slices"
	"strconv"
)

// Event is one flat telemetry record: a wide event's fields, a span's
// attributes, or a row from a backend query.
type Event = map[string]any

// CohortDifference is how strongly one field/value pair separates the outlier
// group from the baseline.
type CohortDifference struct {
	Field string
	// Value is rendered as a string for display and grouping.
	Value string
	// OutlierFraction is the share of outlier events carrying this value, 0-1.
	OutlierFraction float64
	// BaselineFraction is the share of baseline events carrying it, 0-1.
	BaselineFraction float64
	// Difference is OutlierFraction - BaselineFraction. Positive means the
	// value is over-represented in the outliers.
	Difference    float64
	OutlierCount  int
	BaselineCount int
}

// Options are the two populations and the scan limits. Zero values take the
// defaults noted on each field.
type Options struct {
	// Outlier holds the events under investigation: the errors, the slow
	// requests, the failed checkouts.
	Outlier []Event
	// Baseline is a comparable normal population from the same time range.
	Baseline []Event
	// Fields restricts the scan to these fields. Default: every field seen.
	Fields []string
	// IgnoreFields are skipped, such as identifiers known to be unique.
	IgnoreFields []string
	// MaxValuesPerField skips a field once it holds more distinct values than
	// this across both groups. Default 50.
	MaxValuesPerField int
	// MaxUniqueRatio skips a field whose distinct values outnumber this share
	// of the combined events. A request id or a raw duration takes a
	// near-unique value per event, so its values never repeat and it cannot
	// describe a cohort. Bucket numeric fields to have them ranked. Default 0.5.
	MaxUniqueRatio float64
	// MinDifference drops pairs whose absolute difference falls below it.
	// Default 0.1.
	MinDifference float64
	// Limit is the maximum number of results, strongest first. Default 20.
	Limit int
}

type counts struct{ outlier, baseline int }

// CompareCohorts ranks the field/value pairs that separate Outlier from
// Baseline, strongest first. It returns nothing when either group is empty,
// because a fraction over zero events carries no information.
func CompareCohorts(opts Options) []CohortDifference {
	if len(opts.Outlier) == 0 || len(opts.Baseline) == 0 {
		return nil
	}

	maxValues := cmp.Or(opts.MaxValuesPerField, 50)
	maxRatio := cmp.Or(opts.MaxUniqueRatio, 0.5)
	minDifference := cmp.Or(opts.MinDifference, 0.1)
	limit := cmp.Or(opts.Limit, 20)

	tally := tallyCohorts(opts)

	total := float64(len(opts.Outlier) + len(opts.Baseline))

	var results []CohortDifference
	for field, values := range tally {
		if len(values) > maxValues || float64(len(values)) > total*maxRatio {
			continue
		}

		for value, c := range values {
			outlierFraction := float64(c.outlier) / float64(len(opts.Outlier))
			baselineFraction := float64(c.baseline) / float64(len(opts.Baseline))
			difference := outlierFraction - baselineFraction

			if math.Abs(difference) < minDifference {
				continue
			}

			results = append(results, CohortDifference{
				Field:            field,
				Value:            value,
				OutlierFraction:  outlierFraction,
				BaselineFraction: baselineFraction,
				Difference:       difference,
				OutlierCount:     c.outlier,
				BaselineCount:    c.baseline,
			})
		}
	}

	// Strongest first. On a tie prefer the over-represented value, because a
	// cohort you can open traces from beats one defined by its absence. Fall
	// back to field and value so repeated runs return the same order.
	slices.SortFunc(results, func(a, b CohortDifference) int {
		return cmp.Or(
			cmp.Compare(math.Abs(b.Difference), math.Abs(a.Difference)),
			cmp.Compare(b.Difference, a.Difference),
			cmp.Compare(a.Field, b.Field),
			cmp.Compare(a.Value, b.Value),
		)
	})

	if len(results) > limit {
		results = results[:limit]
	}

	return results
}

// tallyCohorts counts, per included field and value, how many outlier and
// baseline events carry it.
func tallyCohorts(opts Options) map[string]map[string]*counts {
	includes := func(field string) bool {
		if slices.Contains(opts.IgnoreFields, field) {
			return false
		}

		return opts.Fields == nil || slices.Contains(opts.Fields, field)
	}

	tally := map[string]map[string]*counts{}
	accumulate := func(events []Event, outlier bool) {
		for _, event := range events {
			for field, raw := range event {
				value, ok := valueKey(raw)
				if !ok || !includes(field) {
					continue
				}

				if tally[field] == nil {
					tally[field] = map[string]*counts{}
				}
				c := tally[field][value]
				if c == nil {
					c = &counts{}
					tally[field][value] = c
				}
				if outlier {
					c.outlier++
				} else {
					c.baseline++
				}
			}
		}
	}
	accumulate(opts.Outlier, true)
	accumulate(opts.Baseline, false)

	return tally
}

// valueKey renders a scalar as a grouping key. Anything else (a map, a slice,
// nil) reports false: a nested structure does not name a cohort.
func valueKey(value any) (string, bool) {
	switch v := value.(type) {
	case string:
		return v, true
	case bool:
		return strconv.FormatBool(v), true
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64), true
	case float32:
		return strconv.FormatFloat(float64(v), 'f', -1, 32), true
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return fmt.Sprint(v), true
	default:
		return "", false
	}
}

// Bucket labels a number with the range it falls in: "<1000", "1000-2000",
// ">=5000".
//
// CompareCohorts skips a field whose values never repeat, which is what a raw
// duration or amount does. Bucketing at instrumentation time turns such a
// field into one that can describe a cohort.
func Bucket(value float64, boundaries []float64) string {
	// A NaN filed under the slowest bucket would invent a cohort that never
	// happened, which is worse than admitting the value is missing.
	if math.IsNaN(value) || math.IsInf(value, 0) || len(boundaries) == 0 {
		return "unknown"
	}

	// Sorted, so boundaries listed out of order still give the ranges meant.
	ordered := slices.Sorted(slices.Values(boundaries))
	label := func(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }

	for index, boundary := range ordered {
		if value < boundary {
			if index == 0 {
				return "<" + label(boundary)
			}

			return label(ordered[index-1]) + "-" + label(boundary)
		}
	}

	return ">=" + label(ordered[len(ordered)-1])
}
