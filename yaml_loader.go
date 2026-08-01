package autotel

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// yamlConfig represents the structure of the YAML configuration file
type yamlConfig struct {
	Service struct {
		Name        string `yaml:"name"`
		Version     string `yaml:"version"`
		Environment string `yaml:"environment"`
	} `yaml:"service"`
	Exporter struct {
		Endpoint string            `yaml:"endpoint"`
		Protocol string            `yaml:"protocol"`
		Headers  map[string]string `yaml:"headers"`
	} `yaml:"exporter"`
	Resource map[string]interface{} `yaml:"resource"`
	Debug    *bool                  `yaml:"debug"`
}

// envVarPattern matches ${env:VAR_NAME} and ${env:VAR_NAME:-default}
var envVarPattern = regexp.MustCompile(`\$\{env:([A-Za-z_][A-Za-z0-9_]*)(?::-([^}]*))?\}`)

// substituteEnvVars replaces ${env:VAR} and ${env:VAR:-default} in a string
func substituteEnvVars(value string) string {
	return envVarPattern.ReplaceAllStringFunc(value, func(match string) string {
		matches := envVarPattern.FindStringSubmatch(match)
		if len(matches) < 2 {
			return match
		}
		varName := matches[1]
		defaultValue := ""
		if len(matches) >= 3 {
			defaultValue = matches[2]
		}

		envValue := os.Getenv(varName)
		if envValue != "" {
			return envValue
		}
		if defaultValue != "" {
			return defaultValue
		}
		// Warn but don't fail - return empty string
		fmt.Fprintf(os.Stderr, "[autotel] Warning: Environment variable %s not set and no default provided\n", varName)
		return ""
	})
}

// substituteEnvVarsDeep recursively substitutes env vars in any value
func substituteEnvVarsDeep(value interface{}) interface{} {
	switch v := value.(type) {
	case string:
		return substituteEnvVars(v)
	case []interface{}:
		result := make([]interface{}, len(v))
		for i, item := range v {
			result[i] = substituteEnvVarsDeep(item)
		}
		return result
	case map[string]interface{}:
		result := make(map[string]interface{})
		for k, val := range v {
			result[k] = substituteEnvVarsDeep(val)
		}
		return result
	case map[interface{}]interface{}:
		result := make(map[string]interface{})
		for k, val := range v {
			key := fmt.Sprintf("%v", k)
			result[key] = substituteEnvVarsDeep(val)
		}
		return result
	default:
		return value
	}
}

// findConfigFile finds the YAML config file using priority:
// 1. AUTOTEL_CONFIG_FILE env var (explicit path)
// 2. autotel.yaml in current working directory
// 3. autotel.yml in current working directory
func findConfigFile() (string, error) {
	// Check env var first (explicit takes priority)
	if envPath := os.Getenv("AUTOTEL_CONFIG_FILE"); envPath != "" {
		resolved, err := filepath.Abs(envPath)
		if err != nil {
			return "", fmt.Errorf("invalid AUTOTEL_CONFIG_FILE path: %w", err)
		}
		if _, err := os.Stat(resolved); err == nil {
			return resolved, nil
		}
		fmt.Fprintf(os.Stderr, "[autotel] Warning: Config file not found: %s\n", envPath)
		return "", nil // Not an error - YAML config is optional
	}

	// Auto-discover autotel.yaml in cwd
	cwd, err := os.Getwd()
	if err != nil {
		return "", nil // Can't determine cwd, but not an error
	}

	conventionPath := filepath.Join(cwd, "autotel.yaml")
	if _, err := os.Stat(conventionPath); err == nil {
		return conventionPath, nil
	}

	// Also check .yml extension
	altPath := filepath.Join(cwd, "autotel.yml")
	if _, err := os.Stat(altPath); err == nil {
		return altPath, nil
	}

	return "", nil // No config file found (not an error)
}

// yamlToConfig converts a yamlConfig to a partial Config
func yamlToConfig(yaml *yamlConfig) *Config {
	cfg := &Config{}

	// Service configuration
	if yaml.Service.Name != "" {
		cfg.ServiceName = yaml.Service.Name
	}
	if yaml.Service.Version != "" {
		cfg.ServiceVersion = yaml.Service.Version
	}
	if yaml.Service.Environment != "" {
		cfg.Environment = yaml.Service.Environment
	}

	// Exporter configuration
	if yaml.Exporter.Endpoint != "" {
		cfg.Endpoint = sanitizeEndpoint(yaml.Exporter.Endpoint)
	}
	if yaml.Exporter.Protocol != "" {
		switch strings.ToLower(yaml.Exporter.Protocol) {
		case "http":
			cfg.Protocol = ProtocolHTTP
		case "grpc":
			cfg.Protocol = ProtocolGRPC
		}
	}
	if len(yaml.Exporter.Headers) > 0 {
		cfg.Headers = yaml.Exporter.Headers
	}

	// Resource attributes
	if len(yaml.Resource) > 0 {
		// Convert map[string]interface{} to map[string]string for resource attributes
		cfg.ResourceAttributes = make(map[string]string)
		for k, v := range yaml.Resource {
			cfg.ResourceAttributes[k] = fmt.Sprintf("%v", v)
		}
	}

	// Debug mode
	if yaml.Debug != nil {
		cfg.Debug = yaml.Debug
	}

	return cfg
}

// loadYamlConfigFromAutotel loads and parses YAML config file (auto-discovery)
// Returns nil if no config file found (not an error - YAML config is optional)
func loadYamlConfigFromAutotel() (*Config, error) {
	filePath, err := findConfigFile()
	if err != nil {
		return nil, err
	}
	if filePath == "" {
		return nil, nil // No config file found (not an error)
	}

	return loadYamlConfigFromFile(filePath)
}

// loadYamlConfigFromFile loads YAML config from a specific file path
func loadYamlConfigFromFile(filePath string) (*Config, error) {
	// #nosec G304 -- callers intentionally select the local configuration path.
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file %s: %w", filePath, err)
	}

	// First, substitute environment variables in the raw YAML string
	// This handles ${env:VAR} and ${env:VAR:-default} patterns
	substitutedContent := substituteEnvVars(string(data))

	// Now parse the substituted YAML
	var yamlCfg yamlConfig
	if err := yaml.Unmarshal([]byte(substitutedContent), &yamlCfg); err != nil {
		return nil, fmt.Errorf("failed to parse YAML config from %s: %w", filePath, err)
	}

	// Recursively substitute env vars in nested structures (for map values, etc.)
	// This handles cases where env vars might be in nested maps/arrays
	yamlCfg.Service.Name = substituteEnvVars(yamlCfg.Service.Name)
	yamlCfg.Service.Version = substituteEnvVars(yamlCfg.Service.Version)
	yamlCfg.Service.Environment = substituteEnvVars(yamlCfg.Service.Environment)
	yamlCfg.Exporter.Endpoint = substituteEnvVars(yamlCfg.Exporter.Endpoint)
	yamlCfg.Exporter.Protocol = substituteEnvVars(yamlCfg.Exporter.Protocol)

	// Substitute in headers map
	if len(yamlCfg.Exporter.Headers) > 0 {
		newHeaders := make(map[string]string)
		for k, v := range yamlCfg.Exporter.Headers {
			newHeaders[k] = substituteEnvVars(v)
		}
		yamlCfg.Exporter.Headers = newHeaders
	}

	// Substitute in resource attributes
	if len(yamlCfg.Resource) > 0 {
		newResource := make(map[string]interface{})
		for k, v := range yamlCfg.Resource {
			if strVal, ok := v.(string); ok {
				newResource[k] = substituteEnvVars(strVal)
			} else {
				newResource[k] = substituteEnvVarsDeep(v)
			}
		}
		yamlCfg.Resource = newResource
	}

	return yamlToConfig(&yamlCfg), nil
}
