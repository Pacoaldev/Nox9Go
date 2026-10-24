package chat

import (
	"testing"

	"nox9go/proxy/internal/db"
)

func TestGetBestConnection_NoAuth_WithProxyStrategy(t *testing.T) {
	database, cleanup := setupChatTestDB(t)
	defer cleanup()

	if _, err := database.Exec(`CREATE TABLE IF NOT EXISTS proxyPools (
		id TEXT PRIMARY KEY,
		isActive INTEGER DEFAULT 1,
		testStatus TEXT,
		data TEXT NOT NULL,
		createdAt TEXT NOT NULL,
		updatedAt TEXT NOT NULL
	);`); err != nil {
		t.Fatalf("failed to create proxyPools table: %v", err)
	}

	repo := db.NewRepo(database)

	pool, err := repo.InsertProxyPool(db.ProxyPoolData{
		Name:     "mimo-pool-1",
		ProxyURL: "http://proxy1.example.com:8080",
		Type:     "http",
	})
	if err != nil {
		t.Fatalf("insert pool: %v", err)
	}
	poolID := pool["id"].(string)

	if _, err := database.Exec(`CREATE TABLE IF NOT EXISTS settings (id INTEGER PRIMARY KEY, data TEXT);`); err != nil {
		t.Fatalf("create settings table: %v", err)
	}

	// Save settings with providerStrategies
	settingsJSON := `{
		"providerStrategies": {
			"mimo-free": {
				"proxyPoolId": "` + poolID + `",
				"rotateStrategy": "none"
			}
		}
	}`
	if _, err := database.Exec(`INSERT INTO settings (id, data) VALUES (1, ?) ON CONFLICT(id) DO UPDATE SET data = excluded.data`, settingsJSON); err != nil {
		t.Fatalf("insert settings: %v", err)
	}

	h := NewChatHandler(repo)

	conn, connData, err := h.GetBestConnection("mimo-free", "", nil, "")
	if err != nil {
		t.Fatalf("GetBestConnection failed: %v", err)
	}

	if conn == nil || connData == nil {
		t.Fatal("expected virtual connection for no-auth provider")
	}

	if connData.ProxyPoolID != poolID {
		t.Errorf("expected ProxyPoolID %s from settings strategy, got %s", poolID, connData.ProxyPoolID)
	}
}

func TestResolveProviderProxyPoolID_CrossAlias(t *testing.T) {
	database, cleanup := setupChatTestDB(t)
	defer cleanup()

	if _, err := database.Exec(`CREATE TABLE IF NOT EXISTS settings (id INTEGER PRIMARY KEY, data TEXT);`); err != nil {
		t.Fatalf("create settings table: %v", err)
	}

	// User sets proxyPoolId under "antigravity", request asks for "ag"
	settingsJSON := `{
		"providerStrategies": {
			"antigravity": {
				"proxyPoolId": "pool-antigravity-123"
			}
		}
	}`
	if _, err := database.Exec(`INSERT INTO settings (id, data) VALUES (1, ?) ON CONFLICT(id) DO UPDATE SET data = excluded.data`, settingsJSON); err != nil {
		t.Fatalf("insert settings: %v", err)
	}

	repo := db.NewRepo(database)
	h := NewChatHandler(repo)

	// 1. Direct match on antigravity
	if got := h.ResolveProviderProxyPoolID("antigravity"); got != "pool-antigravity-123" {
		t.Errorf("expected pool-antigravity-123 for antigravity, got %q", got)
	}

	// 2. Alias match on ag (should find antigravity proxy pool)
	if got := h.ResolveProviderProxyPoolID("ag"); got != "pool-antigravity-123" {
		t.Errorf("expected ag to inherit pool-antigravity-123 from antigravity strategy, got %q", got)
	}

	// 3. Set under "opencode", check "oc"
	settingsJSON2 := `{
		"providerStrategies": {
			"opencode": {
				"proxyPoolId": "pool-opencode-456"
			}
		}
	}`
	if _, err := database.Exec(`UPDATE settings SET data = ? WHERE id = 1`, settingsJSON2); err != nil {
		t.Fatalf("update settings: %v", err)
	}
	if got := h.ResolveProviderProxyPoolID("oc"); got != "pool-opencode-456" {
		t.Errorf("expected oc to inherit pool-opencode-456 from opencode strategy, got %q", got)
	}

	// 4. Cline <-> clinepass alias
	settingsJSON3 := `{
		"providerStrategies": {
			"cline": {
				"proxyPoolId": "pool-cline-789"
			}
		}
	}`
	if _, err := database.Exec(`UPDATE settings SET data = ? WHERE id = 1`, settingsJSON3); err != nil {
		t.Fatalf("update settings: %v", err)
	}
	if got := h.ResolveProviderProxyPoolID("clinepass"); got != "pool-cline-789" {
		t.Errorf("expected clinepass to inherit pool-cline-789 from cline strategy, got %q", got)
	}
}

func TestGetBestConnection_Opencode_InheritsProxyFromStrategy(t *testing.T) {
	database, cleanup := setupChatTestDB(t)
	defer cleanup()

	if _, err := database.Exec(`CREATE TABLE IF NOT EXISTS settings (id INTEGER PRIMARY KEY, data TEXT);`); err != nil {
		t.Fatalf("create settings table: %v", err)
	}

	settingsJSON := `{
		"providerStrategies": {
			"opencode": {
				"proxyPoolId": "pool-vercel-relay-xyz"
			}
		}
	}`
	if _, err := database.Exec(`INSERT INTO settings (id, data) VALUES (1, ?) ON CONFLICT(id) DO UPDATE SET data = excluded.data`, settingsJSON); err != nil {
		t.Fatalf("insert settings: %v", err)
	}

	repo := db.NewRepo(database)
	h := NewChatHandler(repo)

	// Opencode is NoAuth, so GetBestConnection returns a virtual connection
	conn, connData, err := h.GetBestConnection("opencode", "", nil, "")
	if err != nil {
		t.Fatalf("GetBestConnection(opencode) failed: %v", err)
	}
	if conn == nil || connData == nil {
		t.Fatal("expected virtual connection for opencode")
	}
	if connData.ProxyPoolID != "pool-vercel-relay-xyz" {
		t.Errorf("expected ProxyPoolID 'pool-vercel-relay-xyz' on opencode virtual connection, got %q", connData.ProxyPoolID)
	}
}
