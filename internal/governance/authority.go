package governance

import (
	"github.com/nlink-jp/mcp-guardian/internal/state"
)

// AuthorityCheckResult holds the result of an authority check.
type AuthorityCheckResult struct {
	Passed bool
	Reason string
}

// CheckAuthority verifies that the session epoch matches the authority epoch.
func CheckAuthority(auth *state.Authority) AuthorityCheckResult {
	if auth == nil {
		return AuthorityCheckResult{Passed: true}
	}
	if auth.ActiveSessionEpoch < auth.Epoch {
		return AuthorityCheckResult{
			Passed: false,
			Reason: "stale session: authority has been bumped",
		}
	}
	return AuthorityCheckResult{Passed: true}
}
