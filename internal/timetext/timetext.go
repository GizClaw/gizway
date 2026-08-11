// Package timetext defines the only textual timestamp representation stored
// by Gizway. The PostgreSQL schema uses text for stable scan behavior, so
// every value must be fixed-width UTC for lexical ordering to equal temporal
// ordering in PostgreSQL.
package timetext

import "time"

// Layout has fixed nanosecond precision and a literal UTC suffix. Unlike
// time.RFC3339Nano it never removes trailing zeroes, so adjacent instants sort
// correctly as database text.
const Layout = "2006-01-02T15:04:05.000000000Z"

// Format converts an instant to Gizway's canonical database representation.
func Format(value time.Time) string { return value.UTC().Format(Layout) }

// Parse accepts any RFC3339 offset/precision at an API boundary.
func Parse(value string) (time.Time, error) { return time.Parse(time.RFC3339Nano, value) }

// Normalize accepts public RFC3339 input and returns fixed-width UTC text.
func Normalize(value string) (string, error) {
	parsed, err := Parse(value)
	if err != nil {
		return "", err
	}
	return Format(parsed), nil
}
