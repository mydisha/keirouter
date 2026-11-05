package gateway

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/mydisha/keirouter/backend/internal/caveman"
	"github.com/mydisha/keirouter/backend/internal/crypto"
	"github.com/mydisha/keirouter/backend/internal/ponytail"
	"github.com/mydisha/keirouter/backend/internal/store"
)

// adminImport9routerSQLite imports a 9router SQLite database (data.sqlite)
// directly, converting its tables into KeiRouter's native model. It reuses the
// JSON importer's per-table converters (importN9router*) by reading the SQLite
// rows into the same map[string]json.RawMessage document shape.
//
// Unlike the JSON path, this also migrates usageHistory (usage_records) and the
// settings blob (token saver / routing / dashboard password), which the JSON
// export does not carry.
//
// n9IDPrefix marks rows imported from 9router so re-imports can replace them
// without touching KeiRouter-native rows.
const n9IDPrefix = "n9:"

// n9routerImportOptions controls which sections are imported and how existing
// data interacts with the incoming rows.
type n9routerImportOptions struct {
	// Sections.
	Usage      bool `json:"usage"`
	Providers  bool `json:"providers"`
	APIKeys    bool `json:"api_keys"`
	ProxyPools bool `json:"proxy_pools"`
	Chains     bool `json:"chains"`
	Settings   bool `json:"settings"`
	Password   bool `json:"password"`
	// Mode: "merge" (skip existing), "overwrite" (delete previous n9: rows
	// for selected sections first), "wipe" (delete ALL rows in selected
	// sections, including KeiRouter-native data).
	Mode string `json:"mode"`
}

// defaultN9routerImportOptions returns all sections enabled in merge mode.
func defaultN9routerImportOptions() n9routerImportOptions {
	return n9routerImportOptions{
		Usage: true, Providers: true, APIKeys: true, ProxyPools: true,
		Chains: true, Settings: true, Password: true,
		Mode: "merge",
	}
}

// adminAnalyze9routerSQLite reads the uploaded 9router database and returns
// per-table row counts so the UI can render an informed confirmation before
// the actual import. Nothing is written.
func (s *Server) adminAnalyze9routerSQLite(w http.ResponseWriter, r *http.Request) {
	counts, err := s.analyze9routerUpload(w, r)
	if err != nil {
		return // response already written
	}
	writeJSON(w, http.StatusOK, counts)
}

// analyze9routerUpload performs the shared upload+validate+count steps for
// both the analyze and import endpoints.
func (s *Server) analyze9routerUpload(w http.ResponseWriter, r *http.Request) (map[string]int64, error) {
	r.Body = http.MaxBytesReader(w, r.Body, sqliteBackupMaxBytes)
	if err := r.ParseMultipartForm(sqliteBackupMaxBytes); err != nil {
		writeError(w, http.StatusBadRequest, "invalid upload: "+err.Error())
		return nil, err
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "file is required")
		return nil, err
	}
	defer file.Close()
	if header.Size <= 0 {
		writeError(w, http.StatusBadRequest, "uploaded file is empty")
		return nil, fmt.Errorf("empty file")
	}
	tmp, err := os.CreateTemp(s.dataDirOrTemp(), "keirouter-9router-import-*.sqlite")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "create temp file failed: "+err.Error())
		return nil, err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	defer func() {
		// A WAL-mode upload leaves -wal/-shm siblings when opened; remove them
		// with the temp file so the data dir stays clean.
		_ = os.Remove(tmpPath + "-wal")
		_ = os.Remove(tmpPath + "-shm")
	}()
	if _, err := tmp.ReadFrom(file); err != nil {
		_ = tmp.Close()
		writeError(w, http.StatusInternalServerError, "save upload failed: "+err.Error())
		return nil, err
	}
	if err := tmp.Close(); err != nil {
		writeError(w, http.StatusInternalServerError, "close upload failed: "+err.Error())
		return nil, err
	}
	if err := validateSQLiteFile(r.Context(), tmpPath); err != nil {
		writeError(w, http.StatusBadRequest, "invalid SQLite database: "+err.Error())
		return nil, err
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	db, err := sql.Open("sqlite", "file:"+tmpPath+"?mode=ro")
	if err != nil {
		writeError(w, http.StatusBadRequest, "open 9router database: "+err.Error())
		return nil, err
	}
	defer db.Close()
	counts := map[string]int64{}
	for _, t := range []string{"providerNodes", "providerConnections", "apiKeys", "combos", "proxyPools", "usageHistory"} {
		var exists int
		if err := db.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?", t).Scan(&exists); err != nil {
			writeError(w, http.StatusBadRequest, "read 9router database: "+err.Error())
			return nil, err
		}
		if exists == 0 {
			continue
		}
		var n int64
		if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+t).Scan(&n); err == nil {
			counts[t] = n
		}
	}
	return counts, nil
}

func (s *Server) adminImport9routerSQLite(w http.ResponseWriter, r *http.Request) {
	if s.dbDialect() != store.DialectSQLite {
		writeError(w, http.StatusBadRequest, "9router SQLite import requires database.driver=sqlite")
		return
	}
	started := time.Now()

	// Options arrive as a JSON form field alongside the file.
	opts := defaultN9routerImportOptions()
	if raw := r.FormValue("options"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &opts); err != nil {
			writeError(w, http.StatusBadRequest, "invalid options: "+err.Error())
			return
		}
	}
	switch opts.Mode {
	case "merge", "overwrite", "wipe":
	default:
		writeError(w, http.StatusBadRequest, "mode must be merge, overwrite, or wipe")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, sqliteBackupMaxBytes)
	if err := r.ParseMultipartForm(sqliteBackupMaxBytes); err != nil {
		writeError(w, http.StatusBadRequest, "invalid upload: "+err.Error())
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "file is required")
		return
	}
	defer file.Close()

	if header.Size <= 0 {
		writeError(w, http.StatusBadRequest, "uploaded file is empty")
		return
	}

	tmp, err := os.CreateTemp(s.dataDirOrTemp(), "keirouter-9router-import-*.sqlite")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "create temp file failed: "+err.Error())
		return
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	defer func() {
		// A WAL-mode upload leaves -wal/-shm siblings when opened; remove them
		// with the temp file so the data dir stays clean.
		_ = os.Remove(tmpPath + "-wal")
		_ = os.Remove(tmpPath + "-shm")
	}()

	if _, err := tmp.ReadFrom(file); err != nil {
		_ = tmp.Close()
		writeError(w, http.StatusInternalServerError, "save upload failed: "+err.Error())
		return
	}
	if err := tmp.Close(); err != nil {
		writeError(w, http.StatusInternalServerError, "close upload failed: "+err.Error())
		return
	}

	if err := validateSQLiteFile(r.Context(), tmpPath); err != nil {
		writeError(w, http.StatusBadRequest, "invalid SQLite database: "+err.Error())
		return
	}

	// Import is additive (best-effort skip on conflict), so no full-database
	// replacement is needed. Still checkpoint WAL so the safety copy is clean.
	if err := s.checkpointSQLite(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "sqlite checkpoint failed: "+err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()

	doc, err := read9routerSQLiteDoc(ctx, tmpPath)
	if err != nil {
		writeError(w, http.StatusBadRequest, "read 9router database: "+err.Error())
		return
	}
	s.log.Info("9router import: database read", "elapsed_ms", time.Since(started).Milliseconds())

	// Wipe mode destroys ALL rows in the selected sections (including
	// KeiRouter-native data). Take a safety backup first so the operator can
	// recover; overwrite mode only removes previous n9: rows, which the
	// re-import recreates, so no backup is needed there.
	if opts.Mode == "wipe" {
		if err := s.n9routerSafetyBackup(); err != nil {
			writeError(w, http.StatusInternalServerError, "safety backup failed: "+err.Error())
			return
		}
	}
	res := &foreignImportResult{Source: "9router"}
	if opts.Mode != "merge" {
		s.delete9routerRows(ctx, opts, res)
	}

	if opts.Providers {
		s.importN9router(ctx, doc, res) // nodes + connections + keys + combos + pools + aliases + custom models
	}
	if opts.Usage {
		s.import9routerUsageHistory(ctx, doc, res)
	}
	if opts.Settings {
		s.import9routerSettings(ctx, doc, res, opts.Password)
	}
	s.log.Info("9router import: complete", "elapsed_ms", time.Since(started).Milliseconds(),
		"mode", opts.Mode, "accounts", res.Accounts, "custom_providers", res.CustomProviders,
		"api_keys", res.APIKeys, "chains", res.Chains, "proxy_pools", res.ProxyPools,
		"usage_records", res.UsageRecords, "errors", len(res.Errors))

	res.Imported = res.Accounts + res.CustomProviders + res.APIKeys + res.Chains + res.Aliases + res.ProxyPools
	writeJSON(w, http.StatusOK, res)
}

// n9routerSafetyBackup copies the live SQLite database to a timestamped
// safety file before destructive (wipe-mode) imports.
func (s *Server) n9routerSafetyBackup() error {
	path, ok := s.sqliteDBPath()
	if !ok {
		return fmt.Errorf("sqlite database path unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := s.checkpointSQLite(ctx); err != nil {
		return err
	}
	safetyPath := path + ".before-9router-import-" + time.Now().UTC().Format("20060102-150405")
	return copyFile(path, safetyPath)
}

// delete9routerRows removes previously imported 9router rows before a
// re-import. Overwrite mode deletes only rows carrying the n9: id prefix;
// wipe mode deletes every row in the selected sections.
func (s *Server) delete9routerRows(ctx context.Context, opts n9routerImportOptions, res *foreignImportResult) {
	onlyN9 := opts.Mode == "overwrite"
	del := func(table, idCol string) {
		q := "DELETE FROM " + table
		if onlyN9 {
			q += " WHERE " + idCol + " LIKE '" + n9IDPrefix + "%'"
		}
		if _, err := s.db.SQL().ExecContext(ctx, q); err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("delete %s: %v", table, err))
		}
	}
	if opts.Usage {
		del("usage_records", "id")
	}
	if opts.Providers {
		del("accounts", "id")
	}
	if opts.APIKeys {
		del("api_keys", "id")
	}
	if opts.ProxyPools {
		del("proxy_pools", "id")
	}
	if opts.Chains {
		del("chains", "id")
	}
	if opts.Settings {
		// Settings are keyed, not prefixed; reset the imported keys so the
		// incoming blob lands cleanly.
		for _, k := range []string{endpointSettingsKey, "auth.password_hash"} {
			_ = s.settings.Delete(ctx, k)
		}
		_, _ = s.db.SQL().ExecContext(ctx,
			"DELETE FROM settings WHERE key LIKE '"+providerRoutingPrefix+"%'")
		s.db.InvalidateSettingsCache()
	}
}

// read9routerSQLiteDoc opens the 9router SQLite file read-only and returns a
// document keyed by table name, where each value is a JSON array of row objects
// (column name -> value). This mirrors the JSON export shape consumed by the
// importN9router* converters.
func read9routerSQLiteDoc(ctx context.Context, path string) (map[string]json.RawMessage, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		return nil, err
	}
	defer db.Close()

	// Tables the JSON importer understands, plus usageHistory/settings handled
	// separately. requestDetails/usageDaily/_meta/sqlite_sequence are ignored.
	tables := []string{
		"providerNodes", "providerConnections", "apiKeys", "combos",
		"proxyPools", "modelAliases", "customModels", "usageHistory", "settings",
	}

	doc := make(map[string]json.RawMessage, len(tables))
	for _, t := range tables {
		raw, err := readTableAsJSON(ctx, db, t)
		if err != nil {
			return nil, fmt.Errorf("table %s: %w", t, err)
		}
		if raw == nil {
			continue // table absent
		}
		doc[t] = raw
	}
	// proxyPools keeps name/proxyUrl/type/strictProxy inside the nested `data`
	// JSON column; the JSON export flattens them and the importer reads the
	// flat shape. Merge `data` fields to the top level to match.
	flattenNestedData(doc, "proxyPools")
	return doc, nil
}

// flattenNestedData merges the parsed `data` JSON column of every row in the
// given table into the row's top level (without clobbering existing keys).
func flattenNestedData(doc map[string]json.RawMessage, table string) {
	raw, ok := doc[table]
	if !ok {
		return
	}
	var rows []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &rows); err != nil {
		return
	}
	for _, row := range rows {
		dataRaw, ok := row["data"]
		if !ok {
			continue
		}
		var data map[string]json.RawMessage
		if err := json.Unmarshal(dataRaw, &data); err != nil {
			continue
		}
		delete(row, "data")
		for k, v := range data {
			if _, exists := row[k]; !exists {
				row[k] = v
			}
		}
	}
	if b, err := json.Marshal(rows); err == nil {
		doc[table] = b
	}
}

// readTableAsJSON returns a JSON array of row objects for the given table, or
// nil when the table does not exist.
func readTableAsJSON(ctx context.Context, db *sql.DB, table string) (json.RawMessage, error) {
	var exists int
	if err := db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&exists); err != nil {
		return nil, err
	}
	if exists == 0 {
		return nil, nil
	}

	rows, err := db.QueryContext(ctx, "SELECT * FROM "+table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}

	// 9router stores booleans as SQLite INTEGER (0/1) and nested config as
	// JSON strings (e.g. providerNodes.data, combos.models). The JSON importer
	// structs expect real bools/arrays, so normalize while building each row:
	// INTEGER 0/1 → JSON bool, JSON-looking strings → embedded JSON values.
	intCols := make([]bool, len(cols))
	for i, c := range cols {
		switch c {
		case "isActive", "disabled", "enabled", "strictProxy":
			intCols[i] = true
		}
	}

	out := make([]map[string]any, 0)
	scan := make([]any, len(cols))
	scanPtrs := make([]any, len(cols))
	for i := range scan {
		scanPtrs[i] = &scan[i]
	}
	for rows.Next() {
		if err := rows.Scan(scanPtrs...); err != nil {
			return nil, err
		}
		obj := make(map[string]any, len(cols))
		for i, c := range cols {
			v := scan[i]
			if intCols[i] {
				if n, ok := v.(int64); ok {
					obj[c] = n != 0
					continue
				}
			}
			if s, ok := v.(string); ok && looksLikeJSON(s) {
				var parsed any
				if err := json.Unmarshal([]byte(s), &parsed); err == nil {
					obj[c] = parsed
					continue
				}
			}
			obj[c] = v
		}
		out = append(out, obj)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return json.Marshal(out)
}

// looksLikeJSON reports whether s starts like a JSON object or array (9router
// JSON-string columns such as data/models/tokens/meta always do).
func looksLikeJSON(s string) bool {
	s = strings.TrimSpace(s)
	return len(s) > 1 && (s[0] == '{' || s[0] == '[')
}

// ── usageHistory → usage_records ──────────────────────────────────────────

// n9routerUsageRow is one 9router usageHistory row.
type n9routerUsageRow struct {
	ID               int64           `json:"id"`
	Timestamp        string          `json:"timestamp"`
	Provider         string          `json:"provider"`
	Model            string          `json:"model"`
	ConnectionID     string          `json:"connectionId"`
	APIKey           string          `json:"apiKey"`
	Endpoint         string          `json:"endpoint"`
	PromptTokens     int             `json:"promptTokens"`
	CompletionTokens int             `json:"completionTokens"`
	Cost             float64         `json:"cost"`
	Status           string          `json:"status"`
	Tokens           json.RawMessage `json:"tokens"`
	Meta             json.RawMessage `json:"meta"`
}

// decode9routerJSONCol decodes a column that may arrive either as a JSON
// string containing JSON (the JSON export path) or as an already-parsed JSON
// object (the SQLite reader path).
func decode9routerJSONCol(raw json.RawMessage, target any) error {
	if len(raw) == 0 {
		return nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		if strings.TrimSpace(s) == "" {
			return nil
		}
		return json.Unmarshal([]byte(s), target)
	}
	return json.Unmarshal(raw, target)
}

// import9routerUsageHistory migrates 9router usageHistory rows into
// usage_records. Cost is converted USD → nanos (authoritative) and micros
// (compatibility). Provider ids are transformed to KeiRouter's custom-provider
// naming. Rows are inserted in batches via RecordBatch.
func (s *Server) import9routerUsageHistory(ctx context.Context, doc map[string]json.RawMessage, res *foreignImportResult) {
	raw, ok := doc["usageHistory"]
	if !ok {
		return
	}
	var rows []n9routerUsageRow
	if err := json.Unmarshal(raw, &rows); err != nil {
		res.Errors = append(res.Errors, "usageHistory: "+err.Error())
		return
	}

	// Pre-resolve apiKey plaintext → api_key_id via lookup hash.
	keyIDByLookup := s.usageKeyLookup(ctx)

	records := make([]store.UsageRecord, 0, len(rows))
	for _, r := range rows {
		rec := store.UsageRecord{
			ID:       strconv.FormatInt(r.ID, 10),
			TenantID: adminTenant,
			Provider: xform9routerProvider(r.Provider),
			Model:    r.Model,
			// Deterministic account id: matches the n9:-prefixed account row
			// created by the connections importer.
			AccountID: n9IDPrefix + r.ConnectionID,
			Status:    map9routerUsageStatus(r.Status),
		}
		if ts := parseRFC3339(r.Timestamp); ts != nil {
			rec.CreatedAt = *ts
		} else {
			rec.CreatedAt = time.Now()
		}
		rec.PromptTokens = r.PromptTokens
		rec.CompletionTokens = r.CompletionTokens

		// Cost: USD float → nanos (round half up), micros = nanos/1000.
		nanos := int64(r.Cost*1e9 + 0.5)
		rec.CostNanos = nanos
		rec.CostMicros = (nanos + 500) / 1000
		rec.PricingStatus = "legacy"
		rec.PricingSource = "legacy"
		rec.UsageSource = "provider"

		// tokens JSON carries cached/reasoning/cache-write breakdown.
		if len(r.Tokens) > 0 {
			var tk struct {
				CachedTokens     int `json:"cached_tokens"`
				CacheReadTokens  int `json:"cache_read_input_tokens"`
				CacheWriteTokens int `json:"cache_creation_input_tokens"`
				ReasoningTokens  int `json:"reasoning_tokens"`
			}
			if err := decode9routerJSONCol(r.Tokens, &tk); err == nil {
				rec.CachedTokens = tk.CachedTokens
				if rec.CachedTokens == 0 {
					rec.CachedTokens = tk.CacheReadTokens
				}
				rec.CacheWriteTokens = tk.CacheWriteTokens
				rec.ReasoningTokens = tk.ReasoningTokens
			}
		}

		// meta JSON carries client/request_id/latency.
		if len(r.Meta) > 0 && string(r.Meta) != "{}" && string(r.Meta) != `"{}"` {
			var m struct {
				Client    string `json:"client"`
				RequestID string `json:"request_id"`
				LatencyMS int    `json:"latency_ms"`
				TTFTMS    int    `json:"ttft_ms"`
			}
			if err := decode9routerJSONCol(r.Meta, &m); err == nil {
				rec.Client = m.Client
				rec.RequestID = m.RequestID
				rec.LatencyMS = m.LatencyMS
				rec.EndToEndLatencyMS = m.LatencyMS
				rec.TTFTMS = m.TTFTMS
			}
		}

		// Resolve apiKey plaintext → api_key_id.
		if r.APIKey != "" {
			if id, ok := keyIDByLookup[crypto.LookupHash(r.APIKey)]; ok {
				rec.APIKeyID = id
			}
		}

		records = append(records, rec)
	}

	// Insert in chunks so each transaction stays short: one 60k-row
	// transaction holds the SQLite write lock for the whole import and starves
	// background writers (health checks, resource samples) with SQLITE_BUSY.
	// RecordBatch already splits statements to respect bind limits; chunking
	// here additionally bounds transaction duration.
	const chunkSize = 1000
	imported := 0
	for start := 0; start < len(records); start += chunkSize {
		end := min(start+chunkSize, len(records))
		if err := s.usage.RecordBatch(ctx, records[start:end]); err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("usageHistory: %d/%d rows: %v", start, len(records), err))
			break
		}
		imported = end
	}
	res.UsageRecords = imported
}

// usageKeyLookup builds a map from api_key lookup_hash → api_key id for the
// current tenant, so usageHistory rows can be linked to imported keys.
func (s *Server) usageKeyLookup(ctx context.Context) map[string]string {
	out := map[string]string{}
	if s.identity == nil {
		return out
	}
	keys, err := s.identity.List(ctx, adminTenant)
	if err != nil {
		return out
	}
	for _, k := range keys {
		if k.LookupHash != "" {
			out[k.LookupHash] = k.ID
		}
	}
	return out
}

// xform9routerProvider maps 9router provider ids to KeiRouter custom-provider
// naming. Unknown providers pass through unchanged.
func xform9routerProvider(p string) string {
	switch {
	case strings.HasPrefix(p, "openai-compatible-chat-"):
		return "custom-openai-" + strings.TrimPrefix(p, "openai-compatible-chat-")
	case strings.HasPrefix(p, "openai-compatible-responses-"):
		return "custom-openai-" + strings.TrimPrefix(p, "openai-compatible-responses-")
	case strings.HasPrefix(p, "anthropic-compatible-"):
		return "custom-anthropic-" + strings.TrimPrefix(p, "anthropic-compatible-")
	default:
		return p
	}
}

// map9routerUsageStatus normalizes 9router status strings to KeiRouter's
// usage_records.status vocabulary.
func map9routerUsageStatus(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "ok", "success":
		return "success"
	case "cache", "cache_hit", "cached":
		return "cache_hit"
	case "blocked", "guardrail":
		return "blocked"
	case "failed", "error":
		return "failed"
	case "cancelled", "canceled":
		return "cancelled"
	default:
		return "success"
	}
}

// ── settings (token saver / routing / password) ───────────────────────────

// import9routerSettings migrates the 9router settings blob into KeiRouter's
// endpoint_settings (token saver + routing strategy), per-provider routing
// overrides, and optionally the dashboard password.
//
// Settings are additive: existing keys are overwritten only if the imported
// value is non-zero, so manual tweaks are preserved when re-importing.
func (s *Server) import9routerSettings(ctx context.Context, doc map[string]json.RawMessage, res *foreignImportResult, includePassword bool) {
	raw, ok := doc["settings"]
	if !ok {
		return
	}
	var rows []map[string]any
	if err := json.Unmarshal(raw, &rows); err != nil || len(rows) == 0 {
		return
	}
	dataRaw, ok := rows[0]["data"]
	if !ok {
		return
	}
	var data map[string]any
	switch v := dataRaw.(type) {
	case string:
		if err := json.Unmarshal([]byte(v), &data); err != nil {
			return
		}
	case map[string]any:
		data = v
	default:
		return
	}
	if s.settings == nil {
		return
	}

	// ── Patch 5: token saver → endpoint_settings ──────────────────────
	s.import9routerTokenSaver(ctx, data, res)

	// ── Patch 6: routing strategy → endpoint_settings + provider_routing_*
	s.import9routerRouting(ctx, data, res)

	// ── Patch 7: dashboard password bcrypt → argon2id ─────────────────
	if includePassword {
		s.import9routerPassword(ctx, data, res)
	}
}

// import9routerTokenSaver maps 9router's rtk/caveman/ponytail/headroom
// settings into KeiRouter's endpoint_settings JSON blob.
func (s *Server) import9routerTokenSaver(ctx context.Context, data map[string]any, res *foreignImportResult) {
	current := s.loadEndpointSettings(ctx)
	changed := false

	// RTK: 9router uses rtkEnabled bool; map "on" → rtk_filter_level "minimal".
	if v, ok := data["rtkEnabled"].(bool); ok {
		current.RTKEnabled = v
		if v {
			current.RTKFilterLevel = "minimal"
		}
		changed = true
	}

	// Caveman: cavemanEnabled + cavemanLevel (lite/full/ultra).
	if v, ok := data["cavemanEnabled"].(bool); ok {
		current.CavemanEnabled = v
		changed = true
	}
	if v, ok := data["cavemanLevel"].(string); ok && caveman.ValidLevel(caveman.Level(v)) {
		current.CavemanLevel = v
		changed = true
	}

	// Ponytail: ponytailEnabled + ponytailLevel (lite/full/ultra).
	if v, ok := data["ponytailEnabled"].(bool); ok {
		current.PonytailEnabled = v
		changed = true
	}
	if v, ok := data["ponytailLevel"].(string); ok && ponytail.ValidLevel(ponytail.Level(v)) {
		current.PonytailLevel = v
		changed = true
	}

	// Headroom: headroomEnabled + headroomUrl + headroomCodeAware.
	if v, ok := data["headroomEnabled"].(bool); ok {
		current.HeadroomEnabled = v
		changed = true
	}
	if v, ok := data["headroomUrl"].(string); ok && v != "" {
		current.HeadroomURL = v
		changed = true
	}
	if v, ok := data["headroomCodeAware"].(bool); ok {
		current.HeadroomCompressUserMessages = v
		changed = true
	}

	if !changed {
		return
	}

	rawJSON, _ := json.Marshal(current)
	if err := s.settings.Set(ctx, endpointSettingsKey, string(rawJSON)); err != nil {
		res.Errors = append(res.Errors, "token saver settings: "+err.Error())
		return
	}
}

// import9routerRouting maps 9router's fallbackStrategy/comboStrategy/sticky
// limits into KeiRouter's endpoint_settings and per-provider routing overrides.
func (s *Server) import9routerRouting(ctx context.Context, data map[string]any, res *foreignImportResult) {
	current := s.loadEndpointSettings(ctx)

	// Global routing strategy.
	if v, ok := data["fallbackStrategy"].(string); ok {
		if normalized, ok := normalizeAccountRoutingStrategy(v); ok {
			current.RoutingStrategy = normalized
		}
	}
	// Global combo strategy.
	if v, ok := data["comboStrategy"].(string); ok {
		if normalized, ok := normalizeComboRoutingStrategy(v); ok {
			current.ComboStrategy = normalized
		}
	}
	// Global sticky limits.
	if v, ok := data["stickyRoundRobinLimit"].(float64); ok && v > 0 {
		current.StickyLimit = int(v)
	}
	if v, ok := data["comboStickyRoundRobinLimit"].(float64); ok && v > 0 {
		current.ComboStickyLimit = int(v)
	}

	rawJSON, _ := json.Marshal(current)
	if err := s.settings.Set(ctx, endpointSettingsKey, string(rawJSON)); err != nil {
		res.Errors = append(res.Errors, "routing settings: "+err.Error())
		return
	}

	// Per-provider routing overrides.
	if ps, ok := data["providerStrategies"].(map[string]any); ok {
		s.import9routerProviderRouting(ctx, ps, res)
	}
}

// import9routerProviderRouting writes per-provider routing overrides to the
// settings store under the "provider_routing_{provider}" key. 9router provider
// ids (openai-compatible-chat-X / anthropic-compatible-X) are transformed to
// KeiRouter's custom-provider naming so the keys resolve at read time.
func (s *Server) import9routerProviderRouting(ctx context.Context, ps map[string]any, res *foreignImportResult) {
	for provider, v := range ps {
		m, ok := v.(map[string]any)
		if !ok || provider == "" {
			continue
		}
		provider = xform9routerProvider(provider)
		prs := ProviderRoutingSettings{
			RoutingStrategy: "inherit",
			StickyLimit:     3,
		}
		if fs, ok := m["fallbackStrategy"].(string); ok {
			if normalized, ok := normalizeProviderRoutingStrategy(fs); ok {
				prs.RoutingStrategy = normalized
			}
		}
		if sr, ok := m["stickyRoundRobinLimit"].(float64); ok && sr > 0 {
			prs.StickyLimit = int(sr)
		}
		rawJSON, _ := json.Marshal(prs)
		_ = s.settings.Set(ctx, providerRoutingPrefix+provider, string(rawJSON))
	}
}

// import9routerPassword imports the 9router dashboard password (bcrypt) into
// KeiRouter's auth.password_hash. If Keirouter already has a non-default
// password, the imported one is skipped to avoid overwriting.
func (s *Server) import9routerPassword(ctx context.Context, data map[string]any, res *foreignImportResult) {
	pw, ok := data["password"].(string)
	if !ok || pw == "" {
		return
	}

	// Only import if the current password is still the seeded default.
	if s.auth != nil && !s.auth.UsingDefaultPassword(ctx) {
		res.Errors = append(res.Errors, "password: skipped (dashboard already has a custom password)")
		return
	}

	// The bcrypt hash is stored directly; VerifyPassword detects $2b$/etc.
	// and re-hashes to argon2id on successful verification. Here we store it
	// as-is and let the login flow handle migration.
	if err := s.settings.Set(ctx, "auth.password_hash", pw); err != nil {
		res.Errors = append(res.Errors, "password: "+err.Error())
		return
	}
	res.Errors = append(res.Errors, "password: imported (bcrypt, will re-hash to argon2id on first login)")
}
