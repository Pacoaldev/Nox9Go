package oauth

import "strings"

// connectionDisplayName is the single naming rule for every OAuth-family
// connection: the account email when known, else an explicit user-supplied
// name, else the provider default. Naming accounts "Provider (name)" makes
// multi-account setups indistinguishable on the provider detail page, so the
// provider prefix is intentionally dropped here.
func connectionDisplayName(provider, name, email, fallback string) string {
	if email != "" {
		return email
	}
	if strings.TrimSpace(name) != "" {
		return name
	}
	if fallback != "" {
		return fallback
	}
	return provider
}
