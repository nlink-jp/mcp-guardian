package transport

import (
	"bufio"
	"fmt"
	"io"
	"os/exec"
)

// processTransport implements Transport by spawning a child process
// and communicating via its stdin/stdout pipes.
type processTransport struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Scanner
	stderr io.ReadCloser
}

// NewProcessTransport spawns the upstream MCP server as a child process.
// The command is executed directly without a shell to prevent command injection.
// Returns a ProcessTransportResult containing the Transport and a stderr reader.
func NewProcessTransport(command string, args []string) (*ProcessTransportResult, error) {
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

	t := &processTransport{
		cmd:    cmd,
		stdin:  stdin,
		stdout: scanner,
		stderr: stderr,
	}

	return &ProcessTransportResult{
		Transport: t,
		Stderr:    stderr,
	}, nil
}

// Send writes a JSON-RPC message line to the process stdin.
func (t *processTransport) Send(data []byte) error {
	_, err := t.stdin.Write(append(data, '\n'))
	return err
}

// ReadLine reads the next line from the process stdout.
func (t *processTransport) ReadLine() ([]byte, bool) {
	if t.stdout.Scan() {
		return t.stdout.Bytes(), true
	}
	return nil, false
}

// Close terminates the child process.
func (t *processTransport) Close() error {
	t.stdin.Close()
	return t.cmd.Wait()
}
