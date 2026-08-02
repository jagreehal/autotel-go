package autotel_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// Three features have shipped doing nothing: WithSpanFilter was dropped by the
// config merge, AdaptiveSampler's error and latency rates were stored and never
// read, and the rate limiter covered only spans created through autotel.Start.
// Every one of them passed a test that asserted a value had been set.
//
// This test makes the gap visible instead. It reads options.go, collects every
// exported option, and fails when one is neither exercised end-to-end nor
// recorded below as verified somewhere else. Adding an option now means saying
// how it is checked.
//
// Moving a name into verifiedElsewhere is a deliberate act, not a default.

// endToEndTest drives Init and asserts on what reached an exporter, which is the
// only way to catch a value that is set and then discarded.
const endToEndTest = "pipeline_e2e_test.go"

// verifiedElsewhere lists options whose effect cannot be observed through an
// in-memory exporter, mapped to the test that does verify them. The named file
// has to mention the option, so an entry cannot rot into a claim about a test
// that no longer exists.
var verifiedElsewhere = map[string]string{
	// These configure the exporter's target. An in-memory exporter has no target,
	// so the assertion has to be made against the resolved config.
	"WithEndpoint": "endpoint_test.go",
	"WithInsecure": "endpoint_test.go",
	"WithProtocol": "option_config_test.go",
	"WithHeaders":  "option_config_test.go",
	"WithBackend":  "option_config_test.go",

	// The event pipeline runs beside the span pipeline, so a span exporter cannot
	// see it. Delivery is asserted against a recording subscriber; the queue
	// tuning knobs are only checked at config level, which is weaker.
	"WithSubscribers":  "events_test.go",
	"WithEventQueue":   "option_config_test.go",
	"WithEventBackoff": "option_config_test.go",
	"WithEventRetry":   "option_config_test.go",
}

// knownUnverified is the honest remainder: options with no assertion anywhere.
// Shrinking this list is the work; an option must never be added to it without
// a reason that survives being read aloud.
var knownUnverified = map[string]string{
	"WithMetricExporters":    "metrics reach a reader, not the span exporter; needs a metric-side harness",
	"WithMetricInterval":     "same, and asserting an interval means controlling the reader's clock",
	"WithMaxQueueSize":       "only observable by overflowing the batch processor, which is timing-dependent",
	"WithMaxExportBatchSize": "same",
}

func TestEveryOptionIsVerifiedSomewhere(t *testing.T) {
	options := exportedOptions(t, "options.go")
	if len(options) == 0 {
		t.Fatal("parsed no options from options.go; the parser is wrong, not the package")
	}

	e2e := readFile(t, endToEndTest)

	var unclassified []string
	for _, option := range options {
		if strings.Contains(e2e, option) {
			continue
		}
		if file, ok := verifiedElsewhere[option]; ok {
			if !strings.Contains(readFile(t, file), option) {
				t.Errorf("%s claims to verify %s, and does not mention it", file, option)
			}
			continue
		}
		if _, ok := knownUnverified[option]; ok {
			continue
		}
		unclassified = append(unclassified, option)
	}

	if len(unclassified) > 0 {
		t.Errorf("no assertion covers these options: %v\n"+
			"Add an end-to-end test in %s, or record where they are verified.",
			unclassified, endToEndTest)
	}
}

// A stale entry is worse than none: it reads as coverage that does not exist.
func TestCoverageListsDoNotGoStale(t *testing.T) {
	options := map[string]bool{}
	for _, option := range exportedOptions(t, "options.go") {
		options[option] = true
	}

	for _, list := range []map[string]string{verifiedElsewhere, knownUnverified} {
		for option := range list {
			if !options[option] {
				t.Errorf("%s is listed but no longer exists in options.go", option)
			}
		}
	}

	e2e := readFile(t, endToEndTest)
	for option := range knownUnverified {
		if strings.Contains(e2e, option) {
			t.Errorf("%s is listed as unverified but %s now exercises it; remove the entry",
				option, endToEndTest)
		}
	}
}

// exportedOptions returns the names of exported functions in path that return an
// Option.
func exportedOptions(t *testing.T, path string) []string {
	t.Helper()

	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}

	var names []string
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv != nil || !fn.Name.IsExported() {
			continue
		}
		if fn.Type.Results == nil || len(fn.Type.Results.List) != 1 {
			continue
		}
		if ident, ok := fn.Type.Results.List[0].Type.(*ast.Ident); ok && ident.Name == "Option" {
			names = append(names, fn.Name.Name)
		}
	}
	return names
}

func readFile(t *testing.T, path string) string {
	t.Helper()

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(content)
}
