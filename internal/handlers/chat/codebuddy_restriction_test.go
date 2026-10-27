package chat

import (
	"net/http"
	"testing"
)

func TestAccountRestrictionCooldownSec(t *testing.T) {
	restricted := []byte(`{"code":11140,"msg":"request illegal","displayMsg":{"en":"Your account has been restricted due to unusual activity"}}`)
	channelGate := []byte(`{"code":11128,"msg":"request illegal"}`)

	tests := []struct {
		name     string
		provider string
		ue       *upstreamError
		want     int
	}{
		{"restricted codebuddy-intl", "codebuddy-intl", &upstreamError{StatusCode: http.StatusForbidden, Body: restricted}, 12 * 3600},
		{"restricted codebuddy-cn", "codebuddy-cn", &upstreamError{StatusCode: http.StatusForbidden, Body: restricted}, 12 * 3600},
		{"channel gate keeps base", "codebuddy-intl", &upstreamError{StatusCode: http.StatusForbidden, Body: channelGate}, 120},
		{"other provider keeps base", "opencode", &upstreamError{StatusCode: http.StatusForbidden, Body: restricted}, 120},
		{"non json keeps base", "codebuddy-intl", &upstreamError{StatusCode: http.StatusForbidden, Body: []byte("nope")}, 120},
		{"nil error keeps base", "codebuddy-intl", nil, 120},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := accountRestrictionCooldownSec(tt.provider, tt.ue, 120); got != tt.want {
				t.Errorf("got %d, want %d", got, tt.want)
			}
		})
	}
}
