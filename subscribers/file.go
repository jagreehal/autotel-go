package subscribers

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

// FileSubscriber appends each business event to a file as one JSON object per
// line (NDJSON), which jq, DuckDB and most log shippers read as they are.
//
//	sub, err := subscribers.NewFileSubscriber("events.ndjson")
//	autotel.Init(ctx, autotel.WithSubscribers(sub))
//
// Each line is {"type":"event","name":...,"attributes":{...},"timestamp":...}.
// Events tracked inside a span carry its trace_id and span_id among the
// attributes, so a line leads back to the request that produced it.
type FileSubscriber struct {
	mu   sync.Mutex
	file *os.File
}

// NewFileSubscriber opens path for appending, creating it if needed.
func NewFileSubscriber(path string) (*FileSubscriber, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("file subscriber: %w", err)
	}

	return &FileSubscriber{file: file}, nil
}

// Send writes one line.
func (s *FileSubscriber) Send(_ context.Context, event string, properties map[string]any) error {
	line, err := json.Marshal(struct {
		Type       string         `json:"type"`
		Name       string         `json:"name"`
		Attributes map[string]any `json:"attributes"`
		Timestamp  time.Time      `json:"timestamp"`
	}{"event", event, properties, time.Now().UTC()})
	if err != nil {
		return fmt.Errorf("file subscriber: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	_, err = s.file.Write(append(line, '\n'))

	return err
}

// Close closes the file.
func (s *FileSubscriber) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.file.Close()
}
