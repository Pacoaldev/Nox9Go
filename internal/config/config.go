package config

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/viper"

	"nox9go/proxy/internal/constants"
	"nox9go/proxy/internal/log"
)

// Config holds the proxy gateway configuration.
type Config struct {
	Host            string
	Port            int
	DatabasePath    string
	JWTSecret       string
	InitialPassword string
	RTKEnabled      bool
	CavemanEnabled  bool
	PonytailEnabled bool
	ADHDEnabled     bool
}

// NewViper creates and configures a new Viper instance reading from .env with standard defaults.
func NewViper() *viper.Viper {
	return NewViperWithFile(".env")
}

// NewViperWithFile creates and configures a new Viper instance with the specified env file path.
func NewViperWithFile(configFile string) *viper.Viper {
	v := viper.New()
	if configFile != "" {
		v.SetConfigFile(configFile)
		v.SetConfigType("env")
	}

	v.AutomaticEnv()

	v.SetDefault("PORT", 20130)
	v.SetDefault("RTK_ENABLED", true)
	v.SetDefault("CAVEMAN_ENABLED", false)
	v.SetDefault("PONYTAIL_ENABLED", false)
	v.SetDefault("ADHD_ENABLED", false)

	if configFile != "" {
		if err := v.ReadInConfig(); err != nil {
			var configFileNotFoundError viper.ConfigFileNotFoundError
			if !errors.Is(err, os.ErrNotExist) && !os.IsNotExist(err) && !errors.As(err, &configFileNotFoundError) {
				log.Warn("config", "read config file failed", "file", configFile, "error", err)
			}
		}
	}

	return v
}

// ProvideViper returns a configured Viper instance for dependency injection.
func ProvideViper() *viper.Viper {
	return NewViper()
}

// ProvideConfig provides *Config for dependency injection using the provided Viper instance.
func ProvideConfig(v *viper.Viper) *Config {
	return LoadConfigFromViper(v)
}

// ResolveDataDir returns the base data directory: DATA_DIR env, else the
// value from .env, else the platform default (~/.nox9go, or
// %APPDATA%/nox9go on Windows).
//
// The .env read is load-bearing, not a convenience. The server resolves its
// data dir through viper (LoadConfigFromViper), which does read .env, while
// `nox9go status|stop|logs` runs in a separate short-lived process that
// never boots the server. Reading only os.Getenv here meant a deployment
// configured purely through .env — every Docker/compose setup — had the CLI
// look at a different directory than the running daemon: status reported "not
// running" against a live listener, stop refused to kill it, and logs claimed
// no log existed. Both sides now answer the same question the same way.
//
// When the nox9go directory holds no database yet but the legacy one
// (~/.9router) does, the legacy directory wins. That keeps an existing
// installation working without a manual copy, and it keeps the documented
// "share one database with upstream 9Router" setup intact: upstream still
// writes to ~/.9router, so an upstream install is adopted automatically.
func ResolveDataDir() string {
	if dataDir := strings.TrimSpace(os.Getenv("DATA_DIR")); dataDir != "" {
		return dataDir
	}
	if dataDir := dataDirFromEnvFile(); dataDir != "" {
		return dataDir
	}
	preferred, legacy := platformDataDirs()
	if dirHasDatabase(preferred) {
		return preferred
	}
	if dirHasDatabase(legacy) {
		return legacy
	}
	return preferred
}

// platformDataDirs returns the NOX9GO data directory and the legacy 9router
// one for the current platform.
func platformDataDirs() (preferred, legacy string) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return ".nox9go", ".9router"
	}
	if runtime.GOOS == "windows" {
		appData := os.Getenv("APPDATA")
		if appData == "" {
			appData = filepath.Join(homeDir, "AppData", "Roaming")
		}
		return filepath.Join(appData, "nox9go"), filepath.Join(appData, "9router")
	}
	return filepath.Join(homeDir, ".nox9go"), filepath.Join(homeDir, ".9router")
}

// dirHasDatabase reports whether dir already holds a NOX9GO-compatible
// SQLite database. A directory that exists but carries no database is not a
// database holder, so the caller keeps looking at the next candidate.
func dirHasDatabase(dir string) bool {
	candidates := []string{
		filepath.Join("db", "data.sqlite"),
		"data.sqlite",
		"nox9go.db",
		"9router.db",
	}
	for _, name := range candidates {
		if fi, err := os.Stat(filepath.Join(dir, name)); err == nil && !fi.IsDir() {
			return true
		}
	}
	return false
}

// envFileName is the config file the server loads. It is a named constant so
// the resolution helpers and their tests cannot drift on the spelling.
const envFileName = ".env"

// dataDirFromEnvFile reads DATA_DIR out of the .env next to the binary or in
// the working directory. Viper owns the file format, so this reuses it rather
// than hand-parsing KEY=VALUE and drifting from what the server accepted.
func dataDirFromEnvFile() string {
	return envFileValue("DATA_DIR")
}

// envFileValue reads one key out of the first env file found, next to the
// binary or in the working directory. Viper owns the file format, so this
// reuses it rather than hand-parsing KEY=VALUE and drifting from what the
// server accepted.
func envFileValue(key string) string {
	for _, path := range envFileCandidates() {
		if _, err := os.Stat(path); err != nil {
			continue
		}
		v := viper.New()
		v.SetConfigFile(path)
		v.SetConfigType("env")
		if err := v.ReadInConfig(); err != nil {
			continue
		}
		if value := strings.TrimSpace(v.GetString(key)); value != "" {
			return value
		}
	}
	return ""
}

// devValue resolves one of the development report's settings: the process
// environment first, then the env file the server loads.
//
// Reading only os.Getenv was a defect with a visible symptom. Viper reads the
// env file into itself, so a value placed there is never in the process
// environment, and the report answered "no checkout configured" while the
// checkout sat in the config the server had already read. ResolveDataDir hit
// the same trap and was fixed the same way; these three settings now answer
// the question the same way DATA_DIR does.
func devValue(key string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return envFileValue(key)
}

// DevRepoRoot is the checkout the development report reads. Empty means the
// report is off, and the panel says how to enable it instead of guessing.
func DevRepoRoot() string { return devValue("DEV_REPO_ROOT") }

// DevFeature is the ODD document the report follows. Empty means the first
// feature found is used.
func DevFeature() string { return devValue("DEV_FEATURE") }

// DevSpoolPath is the notification spool the report can read. Empty means the
// default location under the data dir.
func DevSpoolPath() string { return devValue("DEV_SPOOL_PATH") }

// DevInboxPath is the instruction inbox the Telegram channel writes and the
// development session reads. It defaults to the data dir, so the channel is
// usable with no configuration; an explicit env value wins.
func DevInboxPath() string {
	if path := devValue("DEV_INBOX_PATH"); path != "" {
		return path
	}
	return filepath.Join(ResolveDataDir(), "dev-inbox.jsonl")
}

// DevOrderRunner is the PowerShell script the Telegram channel runs for each
// drained inbox order. Empty means orders are only queued, not executed here.
func DevOrderRunner() string { return devValue("DEV_ORDER_RUNNER") }

// HerdrAgentTarget is the Herdr agent name for the order runner script, or
// focused/@focused/* to resolve the pane Herdr marks focused:true on each order.
func HerdrAgentTarget() string { return devValue("HERDR_AGENT_TARGET") }

// HerdrAgentWorkCwd pins focused/workdir resolution to the agent whose pane cwd
// matches this path (e.g. the Ouroboros checkout while DEV_REPO_ROOT stays NOX9GO).
func HerdrAgentWorkCwd() string { return devValue("HERDR_AGENT_WORK_CWD") }

// HerdrAgentExcludeCwd drops focused agents on this cwd when disambiguating
// (often the gateway checkout so Telegram does not attach to a NOX9GO Pi pane).
func HerdrAgentExcludeCwd() string { return devValue("HERDR_AGENT_EXCLUDE_CWD") }

// HerdrPassiveWatch reports whether the Telegram channel polls the focused Herdr
// agent for Pi blocker dialogs even when no Telegram order is running.
// NOX9GO_HERDR_WATCH=0 disables; explicit 1 enables; empty defaults on when a
// Herdr order runner or HERDR_AGENT_TARGET is configured.
func HerdrPassiveWatch() bool {
	v := strings.ToLower(strings.TrimSpace(devValue("NOX9GO_HERDR_WATCH")))
	switch v {
	case "0", "false", "off", "no":
		return false
	case "1", "true", "on", "yes":
		return true
	}
	runner := strings.ToLower(strings.TrimSpace(DevOrderRunner()))
	if strings.Contains(runner, "herdr") {
		return true
	}
	return strings.TrimSpace(HerdrAgentTarget()) != ""
}

// envFileCandidates lists the .env locations the server itself reads: the one
// in the working directory, then the one beside the executable so a daemon
// started from another directory still finds the same config its CLI reports on.
func envFileCandidates() []string {
	candidates := []string{".env"}
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(exe), ".env"))
	}
	return candidates
}

// LoadConfig loads the configuration from environment variables, .env file, and platform defaults using Viper.
func LoadConfig() *Config {
	return LoadConfigFromViper(NewViper())
}

// LoadConfigFromViper builds *Config using the given Viper instance.
func LoadConfigFromViper(v *viper.Viper) *Config {
	if v == nil {
		v = NewViper()
	}
	host := strings.TrimSpace(v.GetString("HOST"))
	if host == "" {
		host = strings.TrimSpace(v.GetString("BIND_ADDR"))
	}

	port := v.GetInt("PORT")
	if port <= 0 {
		port = 20130 // Default port (unified port)
	}

	dataDir := v.GetString("DATA_DIR")
	if dataDir == "" {
		dataDir = ResolveDataDir()
	}

	// Ensure the base data directory exists
	if err := os.MkdirAll(dataDir, constants.FilePermDir); err != nil {
		log.Warn("config", "create data dir failed", "dir", dataDir, "error", err)
	}

	// Database file: DB_PATH overrides default DATA_DIR/db/data.sqlite
	dbPath := v.GetString("DB_PATH")
	if dbPath == "" {
		dbPath = filepath.Join(dataDir, "db", "data.sqlite")
	} else if fi, err := os.Stat(dbPath); err == nil && fi.IsDir() {
		if _, err := os.Stat(filepath.Join(dbPath, "db", "data.sqlite")); err == nil {
			dbPath = filepath.Join(dbPath, "db", "data.sqlite")
		} else if _, err := os.Stat(filepath.Join(dbPath, "data.sqlite")); err == nil {
			dbPath = filepath.Join(dbPath, "data.sqlite")
		} else if _, err := os.Stat(filepath.Join(dbPath, "nox9go.db")); err == nil {
			dbPath = filepath.Join(dbPath, "nox9go.db")
		} else if _, err := os.Stat(filepath.Join(dbPath, "9router.db")); err == nil {
			dbPath = filepath.Join(dbPath, "9router.db")
		} else {
			dbPath = filepath.Join(dbPath, "db", "data.sqlite")
		}
	}

	// INITIAL_PASSWORD has no hardcoded default — an empty value forces the
	// operator to set one explicitly rather than shipping a known password.
	initialPassword := v.GetString("INITIAL_PASSWORD")

	rtkEnabled := v.GetBool("RTK_ENABLED")
	cavemanEnabled := v.GetBool("CAVEMAN_ENABLED")
	ponytailEnabled := v.GetBool("PONYTAIL_ENABLED")
	adhdEnabled := v.GetBool("ADHD_ENABLED")

	return &Config{
		Host:            host,
		Port:            port,
		DatabasePath:    dbPath,
		JWTSecret:       loadJWTSecret(v, dataDir),
		InitialPassword: initialPassword,
		RTKEnabled:      rtkEnabled,
		CavemanEnabled:  cavemanEnabled,
		PonytailEnabled: ponytailEnabled,
		ADHDEnabled:     adhdEnabled,
	}
}

func loadJWTSecret(v *viper.Viper, dataDir string) string {
	var secret string
	if v != nil {
		secret = v.GetString("JWT_SECRET")
	}
	if secret == "" {
		secret = os.Getenv("JWT_SECRET")
	}
	if secret != "" {
		return secret
	}

	secretFile := filepath.Join(dataDir, "jwt-secret")
	data, err := os.ReadFile(secretFile)
	if err == nil {
		return string(data)
	}

	// Generate 32 cryptographically secure random bytes
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		log.Error("config", "crypto/rand failed to generate JWT secret; refusing to fall back to a static secret", "error", err)
		return ""
	}

	generated := hex.EncodeToString(bytes)
	if err := os.WriteFile(secretFile, []byte(generated), constants.FilePermKey); err != nil {
		log.Warn("config", "write JWT secret failed", "file", secretFile, "error", err)
	}
	return generated
}
