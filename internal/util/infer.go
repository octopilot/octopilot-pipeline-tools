package util

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const DefaultContainerPort = 8080

// RunOptions holds inferred configuration
type RunOptions struct {
	ContainerPort int
	Env           map[string]string
}

// InferRunOptions determines port and env from context directory.
func InferRunOptions(contextDir string) RunOptions {
	defaults := RunOptions{
		ContainerPort: DefaultContainerPort,
		Env:           map[string]string{"PORT": "8080"},
	}

	// 1. Procfile
	if port, _ := inferFromProcfile(filepath.Join(contextDir, "Procfile")); port != 0 {
		return RunOptions{
			ContainerPort: port,
			Env:           map[string]string{"PORT": fmtInt(port)},
		}
	}

	// 2. project.toml (Buildpacks) -> Default 8080
	if _, err := os.Stat(filepath.Join(contextDir, "project.toml")); err == nil {
		return defaults
	}

	// 3. Dockerfile -> EXPOSE
	if port := inferFromDockerfile(filepath.Join(contextDir, "Dockerfile")); port != 0 {
		return RunOptions{
			ContainerPort: port,
			Env:           map[string]string{"PORT": fmtInt(port)},
		}
	}

	// 4. nginx.conf -> listen
	if port := inferFromNginx(filepath.Join(contextDir, "nginx.conf")); port != 0 {
		return RunOptions{
			ContainerPort: port,
			Env:           map[string]string{"PORT": fmtInt(port)},
		}
	}

	return defaults
}

func inferFromProcfile(path string) (int, map[string]string) {
	content, err := os.ReadFile(path)
	if err != nil {
		return 0, nil
	}
	text := string(content)

	// Find web process
	var webLine string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) == 2 && strings.TrimSpace(strings.ToLower(parts[0])) == "web" {
			webLine = strings.TrimSpace(parts[1])
			break
		}
	}
	// Fallback to first line if no web
	if webLine == "" {
		for _, line := range strings.Split(text, "\n") {
			line = strings.TrimSpace(line)
			if line != "" && !strings.HasPrefix(line, "#") && strings.Contains(line, ":") {
				parts := strings.SplitN(line, ":", 2)
				webLine = strings.TrimSpace(parts[1])
				break
			}
		}
	}

	if webLine == "" {
		return 0, nil
	}

	// Regex for PORT
	// ${PORT:-8080}
	reEnv := regexp.MustCompile(`\$\{PORT:-\s*(\d+)\}`)
	if m := reEnv.FindStringSubmatch(webLine); len(m) > 1 {
		return parseInt(m[1]), nil
	}

	// --port 8080 or -p 8080
	reFlag := regexp.MustCompile(`(?:--port|-p)\s+(\d+)`)
	if m := reFlag.FindStringSubmatch(webLine); len(m) > 1 {
		return parseInt(m[1]), nil
	}

	return 0, nil
}

func inferFromDockerfile(path string) int {
	content, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	re := regexp.MustCompile(`(?i)EXPOSE\s+(\d+)`)
	if m := re.FindStringSubmatch(string(content)); len(m) > 1 {
		return parseInt(m[1])
	}
	return 0
}

func inferFromNginx(path string) int {
	content, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	re := regexp.MustCompile(`listen\s+(\d+)\s*;`)
	if m := re.FindStringSubmatch(string(content)); len(m) > 1 {
		return parseInt(m[1])
	}
	return 0
}

func parseInt(s string) int {
	// simple atoi helper, ignoring error (returns 0)
	var i int
	_, _ = fmt.Sscanf(s, "%d", &i)
	return i
}

func fmtInt(i int) string {
	return fmt.Sprintf("%d", i)
}
