package autotel

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
)

func TestTrackWithoutSubscribersWarnsOnce(t *testing.T) {
	globalTrackerMu.Lock()
	saved := globalTracker
	globalTracker = nil
	globalTrackerMu.Unlock()
	warnNoSubscribers = sync.Once{}

	var buf bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() {
		slog.SetDefault(previous)
		globalTrackerMu.Lock()
		globalTracker = saved
		globalTrackerMu.Unlock()
	})

	Track(context.Background(), "a", nil)
	Track(context.Background(), "b", nil)

	if got := strings.Count(buf.String(), "no subscribers"); got != 1 {
		t.Fatalf("warned %d times, want once:\n%s", got, buf.String())
	}
}
