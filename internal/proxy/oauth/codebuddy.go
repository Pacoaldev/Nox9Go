package oauth

import (
	"context"
	json "encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"strings"

	"nox9go/proxy/internal/providers"
)

// codebuddyRefreshPath is the plugin token refresh endpoint of the official
// CodeBuddy CLI. The refresh token travels in the X-Refresh-Token header, not in
// a form body (upstream decolua/9router open-sse/services/tokenRefresh/providers.js).
const codebuddyRefreshPath = "/v2/plugin/auth/token/refresh"

// codebuddyRefreshTarget describes one CodeBuddy deployment. The two are
// separate providers with their own host, domain and client identity, so each
// registers its own refresher.
type codebuddyRefreshTarget struct {
	provider  string
	baseURL   string
	domain    string
	userAgent string
}

var codebuddyRefreshTargets = []codebuddyRefreshTarget{
	{"codebuddy-cn", "https://copilot.tencent.com", "copilot.tencent.com", "CLI/2.63.2 CodeBuddy/2.63.2"},
	{"codebuddy-intl", "https://www.codebuddy.ai", "www.codebuddy.ai", "IDE/2.63.2 CodeBuddy/2.63.2"},
}

func init() {
	for _, t := range codebuddyRefreshTargets {
		Register(t.provider, t.refresher())
	}
}

func (t codebuddyRefreshTarget) refresher() Refresher {
	return func(ctx context.Context, p *Params) (*TokenResult, error) {
		return t.refresh(ctx, p)
	}
}

func (t codebuddyRefreshTarget) refresh(ctx context.Context, p *Params) (*TokenResult, error) {
	if p.RefreshToken == "" {
		return nil, fmt.Errorf("%s: missing refresh token", t.provider)
	}
	client := p.Client
	if client == nil {
		client = http.DefaultClient
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.baseURL+codebuddyRefreshPath, strings.NewReader("{}"))
	if err != nil {
		return nil, fmt.Errorf("%s create refresh request: %w", t.provider, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", t.userAgent)
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("X-Domain", t.domain)
	req.Header.Set("X-Refresh-Token", p.RefreshToken)
	req.Header.Set("X-Auth-Refresh-Source", "plugin")
	req.Header.Set("X-Product", "SaaS")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s refresh POST: %w", t.provider, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("%s read refresh response: %w", t.provider, err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s %w", t.provider, &providers.OAuthRefreshError{Status: resp.StatusCode, Body: truncateBody(body)})
	}

	var out struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			AccessToken  string `json:"accessToken"`
			RefreshToken string `json:"refreshToken"`
			ExpiresIn    int    `json:"expiresIn"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("%s parse refresh response: %w", t.provider, err)
	}
	if out.Code != 0 || out.Data.AccessToken == "" {
		return nil, fmt.Errorf("%s refresh returned no token (code %d): %s", t.provider, out.Code, out.Msg)
	}

	refreshToken := out.Data.RefreshToken
	if refreshToken == "" {
		refreshToken = p.RefreshToken
	}
	expiresIn := out.Data.ExpiresIn
	if expiresIn <= 0 {
		expiresIn = 86400
	}
	return &TokenResult{
		AccessToken:  out.Data.AccessToken,
		RefreshToken: refreshToken,
		ExpiresIn:    expiresIn,
	}, nil
}
