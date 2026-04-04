package governance

import (
	"encoding/json"
	"time"

	"github.com/nlink-jp/mcp-guardian/internal/classify"
	"github.com/nlink-jp/mcp-guardian/internal/state"
)

// GateResult aggregates the results of all governance gates.
type GateResult struct {
	Forward           bool
	BlockReason       string
	MutationType      string
	Target            string
	BudgetPassed      bool
	SchemaResult      SchemaValidationResult
	ConstraintResult  ConstraintCheckResult
	AuthorityResult   AuthorityCheckResult
	ConvergenceSignal string
}

// GateInput holds all inputs needed to run the governance pipeline.
type GateInput struct {
	ToolName    string
	Arguments   map[string]interface{}
	InputSchema json.RawMessage
	Constraints []state.Constraint
	Authority   *state.Authority
	CallCount   int
	MaxCalls    int
	SchemaMode  string
	Enforcement string
	Convergence *ConvergenceTracker
}

// RunGates executes the full 5-gate governance pipeline.
// Returns a GateResult indicating whether the call should be forwarded or blocked.
func RunGates(input GateInput) GateResult {
	result := GateResult{
		Forward:      true,
		BudgetPassed: true,
	}

	// Classify mutation type and extract target
	result.MutationType = classify.ClassifyMutation(input.ToolName, input.Arguments, input.InputSchema)
	result.Target = classify.ExtractTarget(input.Arguments)

	// Gate 1: Budget check
	passed, reason := BudgetCheck(input.CallCount, input.MaxCalls)
	result.BudgetPassed = passed
	if !passed {
		result.Forward = false
		result.BlockReason = reason
		return result
	}

	// Gate 2: Schema validation
	if input.SchemaMode != "off" {
		result.SchemaResult = ValidateSchema(input.Arguments, input.InputSchema)
		if !result.SchemaResult.Valid && input.SchemaMode == "strict" {
			result.Forward = false
			result.BlockReason = "schema validation failed: " + FormatSchemaErrors(result.SchemaResult.Errors)
			return result
		}
	}

	// Gate 3: Constraint check
	nowMs := time.Now().UnixMilli()
	result.ConstraintResult = CheckConstraints(input.ToolName, result.Target, input.Constraints, nowMs)
	if !result.ConstraintResult.Passed && input.Enforcement == "strict" {
		result.Forward = false
		result.BlockReason = result.ConstraintResult.Reason
		return result
	}

	// Gate 4: Authority check
	result.AuthorityResult = CheckAuthority(input.Authority)
	if !result.AuthorityResult.Passed && input.Enforcement == "strict" {
		result.Forward = false
		result.BlockReason = result.AuthorityResult.Reason
		return result
	}

	// Gate 5: Convergence check
	if input.Convergence != nil {
		result.ConvergenceSignal = input.Convergence.Signal(input.ToolName, result.Target, "")
		if result.ConvergenceSignal == SignalLoop || result.ConvergenceSignal == SignalExhausted {
			if input.Enforcement == "strict" {
				result.Forward = false
				result.BlockReason = "convergence: " + result.ConvergenceSignal
				return result
			}
		}
	} else {
		result.ConvergenceSignal = SignalNone
	}

	return result
}
