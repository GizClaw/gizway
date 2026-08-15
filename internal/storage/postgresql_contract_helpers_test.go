package storage_test

import (
	"database/sql"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/jmoiron/sqlx"
)

func assertPostgreSQLTableColumns(t *testing.T, db *sqlx.DB, required map[string][]string) {
	t.Helper()
	for table, wanted := range required {
		assertPostgreSQLSchemaTableColumns(t, db, "", table, wanted)
	}
}

func assertPostgreSQLSchemaTableColumns(t *testing.T, db *sqlx.DB, schema, table string, wanted []string) {
	t.Helper()
	var columns []string
	query := `SELECT column_name FROM information_schema.columns WHERE table_schema=current_schema() AND table_name=$1 ORDER BY ordinal_position`
	args := []any{table}
	if schema != "" {
		query = `SELECT column_name FROM information_schema.columns WHERE table_schema=$1 AND table_name=$2 ORDER BY ordinal_position`
		args = []any{schema, table}
	}
	if err := db.SelectContext(t.Context(), &columns, query, args...); err != nil {
		t.Fatal(err)
	}
	qualified := table
	if schema != "" {
		qualified = schema + "." + table
	}
	if len(columns) == 0 {
		t.Errorf("missing Milestone 03 table %s", qualified)
		return
	}
	for _, column := range wanted {
		if !slices.Contains(columns, column) {
			t.Errorf("table %s missing column %s; columns=%v", qualified, column, columns)
		}
	}
}

func assertPostgreSQLTableAbsent(t *testing.T, db *sqlx.DB, table string) {
	t.Helper()
	var exists bool
	if err := db.GetContext(t.Context(), &exists, `SELECT to_regclass(current_schema() || '.' || $1) IS NOT NULL`, table); err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Errorf("legacy or wrong-owner table %s must not exist in Milestone 03", table)
	}
}

func assertPostgreSQLUniqueColumns(t *testing.T, db *sqlx.DB, table string, columns []string) {
	t.Helper()
	var found bool
	if err := db.GetContext(t.Context(), &found, `
		SELECT EXISTS (
			SELECT 1 FROM pg_constraint c
			JOIN pg_class r ON r.oid=c.conrelid
			JOIN pg_namespace n ON n.oid=r.relnamespace
			WHERE n.nspname=current_schema() AND r.relname=$1 AND c.contype IN ('u','p')
			AND ARRAY(SELECT a.attname::text FROM unnest(c.conkey) WITH ORDINALITY AS k(attnum,ord)
			          JOIN pg_attribute a ON a.attrelid=c.conrelid AND a.attnum=k.attnum ORDER BY k.ord)=$2::text[]
		)`, table, "{"+strings.Join(columns, ",")+"}"); err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Errorf("table %s lacks unique constraint on (%s)", table, strings.Join(columns, ", "))
	}
}

func assertPostgreSQLForeignKeyDeleteAction(t *testing.T, db *sqlx.DB, table, column string, allowed ...string) {
	t.Helper()
	var action string
	err := db.GetContext(t.Context(), &action, `
		SELECT rc.delete_rule FROM information_schema.referential_constraints rc
		JOIN information_schema.key_column_usage k ON k.constraint_schema=rc.constraint_schema AND k.constraint_name=rc.constraint_name
		WHERE k.table_schema=current_schema() AND k.table_name=$1 AND k.column_name=$2`, table, column)
	if err == sql.ErrNoRows {
		t.Fatalf("missing foreign key %s.%s", table, column)
	}
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(allowed, action) {
		t.Errorf("%s.%s delete action=%s, want one of %v", table, column, action, allowed)
	}
}

func assertPostgreSQLForeignKeyTarget(t *testing.T, db *sqlx.DB, table, column, targetTable, targetColumn string) {
	t.Helper()
	var found bool
	if err := db.GetContext(t.Context(), &found, `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.referential_constraints rc
			JOIN information_schema.key_column_usage source ON source.constraint_schema=rc.constraint_schema AND source.constraint_name=rc.constraint_name
			JOIN information_schema.constraint_column_usage target ON target.constraint_schema=rc.unique_constraint_schema AND target.constraint_name=rc.unique_constraint_name
			WHERE source.table_schema=current_schema() AND source.table_name=$1 AND source.column_name=$2
			  AND target.table_schema=current_schema() AND target.table_name=$3 AND target.column_name=$4
		)`, table, column, targetTable, targetColumn); err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Errorf("%s.%s must reference %s.%s", table, column, targetTable, targetColumn)
	}
}

func checkDefinitions(t *testing.T, db *sqlx.DB, table string) []string {
	t.Helper()
	var definitions []string
	if err := db.SelectContext(t.Context(), &definitions, `SELECT pg_get_constraintdef(c.oid) FROM pg_constraint c JOIN pg_class r ON r.oid=c.conrelid JOIN pg_namespace n ON n.oid=r.relnamespace WHERE n.nspname=current_schema() AND r.relname=$1 AND c.contype='c'`, table); err != nil {
		t.Fatal(err)
	}
	return definitions
}

func assertPostgreSQLPositiveCheck(t *testing.T, db *sqlx.DB, table, column string) {
	t.Helper()
	definitions := checkDefinitions(t, db, table)
	for _, definition := range definitions {
		normalized := strings.NewReplacer("(", "", ")", "", "::bigint", "", "::integer", "").Replace(strings.ToLower(definition))
		if strings.Contains(normalized, column+" > 0") {
			return
		}
	}
	t.Errorf("table %s lacks positive check for %s; checks=%s", table, column, fmt.Sprint(definitions))
}

func assertPostgreSQLNonNegativeCheck(t *testing.T, db *sqlx.DB, table, column string) {
	t.Helper()
	definitions := checkDefinitions(t, db, table)
	for _, definition := range definitions {
		normalized := strings.NewReplacer("(", "", ")", "", "::bigint", "", "::integer", "").Replace(strings.ToLower(definition))
		if strings.Contains(normalized, column+" >= 0") {
			return
		}
	}
	t.Errorf("table %s lacks non-negative check for %s; checks=%s", table, column, fmt.Sprint(definitions))
}

func assertPostgreSQLCheckValues(t *testing.T, db *sqlx.DB, table, column string, values ...string) {
	t.Helper()
	definitions := checkDefinitions(t, db, table)
	quoted := regexp.MustCompile(`'([^']+)'`)
	seen := map[string]bool{}
	for _, definition := range definitions {
		if !strings.Contains(strings.ToLower(definition), strings.ToLower(column)) {
			continue
		}
		for _, match := range quoted.FindAllStringSubmatch(definition, -1) {
			seen[match[1]] = true
		}
	}
	actual := make([]string, 0, len(seen))
	for value := range seen {
		actual = append(actual, value)
	}
	want := append([]string(nil), values...)
	sort.Strings(actual)
	sort.Strings(want)
	if strings.Join(actual, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("%s.%s allows %v, want exactly %v; checks=%s", table, column, actual, want, strings.Join(definitions, " "))
	}
}
