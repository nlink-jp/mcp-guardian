package proxy

import (
	"bufio"
	"fmt"
	"io"
	"os/exec"
)

// Upstream manages the child MCP server process.
type Upstream struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Scanner
	stderr io.ReadCloser
}

// StartUpstream spawns the upstream MCP server as a child process.
// The command is executed directly without a shell to prevent command injection.
func StartUpstream(command string, args []string) (*Upstream, error) {
	cmd := exec.Command(command, args...)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start upstream: %w", err)
	}

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024) // 1MB buffer

	return &Upstream{
		cmd:    cmd,
		stdin:  stdin,
		stdout: scanner,
		stderr: stderr,
	}, nil
}

// Send writes a JSON-RPC message line to the upstream's stdin.
func (u *Upstream) Send(data []byte) error {
	_, err := u.stdin.Write(append(data, '\n'))
	return err
}

// ReadLine reads the next line from the upstream's stdout.
// Returns false when no more data is available.
func (u *Upstream) ReadLine() ([]byte, bool) {
	if u.stdout.Scan() {
		return u.stdout.Bytes(), true
	}
	return nil, false
}

// Close terminates the upstream process.
func (u *Upstream) Close() error {
	u.stdin.Close()
	return u.cmd.Wait()
}

// Stderr returns the stderr reader for logging upstream errors.
func (u *Upstream) Stderr() io.ReadCloser {
	return u.stderr
}
