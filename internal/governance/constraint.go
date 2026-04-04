package governance

import (
	"github.com/nlink-jp/mcp-guardian/internal/state"
)

// ConstraintCheckResult holds the result of a constraint check.
type ConstraintCheckResult struct {
	Passed       bool
	ConstraintID string
	Reason       string
}

// CheckConstraints checks if a tool+target combination is blocked by an active constraint.
func CheckConstraints(toolName, target string, constraints []state.Constraint, nowMs int64) ConstraintCheckResult {
	for _, c := range constraints {
		if c.IsExpired(nowMs) {
			continue
		}
		if c.ToolName == toolName && c.Target == target {
			return ConstraintCheckResult{
				Passed:       false,
				ConstraintID: c.ID,
				Reason:       "blocked by constraint: " + c.FailureSignature,
			}
		}
	}
	return ConstraintCheckResult{Passed: true}
}
