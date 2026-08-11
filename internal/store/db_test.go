package store

import (
	"context"
	"errors"
	"testing"

	"github.com/jmoiron/sqlx"

	"github.com/idy/gizway/internal/testdb"
)

type testSQLStateError string

func (e testSQLStateError) Error() string    { return string(e) }
func (e testSQLStateError) SQLState() string { return string(e) }

func TestSerializableRetryRebuildsWholeOperation(t *testing.T) {
	attempts := 0
	value, err := retrySerializable(t.Context(), func() (string, error) {
		attempts++
		if attempts < 3 {
			return "", testSQLStateError("40001")
		}
		return "committed", nil
	})
	if err != nil || value != "committed" || attempts != 3 {
		t.Fatalf("value=%q attempts=%d err=%v", value, attempts, err)
	}
	if isSerializationFailure(errors.New("ordinary database error")) {
		t.Fatal("ordinary error classified as serialization failure")
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := retrySerializable(cancelled, func() (struct{}, error) {
		return struct{}{}, testSQLStateError("40P01")
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled retry err=%v", err)
	}
}

func TestCommandScopeDefersSerializationRetryToOuterOwner(t *testing.T) {
	collector := &commandRetryCollector{}
	ctx := withCommandRetryCollector(withCommandTransaction(t.Context(), &sqlx.Tx{}), collector)
	attempts := 0
	_, err := retrySerializable(ctx, func() (struct{}, error) {
		attempts++
		return struct{}{}, recordCommandRetryFailure(ctx, testSQLStateError("40001"))
	})
	if !isSerializationFailure(err) || attempts != 1 {
		t.Fatalf("inner retry attempts=%d err=%v", attempts, err)
	}
	if !isSerializationFailure(collector.failure()) {
		t.Fatalf("collector failure=%v", collector.failure())
	}
	// Only retryable transaction failures are promoted. Ordinary domain and
	// validation errors remain the handler's stable HTTP response.
	recordCommandRetryFailure(ctx, errors.New("ordinary"))
	if !isSerializationFailure(collector.failure()) {
		t.Fatalf("ordinary error replaced retry cause: %v", collector.failure())
	}
}

func TestSecretReferenceAndDatabaseTextBoundaries(t *testing.T) {
	reference := credentialReference("provider-secret")
	if reference != credentialReference("provider-secret") || reference == credentialReference("different") {
		t.Fatalf("credential reference is not deterministic: %q", reference)
	}
	plainStore := &Store{}
	if protected, err := plainStore.protectProviderCredential("provider-secret"); err != nil || protected != reference {
		t.Fatalf("unconfigured secret protection = %q, %v", protected, err)
	}
	cipher, err := newSecretCipher([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	encryptedStore := &Store{secrets: cipher}
	protected, err := encryptedStore.protectProviderCredential("provider-secret")
	if err != nil || protected == reference || protected == "provider-secret" {
		t.Fatalf("encrypted secret protection = %q, %v", protected, err)
	}
	if databaseText("text") != "text" || databaseText([]byte("bytes")) != "bytes" || databaseText(42) != "42" {
		t.Fatal("databaseText did not normalize driver values")
	}
}

// TestBoundDBSurface covers the small SQL-dialect boundary directly. Domain
// behavior remains covered through Store and Hurl; this test proves every
// wrapper method rebinding queries delegates to the owned sqlx DB/Tx.
func TestBoundDBSurface(t *testing.T) {
	database := testdb.OpenStory(t)
	defer database.Close()
	db := &boundDB{DB: database.SQL}
	ctx := t.Context()

	if _, err := db.ExecContext(ctx, `UPDATE users SET display_name=? WHERE id=?`, "Bound DB", "11000000-0000-4000-8000-000000000001"); err != nil {
		t.Fatal(err)
	}
	var name string
	if err := db.GetContext(ctx, &name, `SELECT display_name FROM users WHERE id=?`, "11000000-0000-4000-8000-000000000001"); err != nil || name != "Bound DB" {
		t.Fatalf("GetContext = %q, %v", name, err)
	}
	var names []string
	if err := db.SelectContext(ctx, &names, `SELECT display_name FROM users WHERE status=? ORDER BY id`, "active"); err != nil || len(names) == 0 {
		t.Fatalf("SelectContext = %v, %v", names, err)
	}
	rows, err := db.QueryContext(ctx, `SELECT id FROM users WHERE status=?`, "active")
	if err != nil {
		t.Fatal(err)
	}
	rows.Close()
	rowsx, err := db.QueryxContext(ctx, `SELECT id FROM users WHERE status=?`, "active")
	if err != nil {
		t.Fatal(err)
	}
	rowsx.Close()
	if err := db.QueryRowxContext(ctx, `SELECT display_name FROM users WHERE id=?`, "11000000-0000-4000-8000-000000000001").Scan(&name); err != nil {
		t.Fatal(err)
	}

	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE users SET display_name=? WHERE id=?`, "Bound Tx", "11000000-0000-4000-8000-000000000001"); err != nil {
		t.Fatal(err)
	}
	if err := tx.GetContext(ctx, &name, `SELECT display_name FROM users WHERE id=?`, "11000000-0000-4000-8000-000000000001"); err != nil || name != "Bound Tx" {
		t.Fatalf("tx GetContext = %q, %v", name, err)
	}
	if err := tx.SelectContext(ctx, &names, `SELECT display_name FROM users WHERE status=?`, "active"); err != nil {
		t.Fatal(err)
	}
	txRows, err := tx.QueryContext(ctx, `SELECT id FROM users WHERE status=?`, "active")
	if err != nil {
		t.Fatal(err)
	}
	txRows.Close()
	txRowsx, err := tx.QueryxContext(ctx, `SELECT id FROM users WHERE status=?`, "active")
	if err != nil {
		t.Fatal(err)
	}
	txRowsx.Close()
	if err := tx.QueryRowxContext(ctx, `SELECT display_name FROM users WHERE id=?`, "11000000-0000-4000-8000-000000000001").Scan(&name); err != nil {
		t.Fatal(err)
	}
}
