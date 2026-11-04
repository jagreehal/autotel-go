package autotel

import (
	"strings"

	"go.opentelemetry.io/otel/sdk/trace"
)

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
	target.signals = env.signals
	target.LogFile = env.LogFile
	if env.samplerChosen {
		target.Sampler = env.Sampler
		target.samplerChosen = true
		target.UseAdaptiveSampler = false
	}
}

// overrideSignalEndpoints makes a higher layer's Endpoint win over the
// per-signal variables below it. OTEL_*_EXPORTER=none stays: it is an off switch.
func overrideSignalEndpoints(target *Config) {
	for _, signal := range []*signalEnv{&target.signals.traces, &target.signals.metrics, &target.signals.logs} {
		signal.Endpoint = ""
		signal.Headers = nil
	}
}

// applyYAMLLayer applies YAML config (middle priority for mergeable fields).
func applyYAMLLayer(target *Config, yaml *Config) {
	if yaml == nil {
		return
	}

	yieldToResourceName(target, yaml)
	applyStringIfSet(&target.ServiceName, yaml.ServiceName)
	applyStringIfSet(&target.ServiceVersion, yaml.ServiceVersion)
	applyStringIfSet(&target.Environment, yaml.Environment)
	applyStringIfSet(&target.Endpoint, yaml.Endpoint)
	if yaml.Endpoint != "" {
		overrideSignalEndpoints(target)
	}
	applyProtocolIfSet(&target.Protocol, yaml.Protocol)
	if yaml.Protocol != "" {
		target.protocolChosen = true
	}
	target.Headers = mergeHeaders(target.Headers, yaml.Headers)
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
	target.signals = layered.signals
	if explicit.LogFile == nil {
		target.LogFile = layered.LogFile
	}

	// A sampler left at its default doesn't outrank OTEL_TRACES_SAMPLER. Any other
	// was chosen, including one set on the struct or by a vendor preset.
	if !explicit.samplerChosen && samplerIsDefault(explicit.Sampler) {
		target.Sampler = layered.Sampler
		target.samplerChosen = layered.samplerChosen
	} else {
		target.samplerChosen = true
	}

	// Explicit still wins over both lower layers.
	yieldToResourceName(target, explicit)
	applyExplicitString(&target.ServiceName, explicit.ServiceName, defaultServiceName)
	applyChosenString(&target.ServiceVersion, explicit.ServiceVersion, defaultServiceVersion, explicit.versionChosen)
	applyChosenString(&target.Environment, explicit.Environment, defaultEnvironment, explicit.environmentChosen)
	applyStringIfSet(&target.Endpoint, explicit.Endpoint)
	if explicit.Endpoint != "" {
		overrideSignalEndpoints(target)
	}
	// Protocol has a default too, so only a choice outranks the lower layers.
	target.protocolChosen = layered.protocolChosen
	if explicit.protocolChosen || (explicit.Protocol != "" && explicit.Protocol != ProtocolHTTP) {
		target.Protocol = explicit.Protocol
		target.protocolChosen = true
	}
	target.Headers = mergeHeaders(target.Headers, explicit.Headers)
	target.ResourceAttributes = mergeMapIfSet(target.ResourceAttributes, explicit.ResourceAttributes)

	if explicit.Debug != nil {
		target.Debug = explicit.Debug
	}
}

// samplerIsDefault reports whether sampler is unset or the one defaultConfig
// installs. Samplers aren't comparable, so this goes by description.
func samplerIsDefault(sampler trace.Sampler) bool {
	return sampler == nil || sampler.Description() == defaultSampler().Description()
}

// yieldToResourceName lets a layer that names the service through its resource
// attributes outrank a ServiceName from the layers below it.
func yieldToResourceName(target, layer *Config) {
	if _, named := layer.ResourceAttributes["service.name"]; named &&
		(layer.ServiceName == "" || layer.ServiceName == defaultServiceName) {
		target.ServiceName = defaultServiceName
	}
}

// applyChosenString sets target if source was chosen or differs from the default.
func applyChosenString(target *string, source, defaultVal string, chosen bool) {
	if source != "" && (chosen || source != defaultVal) {
		*target = source
	}
}

// mergeHeaders is mergeMapIfSet for HTTP headers, whose names ignore case: left
// as two keys, the winner would come down to map iteration order.
func mergeHeaders(target, source map[string]string) map[string]string {
	for key := range source {
		for existing := range target {
			if existing != key && strings.EqualFold(existing, key) {
				delete(target, existing)
			}
		}
	}
	return mergeMapIfSet(target, source)
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
