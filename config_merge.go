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
//
// The explicit config is taken wholesale rather than field by field. An earlier
// version enumerated the fields to copy, which meant every new Config field was
// silently discarded until someone remembered to extend the list — WithSpanFilter
// and WithTailSampling both shipped that way and did nothing. Copying everything
// and then restoring only the fields that env and YAML also feed inverts that: a
// new field is carried by default and cannot be forgotten.
func applyExplicitLayer(target *Config, explicit *Config) {
	if explicit == nil {
		return
	}

	// Fields resolved from the env and YAML layers, which must survive the copy.
	layered := *target

	*target = *explicit

	target.ServiceName = layered.ServiceName
	target.ServiceVersion = layered.ServiceVersion
	target.Environment = layered.Environment
	target.Endpoint = layered.Endpoint
	target.Protocol = layered.Protocol
	target.Headers = layered.Headers
	target.ResourceAttributes = layered.ResourceAttributes
	target.Debug = layered.Debug

	// Explicit still wins over both lower layers.
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
