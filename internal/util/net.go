package util

import (
	"fmt"
	"net"
)

// FindFreePort finds an available TCP port starting from startPort.
func FindFreePort(startPort, maxTries int) (int, error) {
	for i := 0; i < maxTries; i++ {
		port := startPort + i
		addr := fmt.Sprintf("localhost:%d", port)
		l, err := net.Listen("tcp", addr)
		if err == nil {
			_ = l.Close()
			return port, nil
		}
	}
	return 0, fmt.Errorf("no free port found")
}
