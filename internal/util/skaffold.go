package util

import (
	"os"

	"gopkg.in/yaml.v3"
)

type SkaffoldConfig struct {
	Build struct {
		Artifacts []Artifact `yaml:"artifacts"`
	} `yaml:"build"`
}

type Artifact struct {
	Image   string `yaml:"image"`
	Context string `yaml:"context"`
}

// ParseSkaffoldArtifacts reads skaffold.yaml and returns artifacts.
func ParseSkaffoldArtifacts(path string) ([]Artifact, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var config SkaffoldConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, err
	}

	return config.Build.Artifacts, nil
}
