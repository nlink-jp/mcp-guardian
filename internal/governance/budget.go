package governance

// BudgetCheck checks if the call count is within the budget.
// maxCalls == 0 means unlimited. Returns (passed, reason).
func BudgetCheck(callCount, maxCalls int) (bool, string) {
	if maxCalls <= 0 {
		return true, ""
	}
	if callCount >= maxCalls {
		return false, "budget exhausted"
	}
	return true, ""
}
