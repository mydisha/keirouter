package gateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/mydisha/keirouter/backend/internal/crypto"
	"github.com/mydisha/keirouter/backend/internal/store"
)

// Full backup / restore: a logical dump of every table (chains, providers,
// accounts, API keys, portal users, plans, budgets, top-ups, usage history,
// settings...) for moving a router to another server. Spent and remaining
// balances are derived from budgets, top-ups, adjustments and usage rows, all
// of which are copied verbatim, so they are identical after a restore.
//
// Secrets sealed under this server's master key are re-encrypted under a
// passphrase on export and re-sealed under the target's master key on
// restore, so the target may use a different KEIROUTER_MASTER_KEY.

const (
	backupScheme        = "passphrase-box-v1"
	backupCheckText     = "keirouter-backup-check"
	backupSealedMarker  = "portable-box"
	minBackupPassphrase = 8
	maxRestoreBytes     = 4 << 30
)

type backupEncryption struct {
	Scheme string `json:"scheme"`
	Salt   string `json:"salt"`
	Check  string `json:"check"`
}

// sealedColumnPairs lists every (wrapped DEK, ciphertext) column pair holding
// a master-key-sealed secret.
var sealedColumnPairs = []struct{ table, dek, ct string }{
	{"accounts", "secret_wrapped_dek", "secret_ciphertext"},
	{"accounts", "token_wrapped_dek", "token_ciphertext"},
	{"accounts", "refresh_wrapped_dek", "refresh_ciphertext"},
	{"portal_users", "sealed_wrapped_dek", "sealed_ciphertext"},
	{"hosting_instances", "password_wrapped_dek", "password_ciphertext"},
}

// adminFullBackup serves POST /api/settings/backup {"passphrase": "..."} as a
// downloadable JSON file. The passphrase travels in the body, never the URL.
func (s *Server) adminFullBackup(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Passphrase string `json:"passphrase"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	pass := strings.TrimSpace(body.Passphrase)
	if len(pass) < minBackupPassphrase {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("passphrase must be at least %d characters", minBackupPassphrase))
		return
	}
	if s.db == nil || s.vault == nil {
		writeError(w, http.StatusServiceUnavailable, "backup not available: store or vault not configured")
		return
	}
	backup, err := s.db.ExportBackup(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "backup failed"))
		return
	}
	box, err := crypto.NewPortableBox(pass, nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "backup failed: "+err.Error())
		return
	}
	check, err := box.SealString(backupCheckText)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "backup failed: "+err.Error())
		return
	}
	backup.Encryption, _ = json.Marshal(backupEncryption{Scheme: backupScheme, Salt: box.Salt(), Check: check})
	if err := s.rekeyBackupSecrets(&backup, func(sealed crypto.Sealed) (crypto.Sealed, error) {
		plain, err := s.vault.Sealer().OpenString(sealed)
		if err != nil {
			return crypto.Sealed{}, fmt.Errorf("cannot open a sealed secret (master key mismatch?): %w", err)
		}
		ct, err := box.SealString(plain)
		return crypto.Sealed{WrappedDEK: backupSealedMarker, Ciphertext: ct}, err
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "backup failed: "+err.Error())
		return
	}

	name := "keirouter-backup-" + time.Now().UTC().Format("20060102-150405") + ".json"
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(backup); err != nil {
		s.log.Warn("backup stream failed", "err", err)
	}
	if s.consoleLog != nil {
		s.consoleLog.Log("INFO", "Full backup exported", fmt.Sprintf("%d tables", len(backup.Tables)))
	}
}

// adminFullRestore serves POST /api/settings/restore. The body is the backup
// file; the passphrase comes in the X-Backup-Passphrase header. The whole
// restore is one transaction: a wrong passphrase or any bad row changes nothing.
func (s *Server) adminFullRestore(w http.ResponseWriter, r *http.Request) {
	pass := strings.TrimSpace(r.Header.Get("X-Backup-Passphrase"))
	if pass == "" {
		writeError(w, http.StatusBadRequest, "X-Backup-Passphrase header is required")
		return
	}
	if s.db == nil || s.vault == nil {
		writeError(w, http.StatusServiceUnavailable, "restore not available: store or vault not configured")
		return
	}
	backup, err := store.DecodeBackup(http.MaxBytesReader(w, r.Body, maxRestoreBytes))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var enc backupEncryption
	if err := json.Unmarshal(backup.Encryption, &enc); err != nil || enc.Scheme != backupScheme {
		writeError(w, http.StatusBadRequest, "unsupported or missing backup encryption")
		return
	}
	salt, err := crypto.DecodePortableSalt(enc.Salt)
	if err != nil {
		writeError(w, http.StatusBadRequest, "corrupt backup encryption header")
		return
	}
	box, err := crypto.NewPortableBox(pass, salt)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if check, err := box.OpenString(enc.Check); err != nil || check != backupCheckText {
		writeError(w, http.StatusBadRequest, "wrong passphrase for this backup")
		return
	}
	if err := s.rekeyBackupSecrets(&backup, func(sealed crypto.Sealed) (crypto.Sealed, error) {
		if sealed.WrappedDEK != backupSealedMarker {
			return crypto.Sealed{}, errors.New("backup contains a secret that is not passphrase-encrypted")
		}
		plain, err := box.OpenString(sealed.Ciphertext)
		if err != nil {
			return crypto.Sealed{}, err
		}
		return s.vault.Sealer().SealString(plain)
	}); err != nil {
		writeError(w, http.StatusBadRequest, "restore failed: "+err.Error())
		return
	}
	stats, err := s.db.RestoreBackup(r.Context(), backup)
	if err != nil {
		writeError(w, http.StatusBadRequest, "restore failed (nothing was changed): "+err.Error())
		return
	}
	if s.consoleLog != nil {
		s.consoleLog.Log("WARN", "Full backup restored", fmt.Sprintf("%d tables, %d rows from %s", stats.Tables, stats.Rows, backup.CreatedAt))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "tables": stats.Tables, "rows": stats.Rows,
		"backup_created_at": backup.CreatedAt, "source_dialect": backup.SourceDialect,
		// In-memory caches (API-key auth, budgets, market snapshot) still
		// reflect the old data until the process restarts.
		"restart_required": true,
	})
}

// rekeyBackupSecrets applies fn to every sealed secret in the backup: the
// account/portal-user column pairs and any {wrapped_dek, ciphertext} object
// nested in a settings JSON value (e.g. the bansos key).
func (s *Server) rekeyBackupSecrets(b *store.Backup, fn func(crypto.Sealed) (crypto.Sealed, error)) error {
	for ti := range b.Tables {
		t := &b.Tables[ti]
		idx := map[string]int{}
		for i, c := range t.Columns {
			idx[c] = i
		}
		for _, p := range sealedColumnPairs {
			if p.table != t.Name {
				continue
			}
			di, okD := idx[p.dek]
			ci, okC := idx[p.ct]
			if !okD || !okC {
				continue
			}
			for _, row := range t.Rows {
				dek, _ := row[di].(string)
				ct, _ := row[ci].(string)
				if dek == "" || ct == "" {
					continue
				}
				out, err := fn(crypto.Sealed{WrappedDEK: dek, Ciphertext: ct})
				if err != nil {
					return fmt.Errorf("%s.%s: %w", t.Name, p.ct, err)
				}
				row[di], row[ci] = out.WrappedDEK, out.Ciphertext
			}
		}
		if t.Name != "settings" {
			continue
		}
		ki, okK := idx["key"]
		vi, okV := idx["value"]
		if !okK || !okV {
			continue
		}
		for _, row := range t.Rows {
			raw, _ := row[vi].(string)
			if !strings.Contains(raw, "wrapped_dek") {
				continue
			}
			var doc any
			if err := json.Unmarshal([]byte(raw), &doc); err != nil {
				continue
			}
			changed, err := rekeyJSONSealed(doc, fn)
			if err != nil {
				return fmt.Errorf("settings %v: %w", row[ki], err)
			}
			if changed {
				enc, err := json.Marshal(doc)
				if err != nil {
					return err
				}
				row[vi] = string(enc)
			}
		}
	}
	return nil
}

// rekeyJSONSealed walks a decoded JSON document and re-keys every object that
// carries a non-empty wrapped_dek + ciphertext pair.
func rekeyJSONSealed(doc any, fn func(crypto.Sealed) (crypto.Sealed, error)) (bool, error) {
	changed := false
	switch v := doc.(type) {
	case map[string]any:
		dek, okD := v["wrapped_dek"].(string)
		ct, okC := v["ciphertext"].(string)
		if okD && okC && dek != "" && ct != "" {
			out, err := fn(crypto.Sealed{WrappedDEK: dek, Ciphertext: ct})
			if err != nil {
				return false, err
			}
			v["wrapped_dek"], v["ciphertext"] = out.WrappedDEK, out.Ciphertext
			return true, nil
		}
		for _, child := range v {
			c, err := rekeyJSONSealed(child, fn)
			if err != nil {
				return false, err
			}
			changed = changed || c
		}
	case []any:
		for _, child := range v {
			c, err := rekeyJSONSealed(child, fn)
			if err != nil {
				return false, err
			}
			changed = changed || c
		}
	}
	return changed, nil
}
