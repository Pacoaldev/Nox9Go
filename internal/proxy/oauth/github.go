package oauth

import (
	"context"
	json "encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"nox9go/proxy/internal/providers"
)

func init() {
	Register("github", refreshGitHub)
}

var githubCopilotTokenURL = "https://api.github.com/copilot_internal/v2/token"

// refreshGitHub ports upstream GithubExecutor.refreshCredentials
// (open-sse/executors/github.js): first mints a short-lived Copilot bearer
// token from https://api.github.com/copilot_internal/v2/token using the stored
// GitHub OAuth accessToken (`Authorization: token <accessToken>`). If that
// fails and a refreshToken is present, exchanges the refreshToken at GitHub's
// OAuth endpoint and retries the Copilot token exchange.
func refreshGitHub(ctx context.Context, p *Params) (*TokenResult, error) {
	client := p.Client
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}

	accessToken := strings.TrimSpace(p.AccessToken)
	if psd := p.ProviderSpecificData; psd != nil {
		if ghAccess := strings.TrimSpace(psd["githubAccessToken"]); ghAccess != "" {
			accessToken = ghAccess
		}
	}

	// If p.AccessToken is a `tid=...` Copilot token rather than the ghu_/gho_
	// OAuth token, we can only mint a new Copilot token when we have the
	// underlying GitHub access token or refresh token.
	if strings.HasPrefix(accessToken, "tid=") {
		accessToken = ""
	}

	if accessToken != "" {
		if copilotTok, expSec, rawExp, err := fetchCopilotToken(ctx, client, accessToken); err == nil && copilotTok != "" {
			return &TokenResult{
				AccessToken:  accessToken,
				RefreshToken: p.RefreshToken,
				ExpiresIn:    expSec,
				APIKey:       copilotTok,
				ExtraPSD: map[string]any{
					"copilotToken":          copilotTok,
					"copilotTokenExpiresAt": rawExp,
				},
			}, nil
		}
	}

	if strings.TrimSpace(p.RefreshToken) == "" {
		return nil, fmt.Errorf("github: copilot token exchange failed and no refresh_token available")
	}

	cfg := providers.KnownOAuthConfigs["github"]
	vals := url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {cfg.ClientID},
		"refresh_token": {p.RefreshToken},
	}
	if cfg.ClientSecret != "" {
		vals.Set("client_secret", cfg.ClientSecret)
	}
	ghRes, err := doFormRefresh(ctx, client, cfg.TokenURL, vals)
	if err != nil {
		return nil, fmt.Errorf("github: %w", err)
	}

	if copilotTok, expSec, rawExp, cErr := fetchCopilotToken(ctx, client, ghRes.AccessToken); cErr == nil && copilotTok != "" {
		ghRes.APIKey = copilotTok
		if expSec > 0 {
			ghRes.ExpiresIn = expSec
		}
		ghRes.ExtraPSD = map[string]any{
			"copilotToken":          copilotTok,
			"copilotTokenExpiresAt": rawExp,
		}
	}
	return ghRes, nil
}

func fetchCopilotToken(ctx context.Context, client *http.Client, githubAccessToken string) (string, int, any, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, githubCopilotTokenURL, nil)
	if err != nil {
		return "", 0, nil, err
	}
	req.Header.Set("Authorization", "token "+strings.TrimSpace(githubAccessToken))
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-GitHub-Api-Version", "2025-04-01")
	req.Header.Set("User-Agent", "GitHubCopilotChat/0.38.0")
	req.Header.Set("Editor-Version", "vscode/1.110.0")
	req.Header.Set("Editor-Plugin-Version", "copilot-chat/0.38.0")

	resp, err := client.Do(req)
	if err != nil {
		return "", 0, nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", 0, nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return "", 0, nil, &providers.OAuthRefreshError{Status: resp.StatusCode, Body: truncateBody(body)}
	}

	var parsed struct {
		Token     string `json:"token"`
		ExpiresAt any    `json:"expires_at"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", 0, nil, err
	}
	tok := strings.TrimSpace(parsed.Token)
	if tok == "" {
		return "", 0, nil, fmt.Errorf("empty copilot token")
	}
	expSec, ok := copilotExpiresInSeconds(parsed.ExpiresAt)
	if !ok || expSec <= 0 {
		expSec = 1800
	}
	return tok, expSec, parsed.ExpiresAt, nil
}

func copilotExpiresInSeconds(raw any) (int, bool) {
	now := time.Now()
	switch v := raw.(type) {
	case float64:
		sec := int64(v)
		if sec > 1e12 {
			sec /= 1000
		}
		return int(time.Unix(sec, 0).Sub(now).Seconds()), true
	case int64:
		sec := v
		if sec > 1e12 {
			sec /= 1000
		}
		return int(time.Unix(sec, 0).Sub(now).Seconds()), true
	case int:
		sec := int64(v)
		if sec > 1e12 {
			sec /= 1000
		}
		return int(time.Unix(sec, 0).Sub(now).Seconds()), true
	case string:
		s := strings.TrimSpace(v)
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			if n > 1e12 {
				n /= 1000
			}
			return int(time.Unix(n, 0).Sub(now).Seconds()), true
		}
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			return int(t.Sub(now).Seconds()), true
		}
	}
	return 0, false
}

// CopilotTokenNeedsRefresh reports whether a GitHub connection's short-lived
// copilotToken is missing or expires within 5 minutes.
func CopilotTokenNeedsRefresh(psd map[string]any) bool {
	if psd == nil {
		return true
	}
	tok, _ := psd["copilotToken"].(string)
	if strings.TrimSpace(tok) == "" {
		return true
	}
	rawExp, ok := psd["copilotTokenExpiresAt"]
	if !ok || rawExp == nil {
		return false
	}
	rem, ok := copilotExpiresInSeconds(rawExp)
	if !ok {
		return false
	}
	return rem <= 300
}

