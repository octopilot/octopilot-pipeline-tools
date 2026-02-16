package util

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

const RunConfigFilename = ".github/octopilot.yaml"

type RunConfig struct {
	DefaultRepo string                 `yaml:"default_repo"`
	Tag         string                 `yaml:"tag"`
	Contexts    map[string]ContextOpts `yaml:"contexts"`
}

type ContextOpts struct {
	Ports   []string          `yaml:"ports"`
	Env     map[string]string `yaml:"env"`
	Volumes []string          `yaml:"volumes"`
}

func LoadRunConfig(cwd string) (*RunConfig, error) {
	path := filepath.Join(cwd, RunConfigFilename)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &RunConfig{}, nil
		}
		return nil, err
	}

	var cfg RunConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// GetRunOptionsForContext merges inferred options with config overrides.
func GetRunOptionsForContext(contextName, cwd string, cfg *RunConfig, contextDir string) (hostPorts []string, env map[string]string, volumes []string, containerPort int) {
	if cfg == nil {
		cfg, _ = LoadRunConfig(cwd)
	}

	// Inference
	inferred := InferRunOptions(contextDir)
	containerPort = inferred.ContainerPort
	env = make(map[string]string)
	for k, v := range inferred.Env {
		env[k] = v
	}

	// Overrides from config
	if ctxOpts, ok := cfg.Contexts[contextName]; ok {
		// Env overrides
		for k, v := range ctxOpts.Env {
			env[k] = v
		}

		// Volumes
		volumes = ctxOpts.Volumes

		// Ports
		if len(ctxOpts.Ports) > 0 {
			hostPorts = ctxOpts.Ports
		}
	}

	// If no ports specified in config, caller must auto-assign using containerPort

	return
}
