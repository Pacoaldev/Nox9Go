package auth

import (
	"net"
	"net/http"
	"net/url"
	"strings"
)

// TunnelLoginBlocked mirrors upstream's tunnel gate (login/route.js
// isTunnelRequest + dashboardGuard's dashboard branch): when tunnel dashboard
// access is not explicitly enabled, a request arriving via the configured
// tunnel/tailscale hostname must not log in (or view the dashboard).
func TunnelLoginBlocked(r *http.Request, raw map[string]any) bool {
	if v, ok := raw["tunnelDashboardAccess"].(bool); ok && v {
		return false
	}
	host := strings.ToLower(r.Host)
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = strings.Trim(h, "[]")
	}
	for _, key := range []string{"tunnelUrl", "tailscaleUrl"} {
		rawURL, _ := raw[key].(string)
		if rawURL == "" {
			continue
		}
		u, err := url.Parse(rawURL)
		if err != nil {
			continue
		}
		if tunnelHost := strings.ToLower(u.Hostname()); tunnelHost != "" && host == tunnelHost {
			return true
		}
	}
	return false
}
