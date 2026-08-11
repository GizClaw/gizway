// Package sqlassets owns embedded SQL schemas and explicitly separated seeds.
package sqlassets

import _ "embed"

var (
	// PostgreSQLSchema is the single production and test database schema.
	//go:embed migrations/000001_core_schema.sql
	PostgreSQLSchema string

	// DevelopmentSeed provides deterministic local users, keys, and models.
	//go:embed seeds/development.sql
	DevelopmentSeed string

	// StoryBaseSeed provides deterministic fixtures for black-box Hurl stories.
	// It is never loaded by a normal production or development startup.
	//go:embed seeds/story-base.sql
	StoryBaseSeed string
)
