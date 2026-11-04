package autotel

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/trace"

	"github.com/jagreehal/autotel-go/v2/internal/rotatefile"
)

// pipeline is what one Init built: a provider per signal. mp and lp are nil
// when their signal is disabled.
type pipeline struct {
	tp *trace.TracerProvider
	mp *sdkmetric.MeterProvider
	lp *sdklog.LoggerProvider

	file    *rotatefile.Writer
	tracker EventTracker

	spanExportFailure *atomic.Pointer[error]

	// A second shutdown, from Shutdown and a deferred cleanup both, reports
	// the first one's result rather than the metric reader's "already shut
	// down" error.
	once sync.Once
	err  error
}

// ShutdownTimeout bounds Shutdown, and the cleanup function Init returns,
// when the context given has no deadline of its own, so a process exits
// promptly when no receiver is listening.
const ShutdownTimeout = 5 * time.Second

// current is the pipeline the most recent Init built.
var current atomic.Pointer[pipeline]

// Flush sends everything the pipeline is holding (spans waiting in a batch,
// metrics waiting for the next collection, log records) and waits for the
// exporters to answer or ctx to end. The pipeline keeps running afterwards.
//
// Reach for it before a point where the process may stop without warning. At
// the end of a short-lived program, use Shutdown, which also flushes.
// Business events from Track are delivered in the background and drained by
// Shutdown, not by Flush.
func Flush(ctx context.Context) error {
	p := current.Load()
	if p == nil {
		return nil
	}

	var errs []error
	errs = append(errs, p.tp.ForceFlush(ctx))
	if p.mp != nil {
		errs = append(errs, p.mp.ForceFlush(ctx))
	}
	if p.lp != nil {
		errs = append(errs, p.lp.ForceFlush(ctx))
	}
	errs = append(errs, p.lastSpanExportError())

	return errors.Join(errs...)
}

// Shutdown flushes and closes the pipeline the most recent Init built, and
// drains the business-event queue. It is the cleanup function Init returns,
// callable from anywhere and reporting what failed: a flush that reached no
// receiver comes back as an error rather than vanishing. A ctx without a
// deadline gets ShutdownTimeout.
//
//	defer func() {
//	    if err := autotel.Shutdown(context.Background()); err != nil {
//	        log.Printf("telemetry not delivered: %v", err)
//	    }
//	}()
func Shutdown(ctx context.Context) error {
	p := current.Load()
	if p == nil {
		return nil
	}

	return p.shutdown(ctx)
}

func (p *pipeline) shutdown(ctx context.Context) error {
	p.once.Do(func() {
		if _, ok := ctx.Deadline(); !ok {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, ShutdownTimeout)
			defer cancel()
		}

		p.err = p.close(ctx)
		if p.err != nil {
			p.err = fmt.Errorf("autotel: telemetry not delivered; "+
				"check the endpoint, and any credentials in its headers: %w", p.err)
		}
	})

	return p.err
}

// lastSpanExportError is the error from the latest span export, if it failed.
func (p *pipeline) lastSpanExportError() error {
	if failure := p.spanExportFailure.Load(); failure != nil {
		return *failure
	}
	return nil
}

// close stops everything at once. They share ctx's deadline, so in sequence a
// stuck trace export would use it up and leave logs and events unflushed.
func (p *pipeline) close(ctx context.Context) error {
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		errs []error
	)
	run := func(stop func(context.Context) error) {
		wg.Go(func() {
			err := stop(ctx)
			mu.Lock()
			defer mu.Unlock()
			errs = append(errs, err)
		})
	}

	run(p.tp.Shutdown)
	if p.mp != nil {
		run(p.mp.Shutdown)
	}
	if p.lp != nil {
		run(p.lp.Shutdown)
	}
	if p.tracker != nil {
		globalTrackerMu.Lock()
		if globalTracker == p.tracker {
			globalTracker = nil
		}
		globalTrackerMu.Unlock()
		run(p.tracker.Shutdown)
	}
	wg.Wait()

	errs = append(errs, p.lastSpanExportError())

	// A pipeline an Init has replaced no longer decides where logs go.
	if current.Load() == p {
		logsStayLocal.Store(true)
	}
	if p.file != nil {
		logFileSink.CompareAndSwap(p.file, nil)
		errs = append(errs, p.file.Close())
	}

	return errors.Join(errs...)
}

// shutdownPartial stops the providers an Init built before a later step
// failed, so their readers and processors do not keep running unowned.
func shutdownPartial(mp *sdkmetric.MeterProvider, lp *sdklog.LoggerProvider) {
	ctx, cancel := context.WithTimeout(context.Background(), ShutdownTimeout)
	defer cancel()

	if mp != nil {
		_ = mp.Shutdown(ctx)
	}
	if lp != nil {
		_ = lp.Shutdown(ctx)
	}
}
