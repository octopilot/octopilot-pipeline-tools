package cmd

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// propagationTimeout is --propagation-timeout, else $OP_PROPAGATION_TIMEOUT (a Go duration such as 10m, or plain
// seconds), else the flag's default. The env lets the octopilot action set it without changing op's command line.
func propagationTimeout(cmd *cobra.Command) time.Duration {
	d, _ := cmd.Flags().GetDuration("propagation-timeout")
	if cmd.Flags().Changed("propagation-timeout") {
		return d
	}
	if v := strings.TrimSpace(os.Getenv("OP_PROPAGATION_TIMEOUT")); v != "" {
		if env, err := parseDurationOrSeconds(v); err == nil && env > 0 {
			return env
		}
		fmt.Printf("warning: OP_PROPAGATION_TIMEOUT=%q is not a duration; using %s\n", v, d)
	}
	return d
}

// fanoutParallel is --fanout-parallel, else $OP_FANOUT_PARALLEL, else the flag's default.
func fanoutParallel(cmd *cobra.Command) int {
	n, _ := cmd.Flags().GetInt("fanout-parallel")
	if cmd.Flags().Changed("fanout-parallel") {
		return n
	}
	if v := strings.TrimSpace(os.Getenv("OP_FANOUT_PARALLEL")); v != "" {
		if env, err := strconv.Atoi(v); err == nil && env > 0 {
			return env
		}
		fmt.Printf("warning: OP_FANOUT_PARALLEL=%q is not a positive number; using %d\n", v, n)
	}
	return n
}

func parseDurationOrSeconds(v string) (time.Duration, error) {
	if secs, err := strconv.Atoi(v); err == nil {
		return time.Duration(secs) * time.Second, nil
	}
	return time.ParseDuration(v)
}
