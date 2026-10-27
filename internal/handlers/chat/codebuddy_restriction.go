package chat

import (
	json "encoding/json/v2"
	"time"
)

// codebuddyRestrictionCooldown is how long an account stays out of rotation
// after CodeBuddy restricts it. The restriction is a verdict on the account,
// not on the request: it only ends through an appeal, so retrying every two
// minutes (the generic 403 cooldown) only feeds the abuse detector that
// restricted it.
const codebuddyRestrictionCooldown = 12 * time.Hour

// codebuddyRestrictionCode is the upstream code for "account restricted due to
// unusual activity".
const codebuddyRestrictionCode = 11140

// isCodebuddyAccountRestricted reports whether an upstream failure is
// CodeBuddy restricting the account. The provider check keeps every other
// provider's 403 handling untouched, and the code check keeps a different
// CodeBuddy 403 (channel gate 11128, for example) on the normal path.
func isCodebuddyAccountRestricted(provider string, ue *upstreamError) bool {
	switch provider {
	case "codebuddy", "codebuddy-cn", "codebuddy-intl":
	default:
		return false
	}
	if ue == nil || len(ue.Body) == 0 {
		return false
	}
	var data struct {
		Code int `json:"code"`
	}
	if err := json.Unmarshal(ue.Body, &data); err != nil {
		return false
	}
	return data.Code == codebuddyRestrictionCode
}

// accountRestrictionCooldownSec returns the long cooldown for a restricted
// CodeBuddy account, or base unchanged for every other failure.
func accountRestrictionCooldownSec(provider string, ue *upstreamError, base int) int {
	if isCodebuddyAccountRestricted(provider, ue) {
		return int(codebuddyRestrictionCooldown / time.Second)
	}
	return base
}
