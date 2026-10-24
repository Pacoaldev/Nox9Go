package cloudflare

import "testing"

func TestParseQuickTunnelURL(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "ignores api host",
			in:   "INF Requesting new quick Tunnel on trycloudflare.com using api.trycloudflare.com",
			want: "",
		},
		{
			name: "extracts public URL",
			in:   "INF |  https://random-words-here.trycloudflare.com                                |\n",
			want: "https://random-words-here.trycloudflare.com",
		},
		{
			name: "prefers last non-api URL",
			in:   "https://api.trycloudflare.com https://abc-def.trycloudflare.com https://xyz-uvw.trycloudflare.com",
			want: "https://xyz-uvw.trycloudflare.com",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseQuickTunnelURL(tt.in)
			if got != tt.want {
				t.Fatalf("parseQuickTunnelURL() = %q, want %q", got, tt.want)
			}
		})
	}
}
