package rotatefile

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func names(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, entry := range entries {
		out = append(out, entry.Name())
	}
	return out
}

func TestRollsAndPrunes(t *testing.T) {
	dir := t.TempDir()
	foreign := filepath.Join(dir, "orders-api.2026-11-01.log")
	unrelated := filepath.Join(dir, "orders.notes.log")
	for _, path := range []string{foreign, unrelated} {
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	clock := time.Date(2026, 11, 1, 23, 0, 0, 0, time.UTC)
	w, err := open(Options{Dir: dir, Prefix: "orders", Layout: "2006-01-02", MaxFiles: 2}, func() time.Time { return clock })
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if _, err := w.Write([]byte("line\n")); err != nil {
			t.Fatal(err)
		}
		clock = clock.Add(24 * time.Hour)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	got := names(t, dir)
	want := []string{"orders-api.2026-11-01.log", "orders.2026-11-02.log", "orders.2026-11-03.log", "orders.notes.log"}
	if !slices.Equal(got, want) {
		t.Fatalf("files = %v, want %v", got, want)
	}
}

func TestNeverRolls(t *testing.T) {
	dir := t.TempDir()
	w, err := Open(Options{Dir: dir, Prefix: "orders"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("line\n")); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if _, err := w.Write([]byte("late\n")); err == nil {
		t.Fatal("write after Close succeeded")
	}
	body, err := os.ReadFile(filepath.Join(dir, "orders.log"))
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "line\n" {
		t.Fatalf("body = %q", body)
	}
}

func TestFailedOpenIsAnError(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(Options{Dir: filepath.Join(file, "sub"), Prefix: "x"}); err == nil {
		t.Fatal("opened a file under a file")
	}
}

func TestConcurrentWritesAcrossARollover(t *testing.T) {
	dir := t.TempDir()
	var mu sync.Mutex
	clock := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	w, err := open(Options{Dir: dir, Prefix: "x", Layout: "2006-01-02-15-04"}, func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		clock = clock.Add(time.Second)
		return clock
	})
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 100 {
				if _, err := w.Write([]byte("l\n")); err != nil {
					t.Error(err)
				}
			}
		})
	}
	wg.Wait()
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	lines := 0
	files := names(t, dir)
	for _, name := range files {
		body, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		lines += strings.Count(string(body), "\n")
	}
	if lines != 800 {
		t.Fatalf("%d lines across %d files, want 800", lines, len(files))
	}
	if len(files) < 2 {
		t.Fatalf("%d files: the clock should have crossed a minute", len(files))
	}
}

func TestRolloverClosesTheOldFile(t *testing.T) {
	clock := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	w, err := open(Options{Dir: t.TempDir(), Prefix: "x", Layout: "2006-01-02"}, func() time.Time { return clock })
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()

	old := w.file
	clock = clock.Add(24 * time.Hour)
	if _, err := w.Write([]byte("l\n")); err != nil {
		t.Fatal(err)
	}
	if err := old.Close(); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("old file still open: %v", err)
	}
}

func TestFailedRolloverKeepsWritingToTheOldFile(t *testing.T) {
	dir := t.TempDir()
	clock := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	w, err := open(Options{Dir: dir, Prefix: "x", Layout: "2006-01-02"}, func() time.Time { return clock })
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()

	// A directory where the next file belongs makes opening it fail.
	if err := os.Mkdir(filepath.Join(dir, "x.2026-11-02.log"), 0o750); err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(24 * time.Hour)
	if _, err := w.Write([]byte("kept\n")); err != nil {
		t.Fatalf("a failed rollover lost the line: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(dir, "x.2026-11-01.log"))
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "kept\n" {
		t.Fatalf("old file = %q", body)
	}
}

func TestFutureDatedFilesCannotPruneTheLiveOne(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"x.2030-01-01.log", "x.2030-01-02.log", "x.2030-01-03.log"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	clock := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	w, err := open(Options{Dir: dir, Prefix: "x", Layout: "2006-01-02", MaxFiles: 2}, func() time.Time { return clock })
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()

	got := names(t, dir)
	want := []string{"x.2026-11-01.log", "x.2030-01-03.log"}
	if !slices.Equal(got, want) {
		t.Fatalf("files = %v, want %v", got, want)
	}
}

func TestFilesAreNotWorldReadable(t *testing.T) {
	dir := t.TempDir()
	w, err := Open(Options{Dir: dir, Prefix: "x"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()
	info, err := os.Stat(filepath.Join(dir, "x.log"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o007 != 0 {
		t.Fatalf("mode %v", info.Mode())
	}
}
