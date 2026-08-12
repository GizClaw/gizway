// Package gizpaysql owns the ordered schema history for the GizPay database.
package gizpaysql

import _ "embed"

var (
	//go:embed migrations/000001_schema.sql
	schema string

	//go:embed seeds/story-base.sql
	StoryBaseSeed string

	// Migrations is ordered and append-only. GizPay records these versions in
	// its own database; no regional binary imports this package.
	Migrations = []string{schema}
)
