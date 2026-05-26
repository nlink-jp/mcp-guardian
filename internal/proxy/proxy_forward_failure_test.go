package proxy

import (
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/nlink-jp/mcp-guardian/internal/config"
	"github.com/nlink-jp/mcp-guardian/internal/jsonrpc"
	"github.com/nlink-jp/mcp-guardian/internal/receipt"
	"github.com/nlink-jp/mcp-guardian/internal/state"
)

// sendErrTransport is an upstream whose Send always fails — modelling
// an SSE transport that can't obtain an auth token because the stored
// OAuth token has expired (the Slack-MCP case in ADR-0002).
type sendErrTransport struct{ err error }

func (t sendErrTransport) Send([]byte) error        { return t.err }
func (t sendErrTransport) ReadLine() ([]byte, bool) { return nil, false }
func (t sendErrTransport) Close() error             { return nil }

// TestReadAgent_RequestForwardFailureRepliesError verifies ADR-0002:
// when forwarding a client request upstream fails, readAgent replies to
// the client with a JSON-RPC error (carrying the underlying reason)
// instead of leaving it to hang until its own timeout.
func TestReadAgent_RequestForwardFailureRepliesError(t *testing.T) {
	stateDir := t.TempDir()
	if err := state.EnsureDir(stateDir); err != nil {
		t.Fatal(err)
	}
	ledger, err := receipt.NewLedger(stateDir)
	if err != nil {
		t.Fatal(err)
	}

	p := &Proxy{
		cfg:      &config.Config{StateDir: stateDir, TimeoutMs: 5000},
		upstream: sendErrTransport{err: fmt.Errorf("obtain auth token: access token expired and no refresh token available (run --login again)")},
		ledger:   ledger,
		pending:  make(map[string]chan *jsonrpc.Message),
	}

	// readAgent reads os.Stdin and writes os.Stdout directly; swap both
	// for the duration. Not parallel — these are process globals.
	origIn, origOut := os.Stdin, os.Stdout
	inR, inW, _ := os.Pipe()
	outR, outW, _ := os.Pipe()
	os.Stdin, os.Stdout = inR, outW

	go func() {
		io.WriteString(inW, `{"jsonrpc":"2.0","id":"1","method":"initialize","params":{}}`+"\n")
		inW.Close() // EOF ends the readAgent scanner loop
	}()

	_ = p.readAgent()
	outW.Close()
	out, _ := io.ReadAll(outR)
	os.Stdin, os.Stdout = origIn, origOut // restore before any t logging

	line := strings.TrimSpace(string(out))
	if line == "" {
		t.Fatal("client received no response — request would hang until its own timeout")
	}
	msg, err := jsonrpc.Parse([]byte(line))
	if err != nil {
		t.Fatalf("parse response %q: %v", line, err)
	}
	// IDString returns the raw JSON form, i.e. `"1"` (quotes included).
	if got := msg.IDString(); got != `"1"` {
		t.Errorf("response id = %q, want `\"1\"`", got)
	}
	if msg.Error == nil {
		t.Fatalf("expected a JSON-RPC error response, got %q", line)
	}
	if msg.Error.Code != -32603 {
		t.Errorf("error code = %d, want -32603", msg.Error.Code)
	}
	if !strings.Contains(msg.Error.Message, "access token expired") {
		t.Errorf("error message = %q, want it to carry the upstream reason", msg.Error.Message)
	}
}
