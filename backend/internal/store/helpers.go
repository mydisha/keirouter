package store

import (
	"database/sql"
	"time"
)

// timeLayout is the canonical on-disk timestamp format (UTC, RFC3339).
const timeLayout = time.RFC3339

// formatTime renders a time for storage. The zero time is stored as empty.
func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(timeLayout)
}

// parseTime parses a stored timestamp, returning the zero time on failure so
// callers never have to handle a parse error for display purposes.
func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(timeLayout, s)
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}

// nullString maps an empty string to a SQL NULL so optional FKs stay null.
func nullString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

// nullTime maps a nil pointer to SQL NULL, else a formatted timestamp.
func nullTime(t *time.Time) sql.NullString {
	if t == nil || t.IsZero() {
		return sql.NullString{}
	}
	return sql.NullString{String: formatTime(*t), Valid: true}
}

// boolToInt encodes a bool as 0/1 for portable storage.
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
