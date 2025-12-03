package autotel

// mergeConfigs merges configs in priority order: explicit > yaml > env > defaults
// The explicit config has the highest priority and overrides everything.
// YAML config overrides env vars, and env vars override defaults.
func mergeConfigs(explicit, yaml, env *Config) *Config {
	merged := defaultConfig()

	// Apply layers in priority order (lowest to highest)
	applyEnvLayer(merged, env)
	applyYAMLLayer(merged, yaml)
	applyExplicitLayer(merged, explicit)

	return merged
}

// applyEnvLayer applies environment variable config (lowest priority for mergeable fields).
func applyEnvLayer(target *Config, env *Config) {
	if env == nil {
		return
	}

	applyStringIfSet(&target.ServiceName, env.ServiceName)
	applyStringIfSet(&target.Endpoint, env.Endpoint)
	applyProtocolIfSet(&target.Protocol, env.Protocol)
	target.Headers = copyMapIfSet(target.Headers, env.Headers)
	target.ResourceAttributes = copyMapIfSet(target.ResourceAttributes, env.ResourceAttributes)
}

// applyYAMLLayer applies YAML config (middle priority for mergeable fields).
func applyYAMLLayer(target *Config, yaml *Config) {
	if yaml == nil {
		return
	}

	applyStringIfSet(&target.ServiceName, yaml.ServiceName)
	applyStringIfSet(&target.ServiceVersion, yaml.ServiceVersion)
	applyStringIfSet(&target.Environment, yaml.Environment)
	applyStringIfSet(&target.Endpoint, yaml.Endpoint)
	applyProtocolIfSet(&target.Protocol, yaml.Protocol)
	target.Headers = mergeMapIfSet(target.Headers, yaml.Headers)
	target.ResourceAttributes = mergeMapIfSet(target.ResourceAttributes, yaml.ResourceAttributes)

	if yaml.Debug != nil {
		target.Debug = yaml.Debug
	}
}

// applyExplicitLayer applies explicit config (highest priority).
func applyExplicitLayer(target *Config, explicit *Config) {
	if explicit == nil {
		return
	}

	// Apply mergeable fields (only if explicitly set)
	applyExplicitString(&target.ServiceName, explicit.ServiceName, defaultServiceName)
	applyStringIfSet(&target.ServiceVersion, explicit.ServiceVersion)
	applyStringIfSet(&target.Environment, explicit.Environment)
	applyStringIfSet(&target.Endpoint, explicit.Endpoint)
	applyProtocolIfSet(&target.Protocol, explicit.Protocol)
	target.Headers = mergeMapIfSet(target.Headers, explicit.Headers)
	target.ResourceAttributes = mergeMapIfSet(target.ResourceAttributes, explicit.ResourceAttributes)

	if explicit.Debug != nil {
		target.Debug = explicit.Debug
	}

	// Copy all other fields directly (they don't come from YAML/env)
	copyExplicitOnlyFields(target, explicit)
}

// copyExplicitOnlyFields copies fields that only come from explicit config.
func copyExplicitOnlyFields(target, explicit *Config) {
	target.Insecure = explicit.Insecure
	target.Sampler = explicit.Sampler
	target.UseAdaptiveSampler = explicit.UseAdaptiveSampler
	target.RateLimiter = explicit.RateLimiter
	target.CircuitBreaker = explicit.CircuitBreaker
	target.PIIRedactor = explicit.PIIRedactor
	target.Subscribers = explicit.Subscribers
	target.BackendPreset = explicit.BackendPreset
	target.SpanExporters = explicit.SpanExporters
	target.SpanProcessors = explicit.SpanProcessors
	target.EventQueueSize = explicit.EventQueueSize
	target.EventFlushInterval = explicit.EventFlushInterval
	target.EventCBThreshold = explicit.EventCBThreshold
	target.EventBackoffMin = explicit.EventBackoffMin
	target.EventBackoffMax = explicit.EventBackoffMax
	target.EventCBReset = explicit.EventCBReset
	target.EventMaxRetries = explicit.EventMaxRetries
	target.EventJitter = explicit.EventJitter
	target.BatchTimeout = explicit.BatchTimeout
	target.MaxQueueSize = explicit.MaxQueueSize
	target.MaxExportBatchSize = explicit.MaxExportBatchSize
	target.MetricsEnabled = explicit.MetricsEnabled
	target.MetricExporters = explicit.MetricExporters
	target.MetricInterval = explicit.MetricInterval
}

// applyStringIfSet sets target to source if source is non-empty.
func applyStringIfSet(target *string, source string) {
	if source != "" {
		*target = source
	}
}

// applyExplicitString sets target if source differs from the default value.
func applyExplicitString(target *string, source, defaultVal string) {
	if source != "" && source != defaultVal {
		*target = source
	}
}

// applyProtocolIfSet sets target protocol if source is non-empty.
func applyProtocolIfSet(target *Protocol, source Protocol) {
	if source != "" {
		*target = source
	}
}

// copyMapIfSet returns a copy of source if non-empty, otherwise returns nil.
func copyMapIfSet(_, source map[string]string) map[string]string {
	if len(source) == 0 {
		return nil
	}
	result := make(map[string]string, len(source))
	for k, v := range source {
		result[k] = v
	}
	return result
}

// mergeMapIfSet merges source into target (source values override target).
func mergeMapIfSet(target, source map[string]string) map[string]string {
	if len(source) == 0 {
		return target
	}
	if target == nil {
		target = make(map[string]string)
	}
	for k, v := range source {
		target[k] = v
	}
	return target
}
