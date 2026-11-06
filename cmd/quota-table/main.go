// Command quota-table regenerates docs/tabla_saldos.md from the live SQLite
// database, so the operator can hand a real numbers table to a third party
// instead of guessing.
//
// The quota numbers are not in the source tree. They live in two tables:
//
//   - apiKeys holds the per-caller allowances (rateLimitRPM, rateLimitTPM,
//     rateLimitConcurrency, tokenQuota, expiresAt). A key's spend is not a
//     counter: it is summed from usageHistory rows recorded after the key's
//     quotaResetId mark, plus the carryover row that survives a ledger prune.
//   - providerNodes holds the capacity policy of a custom provider endpoint
//     (rpmLimit, tpmLimit, concurrencyLimit, tokenQuota, queueTimeoutMs,
//     contractExpiresAt).
//
// Upstream quota windows (a provider's own daily or weekly allowance) are not
// stored: the gateway reads them from the provider at runtime. What this table
// can show honestly is the configured limit plus the consumption the gateway
// measured against it.
package main

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// defaultDBPath is the Windows install used by the author. Override it with
// NOX9GO_DB or a positional argument when the data dir lives elsewhere.
const defaultDBPath = `C:\Users\spano\AppData\Roaming\9router\db\data.sqlite`

func main() {
	dbPath := defaultDBPath
	if v := os.Getenv("NOX9GO_DB"); v != "" {
		dbPath = v
	}
	if len(os.Args) > 1 && os.Args[1] != "" {
		dbPath = os.Args[1]
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		log.Fatalf("open database %s: %v", dbPath, err)
	}
	defer db.Close()

	out, err := os.Create("docs/tabla_saldos.md")
	if err != nil {
		log.Fatalf("create docs/tabla_saldos.md: %v", err)
	}
	defer out.Close()

	fmt.Fprintf(out, "# Tabla de saldos y limites\n\n")
	fmt.Fprintf(out, "Generado por `make quota-table` desde `%s`.\n\n", dbPath)
	fmt.Fprintf(out, "Fecha: %s\n\n", time.Now().Format("2006-01-02 15:04:05"))
	fmt.Fprintf(out, "> 0 significa ilimitado. `contractExpiresAt` vacio significa sin expiracion.\n")
	fmt.Fprintf(out, "> El consumo de una API key se suma desde `usageHistory` por encima de su marca `quotaResetId`.\n\n")

	writeApiKeys(out, db)
	writeProviderNodes(out, db)
	writeProviderConsumption(out, db)

	fmt.Println("wrote docs/tabla_saldos.md")
}

// writeApiKeys renders the per-caller allowances and the spend measured
// against them.
func writeApiKeys(out *os.File, db *sql.DB) {
	fmt.Fprintf(out, "## API keys (limites por cliente)\n\n")
	fmt.Fprintf(out, "| API Key | Activa | RPM | TPM | Concurrencia | TokenQuota | Tokens usados | Saldo | quotaResetId | Expira | Ultimo uso | Requests |\n")
	fmt.Fprintf(out, "|---|---|---|---|---|---|---|---|---|---|---|---|\n")

	rows, err := db.Query(`
		SELECT id, name, isActive, rateLimitRPM, rateLimitTPM, rateLimitConcurrency,
		       tokenQuota, quotaResetId, expiresAt, lastUsedAt, usedCount, key
		FROM apiKeys
		ORDER BY name
	`)
	if err != nil {
		fmt.Fprintf(out, "| _(error leyendo apiKeys: %v)_ | | | | | | | | | | | |\n", err)
		fmt.Fprintf(out, "\n")
		return
	}
	defer rows.Close()

	for rows.Next() {
		var id, name, key string
		var isActive int
		var rpm, tpm, conc, tokenQuota, quotaResetID sql.NullInt64
		var expiresAt, lastUsedAt sql.NullString
		var usedCount sql.NullInt64

		if err := rows.Scan(&id, &name, &isActive, &rpm, &tpm, &conc, &tokenQuota,
			&quotaResetID, &expiresAt, &lastUsedAt, &usedCount, &key); err != nil {
			log.Fatalf("scan apiKeys: %v", err)
		}

		used := tokenUsageSince(db, key, int(quotaResetID.Int64))
		quota := int(tokenQuota.Int64)
		remaining := "-"
		if quota > 0 {
			remaining = strconv.Itoa(quota - used)
			if quota-used < 0 {
				remaining = "0 (agotada, " + strconv.Itoa(used-quota) + " de exceso)"
			}
		}

		fmt.Fprintf(out, "| %s | %s | %s | %s | %s | %s | %d | %s | %d | %s | %s | %d |\n",
			orDash(name), yesNo(isActive == 1),
			nullInt(rpm), nullInt(tpm), nullInt(conc), nullInt(tokenQuota),
			used, remaining,
			quotaResetID.Int64,
			nullStr(expiresAt), nullStr(lastUsedAt),
			usedCount.Int64,
		)
	}
	fmt.Fprintf(out, "\n")
}

// tokenUsageSince mirrors internal/db/quota_reset.go: spend is summed from the
// usageHistory rows recorded after the reset mark, matching the client key in
// either spelling (masked or raw), plus the carryover that survived a ledger
// prune.
func tokenUsageSince(db *sql.DB, rawKey string, mark int) int {
	if rawKey == "" {
		return 0
	}
	masked := maskAPIKey(rawKey)

	history := `COALESCE(SUM(COALESCE(promptTokens,0) + COALESCE(completionTokens,0)), 0)
	   FROM usageHistory WHERE (clientKey = ? OR apiKey = ? OR apiKey = ?)`
	args := []any{masked, rawKey, masked}
	if mark > 0 {
		history += ` AND id > ?`
		args = append(args, mark)
	}

	query := `SELECT (SELECT ` + history + `
	   + COALESCE((SELECT tokens FROM usageLedgerCarryover
	                WHERE keyId = (SELECT id FROM apiKeys WHERE key = ? LIMIT 1)), 0))`
	args = append(args, rawKey)

	var used int64
	if err := db.QueryRow(query, args...).Scan(&used); err != nil {
		log.Printf("token usage for %s: %v", masked, err)
		return 0
	}
	return int(used)
}

// writeProviderNodes renders the capacity policy of custom provider endpoints.
func writeProviderNodes(out *os.File, db *sql.DB) {
	fmt.Fprintf(out, "## Nodos de proveedor (capacidad del endpoint)\n\n")
	fmt.Fprintf(out, "| Nodo | Tipo | RPM | TPM | Concurrencia | QueueTimeoutMs | TokenQuota | quotaResetId | Contrato expira |\n")
	fmt.Fprintf(out, "|---|---|---|---|---|---|---|---|---|\n")

	rows, err := db.Query(`
		SELECT name, type, rpmLimit, tpmLimit, concurrencyLimit,
		       queueTimeoutMs, tokenQuota, quotaResetId, contractExpiresAt
		FROM providerNodes
		ORDER BY name
	`)
	if err != nil {
		fmt.Fprintf(out, "| _(error leyendo providerNodes: %v)_ | | | | | | | | |\n", err)
		fmt.Fprintf(out, "\n")
		return
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var name, ptype sql.NullString
		var rpm, tpm, conc, queue, tokenQuota, resetID sql.NullInt64
		var contract sql.NullString
		if err := rows.Scan(&name, &ptype, &rpm, &tpm, &conc, &queue, &tokenQuota, &resetID, &contract); err != nil {
			log.Fatalf("scan providerNodes: %v", err)
		}
		fmt.Fprintf(out, "| %s | %s | %s | %s | %s | %s | %s | %d | %s |\n",
			orDash(nullStr(name)), orDash(nullStr(ptype)),
			nullInt(rpm), nullInt(tpm), nullInt(conc), nullInt(queue),
			nullInt(tokenQuota), resetID.Int64, nullStr(contract))
		count++
	}
	if count == 0 {
		fmt.Fprintf(out, "| _(sin nodos de proveedor configurados)_ | | | | | | | | |\n")
	}
	fmt.Fprintf(out, "\n")
}

// writeProviderConsumption renders what each provider actually consumed over
// the last day, week and month. These are the measured spends; the upstream
// allowance itself is not stored by the gateway.
func writeProviderConsumption(out *os.File, db *sql.DB) {
	fmt.Fprintf(out, "## Consumo por proveedor (medido)\n\n")
	fmt.Fprintf(out, "| Proveedor | Requests 24h | Tokens 24h | Requests 7d | Tokens 7d | Requests 30d | Tokens 30d | Coste 30d |\n")
	fmt.Fprintf(out, "|---|---|---|---|---|---|---|---|\n")

	rows, err := db.Query(`
		SELECT provider,
		       SUM(CASE WHEN timestamp >= datetime('now','-1 day')  THEN 1 ELSE 0 END),
		       SUM(CASE WHEN timestamp >= datetime('now','-1 day')  THEN COALESCE(promptTokens,0)+COALESCE(completionTokens,0) ELSE 0 END),
		       SUM(CASE WHEN timestamp >= datetime('now','-7 days') THEN 1 ELSE 0 END),
		       SUM(CASE WHEN timestamp >= datetime('now','-7 days') THEN COALESCE(promptTokens,0)+COALESCE(completionTokens,0) ELSE 0 END),
		       SUM(CASE WHEN timestamp >= datetime('now','-30 days') THEN 1 ELSE 0 END),
		       SUM(CASE WHEN timestamp >= datetime('now','-30 days') THEN COALESCE(promptTokens,0)+COALESCE(completionTokens,0) ELSE 0 END),
		       SUM(CASE WHEN timestamp >= datetime('now','-30 days') THEN COALESCE(cost,0) ELSE 0 END)
		FROM usageHistory
		GROUP BY provider
		ORDER BY 7 DESC
	`)
	if err != nil {
		fmt.Fprintf(out, "| _(error leyendo usageHistory: %v)_ | | | | | | | |\n", err)
		fmt.Fprintf(out, "\n")
		return
	}
	defer rows.Close()

	for rows.Next() {
		var provider sql.NullString
		var r24, t24, r7, t7, r30, t30 sql.NullInt64
		var cost sql.NullFloat64
		if err := rows.Scan(&provider, &r24, &t24, &r7, &t7, &r30, &t30, &cost); err != nil {
			log.Fatalf("scan usageHistory: %v", err)
		}
		fmt.Fprintf(out, "| %s | %d | %d | %d | %d | %d | %d | %.4f |\n",
			orDash(nullStr(provider)),
			r24.Int64, t24.Int64, r7.Int64, t7.Int64, r30.Int64, t30.Int64,
			cost.Float64)
	}
	fmt.Fprintf(out, "\n")
}

// maskAPIKey mirrors internal/handlerutil.MaskAPIKey.
func maskAPIKey(key string) string {
	trimmed := strings.TrimSpace(key)
	runes := []rune(trimmed)
	if len(runes) <= 8 {
		return "***"
	}
	return string(runes[:8]) + "***" + string(runes[len(runes)-4:])
}

func nullInt(v sql.NullInt64) string {
	if !v.Valid {
		return "-"
	}
	return strconv.FormatInt(v.Int64, 10)
}

func nullStr(v sql.NullString) string {
	if !v.Valid || v.String == "" {
		return "-"
	}
	return v.String
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func yesNo(b bool) string {
	if b {
		return "si"
	}
	return "no"
}
