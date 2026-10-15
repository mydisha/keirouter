package audit

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"hash"
	"strconv"
	"time"

	"github.com/mydisha/keirouter/backend/internal/store"
)

// DeriveKey derives the audit HMAC key from the credential master key, so the
// chain cannot be recomputed by someone who can only write to the database.
func DeriveKey(masterKey []byte) []byte {
	mac := hmac.New(sha256.New, masterKey)
	mac.Write([]byte("keirouter/audit-chain/v1"))
	return mac.Sum(nil)
}

// entryHash is the chain hash of e, covering every stored column plus the
// previous row's hash. Without a key it degrades to plain SHA-256, which
// still catches accidental edits but not deliberate forgery.
func entryHash(key []byte, e store.AuditEntry) string {
	var h hash.Hash
	if len(key) > 0 {
		h = hmac.New(sha256.New, key)
	} else {
		h = sha256.New()
	}
	field := func(s string) {
		h.Write([]byte(s))
		h.Write([]byte{0x1f})
	}
	field(e.ID)
	field(e.TenantID)
	field(e.Actor)
	field(e.ActorType)
	field(e.Action)
	field(e.Target)
	field(e.ResourceType)
	field(e.Outcome)
	field(e.Severity)
	field(e.ClientIP)
	field(e.UserAgent)
	field(e.HTTPMethod)
	field(e.HTTPRoute)
	field(strconv.Itoa(e.StatusCode))
	field(e.RequestID)
	field(e.TraceID)
	field(e.SpanID)
	field(e.Detail)
	field(e.ChainID)
	field(strconv.FormatInt(e.ChainSeq, 10))
	field(e.PrevHash)
	// Microseconds: the precision audit_log stores.
	field(strconv.FormatInt(e.CreatedAt.UnixMicro(), 10))
	return hex.EncodeToString(h.Sum(nil))
}

// chainReader is the slice of store.AuditRepo Verify needs.
type chainReader interface {
	ChainIDs(ctx context.Context) ([]string, error)
	Chain(ctx context.Context, chainID string, afterSeq int64, limit int) ([]store.AuditEntry, error)
}

// Break describes one place where a chain does not verify.
type Break struct {
	ChainID string `json:"chain_id"`
	Seq     int64  `json:"seq"`
	EntryID string `json:"entry_id"`
	Reason  string `json:"reason"`
}

// VerifyReport summarizes a chain verification.
type VerifyReport struct {
	OK         bool      `json:"ok"`
	Chains     int       `json:"chains"`
	Entries    int       `json:"entries"`
	Breaks     []Break   `json:"breaks"`
	VerifiedAt time.Time `json:"verified_at"`
	// Truncated is set when verification stopped at maxBreaks.
	Truncated bool `json:"truncated,omitempty"`
}

const maxBreaks = 100

// Verify walks every writer chain and recomputes each row's hash. The first
// remaining row of a chain is trusted as its anchor, because the retention
// sweep deletes the oldest rows; every later row must follow it without a
// gap. Rows written before the chain existed (empty chain_id) are skipped.
func Verify(ctx context.Context, repo chainReader, key []byte) (VerifyReport, error) {
	rep := VerifyReport{OK: true, Breaks: []Break{}}
	ids, err := repo.ChainIDs(ctx)
	if err != nil {
		return rep, err
	}
	rep.Chains = len(ids)
	for _, id := range ids {
		var prev *store.AuditEntry
		after := int64(-1)
		for {
			rows, err := repo.Chain(ctx, id, after, 1000)
			if err != nil {
				return rep, err
			}
			for i := range rows {
				e := rows[i]
				rep.Entries++
				switch {
				case entryHash(key, e) != e.Hash:
					rep.addBreak(e, "hash mismatch: row was modified")
				case prev != nil && e.ChainSeq != prev.ChainSeq+1:
					rep.addBreak(e, "sequence gap: "+strconv.FormatInt(e.ChainSeq-prev.ChainSeq-1, 10)+" row(s) missing before this one")
				case prev != nil && e.PrevHash != prev.Hash:
					rep.addBreak(e, "previous-hash mismatch: chain was re-linked")
				}
				if len(rep.Breaks) >= maxBreaks {
					rep.Truncated = true
					rep.VerifiedAt = time.Now().UTC()
					return rep, nil
				}
				prev = &rows[i]
			}
			if len(rows) < 1000 {
				break
			}
			after = rows[len(rows)-1].ChainSeq
		}
	}
	rep.VerifiedAt = time.Now().UTC()
	return rep, nil
}

func (r *VerifyReport) addBreak(e store.AuditEntry, reason string) {
	r.OK = false
	r.Breaks = append(r.Breaks, Break{ChainID: e.ChainID, Seq: e.ChainSeq, EntryID: e.ID, Reason: reason})
}
