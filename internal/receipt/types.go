package receipt

// Record represents a single hash-chained receipt entry.
type Record struct {
	Seq              int                    `json:"seq"`
	Hash             string                 `json:"hash"`
	PreviousHash     string                 `json:"previousHash"`
	Timestamp        int64                  `json:"timestamp"`
	ToolName         string                 `json:"toolName"`
	Arguments        map[string]interface{} `json:"arguments,omitempty"`
	Target           string                 `json:"target,omitempty"`
	MutationType     string                 `json:"mutationType"`
	Outcome          string                 `json:"outcome"`
	DurationMs       int64                  `json:"durationMs"`
	ConstraintCheck  *CheckResult           `json:"constraintCheck,omitempty"`
	AuthorityCheck   *CheckResult           `json:"authorityCheck,omitempty"`
	ContainmentCheck *CheckResult           `json:"containmentCheck,omitempty"`
	ConvergenceSignal string               `json:"convergenceSignal,omitempty"`
	AttributionClass string                 `json:"attributionClass,omitempty"`
	ActionClass      string                 `json:"actionClass,omitempty"`
	FailureKind      string                 `json:"failureKind,omitempty"`
	ErrorText        string                 `json:"errorText,omitempty"`
	Title            string                 `json:"title,omitempty"`
	Summary          string                 `json:"summary,omitempty"`
}

// CheckResult represents the result of a governance check.
type CheckResult struct {
	Passed bool   `json:"passed"`
	Reason string `json:"reason,omitempty"`
}
