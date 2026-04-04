package governance

import (
	"sync"
	"time"
)

const (
	maxFailureRepeat   = 3
	maxToolTargetCalls = 5
	convergenceWindow  = 2 * time.Minute

	SignalNone      = "none"
	SignalWarning   = "warning"
	SignalExhausted = "exhausted"
	SignalLoop      = "loop"
)

// ConvergenceTracker detects repetitive patterns in tool calls.
type ConvergenceTracker struct {
	mu             sync.Mutex
	failureCounts  map[string][]int64 // signature -> timestamps
	toolCallCounts map[string][]int64 // tool:target -> timestamps
}

// NewConvergenceTracker creates a new tracker.
func NewConvergenceTracker() *ConvergenceTracker {
	return &ConvergenceTracker{
		failureCounts:  make(map[string][]int64),
		toolCallCounts: make(map[string][]int64),
	}
}

// RecordCall records a tool call for convergence tracking.
func (ct *ConvergenceTracker) RecordCall(toolName, target string) {
	ct.mu.Lock()
	defer ct.mu.Unlock()

	key := toolName + ":" + target
	now := time.Now().UnixMilli()
	ct.toolCallCounts[key] = pruneWindow(append(ct.toolCallCounts[key], now), now)
}

// RecordFailure records a failure for convergence tracking.
func (ct *ConvergenceTracker) RecordFailure(signature string) {
	ct.mu.Lock()
	defer ct.mu.Unlock()

	now := time.Now().UnixMilli()
	ct.failureCounts[signature] = pruneWindow(append(ct.failureCounts[signature], now), now)
}

// Signal returns the current convergence signal for a tool+target+signature combination.
func (ct *ConvergenceTracker) Signal(toolName, target, failureSignature string) string {
	ct.mu.Lock()
	defer ct.mu.Unlock()

	now := time.Now().UnixMilli()

	// Check failure repetition
	if failureSignature != "" {
		times := pruneWindow(ct.failureCounts[failureSignature], now)
		ct.failureCounts[failureSignature] = times
		if len(times) >= maxFailureRepeat {
			return SignalLoop
		}
	}

	// Check tool+target repetition
	key := toolName + ":" + target
	times := pruneWindow(ct.toolCallCounts[key], now)
	ct.toolCallCounts[key] = times
	if len(times) >= maxToolTargetCalls {
		return SignalExhausted
	}
	if len(times) >= maxToolTargetCalls-1 {
		return SignalWarning
	}

	return SignalNone
}

// Reset clears all convergence state (e.g., on session re-initialize).
func (ct *ConvergenceTracker) Reset() {
	ct.mu.Lock()
	defer ct.mu.Unlock()
	ct.failureCounts = make(map[string][]int64)
	ct.toolCallCounts = make(map[string][]int64)
}

// Status returns a summary of convergence state.
func (ct *ConvergenceTracker) Status() map[string]interface{} {
	ct.mu.Lock()
	defer ct.mu.Unlock()

	now := time.Now().UnixMilli()
	failures := make(map[string]int)
	for sig, times := range ct.failureCounts {
		pruned := pruneWindow(times, now)
		if len(pruned) > 0 {
			failures[sig] = len(pruned)
		}
	}
	calls := make(map[string]int)
	for key, times := range ct.toolCallCounts {
		pruned := pruneWindow(times, now)
		if len(pruned) > 0 {
			calls[key] = len(pruned)
		}
	}

	return map[string]interface{}{
		"failureCounts":  failures,
		"toolCallCounts": calls,
		"windowMs":       convergenceWindow.Milliseconds(),
	}
}

func pruneWindow(timestamps []int64, nowMs int64) []int64 {
	cutoff := nowMs - convergenceWindow.Milliseconds()
	start := 0
	for start < len(timestamps) && timestamps[start] < cutoff {
		start++
	}
	return timestamps[start:]
}
