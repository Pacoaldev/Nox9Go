package proxy

import "testing"

func TestResolveKiroProfileARN(t *testing.T) {
	tests := []struct {
		name string
		psd  map[string]any
		want string
	}{
		{
			name: "explicit profileArn takes precedence",
			psd:  map[string]any{"profileArn": "arn:aws:codewhisperer:us-east-1:111111111111:profile/CUSTOM"},
			want: "arn:aws:codewhisperer:us-east-1:111111111111:profile/CUSTOM",
		},
		{
			name: "api_key omits profileArn",
			psd:  map[string]any{"authMethod": "api_key"},
			want: "",
		},
		{
			name: "builder-id uses idc fallback ARN",
			psd:  map[string]any{"authMethod": "builder-id"},
			want: "arn:aws:codewhisperer:us-east-1:638616132270:profile/AAAACCCCXXXX",
		},
		{
			name: "idc uses idc fallback ARN",
			psd:  map[string]any{"authMethod": "idc"},
			want: "arn:aws:codewhisperer:us-east-1:638616132270:profile/AAAACCCCXXXX",
		},
		{
			name: "social uses social fallback ARN",
			psd:  map[string]any{"authMethod": "social"},
			want: "arn:aws:codewhisperer:us-east-1:699475941385:profile/EHGA3GRVQMUK",
		},
		{
			name: "nil psd uses social fallback ARN",
			psd:  nil,
			want: "arn:aws:codewhisperer:us-east-1:699475941385:profile/EHGA3GRVQMUK",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ResolveKiroProfileARN(tt.psd); got != tt.want {
				t.Errorf("ResolveKiroProfileARN() = %q, want %q", got, tt.want)
			}
		})
	}
}
