// Package gizwaysql owns the ordered schema history for one regional database.
package gizwaysql

import _ "embed"

var (
	//go:embed migrations/000001_schema.sql
	schema string

	//go:embed seeds/story-base.sql
	StoryBaseSeed string

	// Migrations is ordered and append-only. CN and Global apply the same
	// history to independent databases and never share a connection pool.
	Migrations = []string{schema}
)
