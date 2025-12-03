package subscribers

import (
	"github.com/jagreehal/autotel-go"
)

func init() {
	// Register the queue factory with autotel to avoid import cycles.
	// This allows autotel.Init() to create queues when subscribers are provided.
	autotel.RegisterQueueFactory(func(cfg *autotel.Config, subscribers []autotel.Subscriber) autotel.EventTracker {
		subs := make([]Subscriber, len(subscribers))
		for i, s := range subscribers {
			subs[i] = s.(Subscriber)
		}
		qc := QueueConfig{
			QueueSize:        cfg.EventQueueSize,
			FlushInterval:    cfg.EventFlushInterval,
			CircuitThreshold: cfg.EventCBThreshold,
			BackoffMin:       cfg.EventBackoffMin,
			BackoffMax:       cfg.EventBackoffMax,
			CircuitReset:     cfg.EventCBReset,
		}
		return NewQueueWithConfig(qc, subs...)
	})
}
