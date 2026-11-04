// Package rotatefile is an io.Writer over files that roll over on a schedule.
package rotatefile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
)

// Options configures a Writer.
type Options struct {
	Dir    string
	Prefix string
	// Layout is the time.Format layout whose change starts a new file.
	// Empty means one file that never rolls over.
	Layout string
	// MaxFiles is how many files to keep, the live one included. Zero or less
	// keeps them all.
	MaxFiles int
}

// Writer appends to the current file. Writes are synchronous, so a slow disk
// slows the caller and no line is dropped.
//
// ponytail: one mutex per file, a buffered background writer if logging ever
// shows up in request latency.
type Writer struct {
	opts Options
	now  func() time.Time

	mu     sync.Mutex
	file   *os.File
	stamp  string
	closed bool
}

// Open creates the directory and the current file, so a path that can't be
// written fails here rather than on the first log line.
func Open(opts Options) (*Writer, error) {
	return open(opts, time.Now)
}

func open(opts Options, now func() time.Time) (*Writer, error) {
	if err := os.MkdirAll(opts.Dir, 0o750); err != nil {
		return nil, err
	}
	w := &Writer{opts: opts, now: now}
	if err := w.roll(w.currentStamp()); err != nil {
		return nil, err
	}
	return w, nil
}

// Write appends p, rolling over first when the period has changed. If the new
// file can't be opened, the line goes to the old one and the next write retries.
func (w *Writer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return 0, os.ErrClosed
	}
	if stamp := w.currentStamp(); stamp != w.stamp {
		if err := w.roll(stamp); err != nil {
			otel.Handle(fmt.Errorf("rolling log file: %w", err))
		}
	}
	return w.file.Write(p)
}

// Close closes the current file. Calling it again does nothing.
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return nil
	}
	w.closed = true
	return w.file.Close()
}

func (w *Writer) currentStamp() string {
	if w.opts.Layout == "" {
		return ""
	}
	return w.now().UTC().Format(w.opts.Layout)
}

func (w *Writer) name(stamp string) string {
	if stamp == "" {
		return w.opts.Prefix + ".log"
	}
	return w.opts.Prefix + "." + stamp + ".log"
}

// roll opens the file for stamp. The old file stays open until the new one is
// ready. Only opening can fail it: losing a line matters more than a file left
// over, so close and prune errors are reported and otherwise ignored.
func (w *Writer) roll(stamp string) error {
	// Logs can hold personal data: not world-readable.
	file, err := os.OpenFile(filepath.Join(w.opts.Dir, w.name(stamp)), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}

	if w.file != nil {
		if err := w.file.Close(); err != nil {
			otel.Handle(fmt.Errorf("closing log file: %w", err))
		}
	}
	w.file, w.stamp = file, stamp
	if err := w.prune(); err != nil {
		otel.Handle(err)
	}
	return nil
}

// prune removes the oldest files past MaxFiles. Only names this writer could
// have produced are candidates, so another service's files in a shared
// directory are safe. The live file always stays, whatever a clock change has
// left dated after it.
func (w *Writer) prune() error {
	if w.opts.MaxFiles <= 0 || w.opts.Layout == "" {
		return nil
	}
	entries, err := os.ReadDir(w.opts.Dir)
	if err != nil {
		return err
	}

	var stamps []string
	for _, entry := range entries {
		middle, ok := strings.CutPrefix(entry.Name(), w.opts.Prefix+".")
		if !ok {
			continue
		}
		middle, ok = strings.CutSuffix(middle, ".log")
		if !ok {
			continue
		}
		if _, err := time.Parse(w.opts.Layout, middle); err == nil && middle != w.stamp {
			stamps = append(stamps, middle)
		}
	}
	keep := w.opts.MaxFiles - 1 // the live file is the other one
	if len(stamps) <= keep {
		return nil
	}

	// The layouts run from year to minute, so names sort by age.
	slices.Sort(stamps)
	var errs []error
	for _, stamp := range stamps[:len(stamps)-keep] {
		if err := os.Remove(filepath.Join(w.opts.Dir, w.name(stamp))); err != nil {
			errs = append(errs, fmt.Errorf("pruning log file: %w", err))
		}
	}
	return errors.Join(errs...)
}
