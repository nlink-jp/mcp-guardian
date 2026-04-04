package proxy

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/nlink-jp/mcp-guardian/internal/classify"
	"github.com/nlink-jp/mcp-guardian/internal/config"
	"github.com/nlink-jp/mcp-guardian/internal/governance"
	"github.com/nlink-jp/mcp-guardian/internal/jsonrpc"
	"github.com/nlink-jp/mcp-guardian/internal/metatool"
	"github.com/nlink-jp/mcp-guardian/internal/receipt"
	"github.com/nlink-jp/mcp-guardian/internal/state"
	"github.com/nlink-jp/mcp-guardian/internal/webhook"
)

// Proxy is the core MCP governance proxy.
type Proxy struct {
	cfg         *config.Config
	upstream    *Upstream
	ledger      *receipt.Ledger
	controller  *state.Controller
	authority   *state.Authority
	convergence *governance.ConvergenceTracker
	schemaCache map[string]json.RawMessage // toolName -> inputSchema

	pending map[string]chan *jsonrpc.Message // id -> response channel
	mu      sync.Mutex
	callCount int
}

// Run starts the proxy, piping between agent stdin/stdout and the upstream process.
func Run(cfg *config.Config) error {
	// Initialize state directory
	if err := state.EnsureDir(cfg.StateDir); err != nil {
		return fmt.Errorf("state dir: %w", err)
	}

	ctrl, err := state.LoadOrCreateController(cfg.StateDir)
	if err != nil {
		return fmt.Errorf("controller: %w", err)
	}

	auth, err := state.LoadOrCreateAuthority(cfg.StateDir, ctrl.ID)
	if err != nil {
		return fmt.Errorf("authority: %w", err)
	}

	ledger, err := receipt.NewLedger(cfg.StateDir)
	if err != nil {
		return fmt.Errorf("ledger: %w", err)
	}

	// Set genesis hash if first run
	if auth.GenesisHash == "" && ledger.Seq() > 0 {
		records, err := ledger.LoadAll()
		if err != nil {
			return fmt.Errorf("load receipts for genesis hash: %w", err)
		}
		if len(records) > 0 {
			auth.GenesisHash = records[0].Hash
			state.SaveAuthority(cfg.StateDir, auth)
		}
	}

	up, err := StartUpstream(cfg.Upstream, cfg.UpstreamArgs)
	if err != nil {
		return fmt.Errorf("start upstream: %w", err)
	}
	defer up.Close()

	p := &Proxy{
		cfg:         cfg,
		upstream:    up,
		ledger:      ledger,
		controller:  ctrl,
		authority:   auth,
		convergence: governance.NewConvergenceTracker(),
		schemaCache: make(map[string]json.RawMessage),
		pending:     make(map[string]chan *jsonrpc.Message),
	}

	logStderr("mcp-guardian: proxy started (controller=%s, epoch=%d)\n", ctrl.ID, auth.Epoch)

	// Forward upstream stderr to our stderr
	go func() {
		io.Copy(os.Stderr, up.Stderr())
	}()

	// Read upstream stdout and dispatch responses
	go p.readUpstream()

	// Read agent stdin and route messages
	return p.readAgent()
}

func (p *Proxy) readAgent() error {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		msg, err := jsonrpc.Parse(line)
		if err != nil {
			logStderr("mcp-guardian: invalid JSON from agent: %v\n", err)
			continue
		}

		if err := p.routeAgentMessage(msg, line); err != nil {
			logStderr("mcp-guardian: route error: %v\n", err)
		}
	}

	// Agent closed stdin - session complete
	webhook.Fire(p.cfg.WebhookURLs, webhook.EventSessionComplete, map[string]interface{}{
		"receiptDepth": p.ledger.Seq(),
		"callCount":    p.callCount,
	})

	return scanner.Err()
}

func (p *Proxy) readUpstream() {
	for {
		line, ok := p.upstream.ReadLine()
		if !ok {
			break
		}
		if len(line) == 0 {
			continue
		}

		msg, err := jsonrpc.Parse(line)
		if err != nil {
			logStderr("mcp-guardian: invalid JSON from upstream: %v\n", err)
			continue
		}

		if msg.IsResponse() {
			id := msg.IDString()
			p.mu.Lock()
			ch, exists := p.pending[id]
			if exists {
				delete(p.pending, id)
			}
			p.mu.Unlock()

			if exists {
				ch <- msg
			} else {
				// Unsolicited response, forward as-is
				writeToAgent(line)
			}
		} else {
			// Upstream notification, forward as-is
			writeToAgent(line)
		}
	}
}

func (p *Proxy) routeAgentMessage(msg *jsonrpc.Message, raw []byte) error {
	switch {
	case msg.IsNotification():
		// Forward notifications as-is
		return p.upstream.Send(raw)

	case msg.IsRequest():
		return p.handleRequest(msg, raw)

	default:
		// Unknown, forward as-is
		return p.upstream.Send(raw)
	}
}

func (p *Proxy) handleRequest(msg *jsonrpc.Message, raw []byte) error {
	switch msg.Method {
	case "initialize":
		return p.handleInitialize(msg, raw)
	case "tools/list":
		return p.handleToolsList(msg, raw)
	case "tools/call":
		return p.handleToolsCall(msg, raw)
	default:
		return p.forwardAndRelay(msg, raw)
	}
}

func (p *Proxy) handleInitialize(msg *jsonrpc.Message, raw []byte) error {
	resp, err := p.forwardRequest(msg, raw)
	if err != nil {
		return err
	}

	// Sync session on successful initialize
	if resp.Error == nil {
		state.SyncSession(p.authority)
		state.SaveAuthority(p.cfg.StateDir, p.authority)
		p.convergence.Reset()
		logStderr("mcp-guardian: session synced (epoch=%d)\n", p.authority.ActiveSessionEpoch)
	}

	return writeMessage(resp)
}

func (p *Proxy) handleToolsList(msg *jsonrpc.Message, raw []byte) error {
	resp, err := p.forwardRequest(msg, raw)
	if err != nil {
		return err
	}

	if resp.Error == nil && resp.Result != nil {
		// Parse and cache tool schemas, inject meta-tools
		var result jsonrpc.ToolsListResult
		if json.Unmarshal(resp.Result, &result) == nil {
			// Cache schemas
			for _, tool := range result.Tools {
				if len(tool.InputSchema) > 0 {
					p.schemaCache[tool.Name] = tool.InputSchema
				}
			}

			// Inject meta-tools
			result.Tools = append(result.Tools, metatool.Definitions()...)

			// Re-serialize
			newResult, _ := json.Marshal(result)
			resp.Result = json.RawMessage(newResult)
		}
	}

	return writeMessage(resp)
}

func (p *Proxy) handleToolsCall(msg *jsonrpc.Message, raw []byte) error {
	params, err := jsonrpc.ParseToolCallParams(msg.Params)
	if err != nil {
		return p.upstream.Send(raw)
	}

	// Check for meta-tools
	if metatool.IsMetaTool(params.Name) {
		return p.handleMetaTool(msg, params)
	}

	// Run governance pipeline
	constraints, _ := state.LoadConstraints(p.cfg.StateDir)
	gateResult := governance.RunGates(governance.GateInput{
		ToolName:    params.Name,
		Arguments:   params.Arguments,
		InputSchema: p.schemaCache[params.Name],
		Constraints: constraints,
		Authority:   p.authority,
		CallCount:   p.callCount,
		MaxCalls:    p.cfg.MaxCalls,
		SchemaMode:  p.cfg.SchemaMode,
		Enforcement: p.cfg.Enforcement,
		Convergence: p.convergence,
	})

	if !gateResult.Forward {
		// Blocked - create receipt and return error
		p.recordReceipt(params.Name, params.Arguments, gateResult, "blocked", 0, gateResult.BlockReason)

		webhook.Fire(p.cfg.WebhookURLs, webhook.EventBlocked, map[string]interface{}{
			"toolName": params.Name,
			"target":   gateResult.Target,
			"reason":   gateResult.BlockReason,
		})

		errResp := jsonrpc.NewErrorResponse(msg.ID, -32600, "governance: "+gateResult.BlockReason)
		return writeMessage(errResp)
	}

	// Forward to upstream
	p.callCount++
	p.convergence.RecordCall(params.Name, gateResult.Target)
	start := time.Now()

	resp, err := p.forwardRequest(msg, raw)
	if err != nil {
		return err
	}
	durationMs := time.Since(start).Milliseconds()

	// Analyze response
	outcome := "success"
	errorText := ""
	if resp.Error != nil {
		outcome = "error"
		errorText = resp.Error.Message
	} else if resp.Result != nil {
		if toolResult, err := jsonrpc.ParseToolResult(resp.Result); err == nil && toolResult.IsError {
			outcome = "error"
			if len(toolResult.Content) > 0 {
				errorText = toolResult.Content[0].Text
			}
		}
	}

	// On failure: seed constraint and record failure for convergence
	if outcome == "error" && errorText != "" {
		sig := classify.ExtractSignature(errorText)
		state.AddConstraint(p.cfg.StateDir, params.Name, gateResult.Target, sig, truncate(errorText, 200), 3600000)
		p.convergence.RecordFailure(sig)

		// Check for loop after recording failure
		signal := p.convergence.Signal(params.Name, gateResult.Target, sig)
		if signal == governance.SignalLoop {
			webhook.Fire(p.cfg.WebhookURLs, webhook.EventLoopDetected, map[string]interface{}{
				"toolName":  params.Name,
				"target":    gateResult.Target,
				"signature": sig,
			})
		}
	}

	// Record receipt
	p.recordReceipt(params.Name, params.Arguments, gateResult, outcome, durationMs, errorText)

	return writeMessage(resp)
}

func (p *Proxy) handleMetaTool(msg *jsonrpc.Message, params *jsonrpc.ToolCallParams) error {
	ctx := &metatool.Context{
		StateDir:    p.cfg.StateDir,
		Controller:  p.controller,
		Authority:   p.authority,
		Ledger:      p.ledger,
		Convergence: p.convergence,
	}

	result, err := metatool.Handle(ctx, params.Name, params.Arguments)
	if err != nil {
		errResp := jsonrpc.NewErrorResponse(msg.ID, -32603, err.Error())
		return writeMessage(errResp)
	}

	resp, err := jsonrpc.NewResultResponse(msg.ID, result)
	if err != nil {
		errResp := jsonrpc.NewErrorResponse(msg.ID, -32603, err.Error())
		return writeMessage(errResp)
	}
	return writeMessage(resp)
}

func (p *Proxy) forwardAndRelay(msg *jsonrpc.Message, raw []byte) error {
	resp, err := p.forwardRequest(msg, raw)
	if err != nil {
		return err
	}
	return writeMessage(resp)
}

func (p *Proxy) forwardRequest(msg *jsonrpc.Message, raw []byte) (*jsonrpc.Message, error) {
	id := msg.IDString()
	ch := make(chan *jsonrpc.Message, 1)

	p.mu.Lock()
	p.pending[id] = ch
	p.mu.Unlock()

	if err := p.upstream.Send(raw); err != nil {
		p.mu.Lock()
		delete(p.pending, id)
		p.mu.Unlock()
		return nil, fmt.Errorf("send to upstream: %w", err)
	}

	// Wait for response with timeout
	timeout := time.Duration(p.cfg.TimeoutMs) * time.Millisecond
	select {
	case resp := <-ch:
		return resp, nil
	case <-time.After(timeout):
		p.mu.Lock()
		delete(p.pending, id)
		p.mu.Unlock()
		return jsonrpc.NewErrorResponse(msg.ID, -32603, "upstream timeout"), nil
	}
}

func (p *Proxy) recordReceipt(toolName string, args map[string]interface{}, gate governance.GateResult, outcome string, durationMs int64, errorText string) {
	r := &receipt.Record{
		Timestamp:    time.Now().UnixMilli(),
		ToolName:     toolName,
		Arguments:    args,
		Target:       gate.Target,
		MutationType: gate.MutationType,
		Outcome:      outcome,
		DurationMs:   durationMs,
		ConvergenceSignal: gate.ConvergenceSignal,
	}

	if !gate.ConstraintResult.Passed {
		r.ConstraintCheck = &receipt.CheckResult{
			Passed: false,
			Reason: gate.ConstraintResult.Reason,
		}
	}
	if !gate.AuthorityResult.Passed {
		r.AuthorityCheck = &receipt.CheckResult{
			Passed: false,
			Reason: gate.AuthorityResult.Reason,
		}
	}
	if errorText != "" {
		r.ErrorText = errorText
		r.FailureKind = "app_failure"
	}

	// Generate title and summary
	r.Title = fmt.Sprintf("%s on %s", toolName, gate.Target)
	if outcome == "blocked" {
		r.Summary = "Blocked: " + errorText
	} else if outcome == "error" {
		r.Summary = "Failed: " + truncate(errorText, 100)
	} else {
		r.Summary = fmt.Sprintf("OK (%dms)", durationMs)
	}

	if err := p.ledger.Append(r); err != nil {
		logStderr("mcp-guardian: receipt write error: %v\n", err)
	}

	// Set genesis hash on first receipt
	if p.ledger.Seq() == 1 && p.authority.GenesisHash == "" {
		p.authority.GenesisHash = r.Hash
		state.SaveAuthority(p.cfg.StateDir, p.authority)
	}
}

func writeMessage(msg *jsonrpc.Message) error {
	data, err := jsonrpc.Marshal(msg)
	if err != nil {
		return err
	}
	return writeToAgent(data)
}

func writeToAgent(data []byte) error {
	_, err := os.Stdout.Write(append(data, '\n'))
	return err
}

func logStderr(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, format, args...)
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
