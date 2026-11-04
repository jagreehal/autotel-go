package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/jagreehal/autotel-go/v2"
	"github.com/jagreehal/autotel-go/v2/logging"
)

// setup holds the order in one place: the pipeline first, then the default
// logger with every destination in it. Call it on the first line of main.
func setup(ctx context.Context) error {
	// 1. The pipeline: where spans, and now log records, go.
	if _, err := autotel.Init(ctx, autotel.WithService("atm"), autotel.WithDevtools()); err != nil {
		return err
	}

	// 2. One logger, two destinations.
	slog.SetDefault(slog.New(slog.NewMultiHandler(
		// The same JSON lines on stdout, with the active span's ids stamped in.
		logging.NewTraceHandler(slog.NewJSONHandler(os.Stdout, nil)),
		// Each line also goes to devtools as a log record, linked to its span.
		autotel.Logger("atm").Handler(),
	)))

	return nil
}
