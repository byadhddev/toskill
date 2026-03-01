package headless

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"time"
)

var proc *os.Process

// EnsureRunning starts a headless copilot CLI server if one isn't already running.
// Returns the address (host:port) the server is on.
func EnsureRunning(addr string) string {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		host = "localhost"
		portStr = addr
	}
	if host == "" {
		host = "localhost"
	}

	target := net.JoinHostPort(host, portStr)

	// Check if already running
	conn, err := net.DialTimeout("tcp", target, 2*time.Second)
	if err == nil {
		conn.Close()
		fmt.Fprintf(os.Stderr, "🔌 Copilot CLI already running at %s\n", target)
		return target
	}

	port, _ := strconv.Atoi(portStr)
	if port == 0 {
		port = 44321
	}

	// Start headless server as background process
	fmt.Fprintf(os.Stderr, "🚀 Starting Copilot CLI (headless) on port %d...\n", port)
	cmd := exec.Command("copilot", "--headless", "--port", strconv.Itoa(port))
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "   ❌ Failed to start: %v\n", err)
		fmt.Fprintf(os.Stderr, "   Ensure 'copilot' is installed: https://github.com/github/copilot-cli\n")
		return ""
	}
	proc = cmd.Process

	// Wait for ready
	target = net.JoinHostPort(host, strconv.Itoa(port))
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", target, 500*time.Millisecond)
		if err == nil {
			conn.Close()
			fmt.Fprintf(os.Stderr, "   ✅ Copilot CLI ready (pid %d)\n", cmd.Process.Pid)
			return target
		}
		time.Sleep(500 * time.Millisecond)
	}

	fmt.Fprintf(os.Stderr, "   ❌ Copilot CLI didn't become ready in 15s\n")
	cmd.Process.Kill()
	proc = nil
	return ""
}

// Stop kills the background headless server if we started one.
func Stop() {
	if proc != nil {
		proc.Kill()
		proc.Wait()
		proc = nil
	}
}
