package metatool

import (
	"encoding/json"

	"github.com/nlink-jp/mcp-guardian/internal/governance"
	"github.com/nlink-jp/mcp-guardian/internal/jsonrpc"
	"github.com/nlink-jp/mcp-guardian/internal/receipt"
	"github.com/nlink-jp/mcp-guardian/internal/state"
)

// MetaTool names.
const (
	GovernanceStatus            = "governance_status"
	GovernanceBumpAuthority     = "governance_bump_authority"
	GovernanceDeclareIntent     = "governance_declare_intent"
	GovernanceClearIntent       = "governance_clear_intent"
	GovernanceConvergenceStatus = "governance_convergence_status"
)

// IsMetaTool returns true if the tool name is a governance meta-tool.
func IsMetaTool(name string) bool {
	switch name {
	case GovernanceStatus, GovernanceBumpAuthority,
		GovernanceDeclareIntent, GovernanceClearIntent,
		GovernanceConvergenceStatus:
		return true
	}
	return false
}

// Definitions returns the tool definitions to inject into tools/list responses.
func Definitions() []jsonrpc.ToolInfo {
	return []jsonrpc.ToolInfo{
		{
			Name:        GovernanceStatus,
			Description: "Inspect governance state: controller ID, epoch, active constraints, and receipt chain depth.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
		},
		{
			Name:        GovernanceBumpAuthority,
			Description: "Advance the authority epoch, invalidating the current session. Use when the agent's context may be stale.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
		},
		{
			Name:        GovernanceDeclareIntent,
			Description: "Declare a goal and predicates for containment attribution. Mutating calls will be checked against declared intent.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"goal":{"type":"string","description":"The goal of the current task"},"predicates":{"type":"array","items":{"type":"object","properties":{"type":{"type":"string"},"fields":{"type":"object"}}}}},"required":["goal"]}`),
		},
		{
			Name:        GovernanceClearIntent,
			Description: "Clear the currently declared intent.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
		},
		{
			Name:        GovernanceConvergenceStatus,
			Description: "Inspect loop detection state: failure counts, tool call repetitions, and window size.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
		},
	}
}

// Context holds the state needed to handle meta-tool calls.
type Context struct {
	StateDir    string
	Controller  *state.Controller
	Authority   *state.Authority
	Ledger      *receipt.Ledger
	Convergence *governance.ConvergenceTracker
}

// Handle processes a meta-tool call and returns the result as a JSON-RPC tool result.
func Handle(ctx *Context, toolName string, args map[string]interface{}) (interface{}, error) {
	switch toolName {
	case GovernanceStatus:
		return handleStatus(ctx)
	case GovernanceBumpAuthority:
		return handleBumpAuthority(ctx)
	case GovernanceDeclareIntent:
		return handleDeclareIntent(ctx, args)
	case GovernanceClearIntent:
		return handleClearIntent(ctx)
	case GovernanceConvergenceStatus:
		return handleConvergenceStatus(ctx)
	}
	return nil, nil
}

func handleStatus(ctx *Context) (interface{}, error) {
	constraints, _ := state.LoadConstraints(ctx.StateDir)
	intent, _ := state.LoadIntent(ctx.StateDir)

	activeConstraints := make([]map[string]interface{}, 0)
	for _, c := range constraints {
		activeConstraints = append(activeConstraints, map[string]interface{}{
			"id":       c.ID,
			"toolName": c.ToolName,
			"target":   c.Target,
			"signature": c.FailureSignature,
		})
	}

	status := map[string]interface{}{
		"controllerId":  ctx.Controller.ID,
		"epoch":         ctx.Authority.Epoch,
		"sessionEpoch":  ctx.Authority.ActiveSessionEpoch,
		"constraints":   activeConstraints,
		"receiptDepth":  ctx.Ledger.Seq(),
		"genesisHash":   ctx.Authority.GenesisHash,
		"hasIntent":     intent != nil,
	}

	return jsonrpc.ToolResult{
		Content: []jsonrpc.ToolContent{{Type: "text", Text: mustJSON(status)}},
	}, nil
}

func handleBumpAuthority(ctx *Context) (interface{}, error) {
	state.BumpEpoch(ctx.Authority)
	if err := state.SaveAuthority(ctx.StateDir, ctx.Authority); err != nil {
		return jsonrpc.ToolResult{
			Content: []jsonrpc.ToolContent{{Type: "text", Text: "failed to bump authority: " + err.Error()}},
			IsError: true,
		}, nil
	}
	result := map[string]interface{}{
		"newEpoch":  ctx.Authority.Epoch,
		"bumpedAt":  ctx.Authority.LastBumpedAt,
	}
	return jsonrpc.ToolResult{
		Content: []jsonrpc.ToolContent{{Type: "text", Text: mustJSON(result)}},
	}, nil
}

func handleDeclareIntent(ctx *Context, args map[string]interface{}) (interface{}, error) {
	goal, _ := args["goal"].(string)
	if goal == "" {
		return jsonrpc.ToolResult{
			Content: []jsonrpc.ToolContent{{Type: "text", Text: "goal is required"}},
			IsError: true,
		}, nil
	}

	var predicates []state.Predicate
	if preds, ok := args["predicates"].([]interface{}); ok {
		for _, p := range preds {
			if pm, ok := p.(map[string]interface{}); ok {
				pred := state.Predicate{
					Type: stringVal(pm, "type"),
				}
				if fields, ok := pm["fields"].(map[string]interface{}); ok {
					pred.Fields = fields
				}
				predicates = append(predicates, pred)
			}
		}
	}

	intent, err := state.SaveIntent(ctx.StateDir, goal, predicates)
	if err != nil {
		return jsonrpc.ToolResult{
			Content: []jsonrpc.ToolContent{{Type: "text", Text: "failed to save intent: " + err.Error()}},
			IsError: true,
		}, nil
	}

	result := map[string]interface{}{
		"goal":    intent.Goal,
		"version": intent.Version,
	}
	return jsonrpc.ToolResult{
		Content: []jsonrpc.ToolContent{{Type: "text", Text: mustJSON(result)}},
	}, nil
}

func handleClearIntent(ctx *Context) (interface{}, error) {
	if err := state.ClearIntent(ctx.StateDir); err != nil {
		return jsonrpc.ToolResult{
			Content: []jsonrpc.ToolContent{{Type: "text", Text: "failed to clear intent: " + err.Error()}},
			IsError: true,
		}, nil
	}
	return jsonrpc.ToolResult{
		Content: []jsonrpc.ToolContent{{Type: "text", Text: `{"cleared":true}`}},
	}, nil
}

func handleConvergenceStatus(ctx *Context) (interface{}, error) {
	status := ctx.Convergence.Status()
	return jsonrpc.ToolResult{
		Content: []jsonrpc.ToolContent{{Type: "text", Text: mustJSON(status)}},
	}, nil
}

func mustJSON(v interface{}) string {
	data, _ := json.MarshalIndent(v, "", "  ")
	return string(data)
}

func stringVal(m map[string]interface{}, key string) string {
	v, _ := m[key].(string)
	return v
}
