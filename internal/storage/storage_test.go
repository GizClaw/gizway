package storage_test

import (
	"testing"

	"github.com/idy/gizway/internal/storage"
	"github.com/idy/gizway/internal/testdb"
)

func TestOpenPostgreSQLModes(t *testing.T) {
	t.Run("empty initialized", func(t *testing.T) {
		database, err := storage.OpenPostgreSQL(testdb.NewSchema(t), true)
		if err != nil {
			t.Fatalf("OpenPostgreSQL: %v", err)
		}
		defer database.Close()
		var users int
		if err := database.SQL.Get(&users, `SELECT COUNT(*) FROM users`); err != nil || users != 0 {
			t.Fatalf("empty users=%d err=%v", users, err)
		}
	})

	t.Run("development seed", func(t *testing.T) {
		database, err := storage.OpenDevelopmentPostgreSQL(testdb.NewSchema(t), true, true)
		if err != nil {
			t.Fatalf("OpenDevelopmentPostgreSQL: %v", err)
		}
		defer database.Close()
		var email string
		if err := database.SQL.Get(&email, `SELECT email FROM users`); err != nil || email != "demo@gizway.dev" {
			t.Fatalf("development user=%q err=%v", email, err)
		}
	})

	t.Run("story seed and reopen", func(t *testing.T) {
		dsn := testdb.NewSchema(t)
		database, err := storage.OpenStoryPostgreSQL(dsn)
		if err != nil {
			t.Fatalf("OpenStoryPostgreSQL: %v", err)
		}
		var users int
		if err := database.SQL.Get(&users, `SELECT COUNT(*) FROM users`); err != nil || users != 3 {
			t.Fatalf("story users=%d err=%v", users, err)
		}
		reopened, err := storage.OpenExistingPostgreSQL(dsn)
		if err != nil {
			_ = database.Close()
			t.Fatalf("OpenExistingPostgreSQL: %v", err)
		}
		defer reopened.Close()
		defer database.Close()
		if err := reopened.SQL.Get(&users, `SELECT COUNT(*) FROM users`); err != nil || users != 3 {
			t.Fatalf("reopened users=%d err=%v", users, err)
		}
	})
}

func TestOpenPostgreSQLRequiresLiveDatabase(t *testing.T) {
	_, err := storage.OpenPostgreSQL("host=127.0.0.1 port=1 user=gizway dbname=gizway sslmode=disable connect_timeout=1", false)
	if err == nil {
		t.Fatal("OpenPostgreSQL without a live database succeeded")
	}
}

func TestOpenPostgreSQLSetupFailures(t *testing.T) {
	if _, err := storage.OpenDevelopmentPostgreSQL(testdb.NewSchema(t), false, true); err == nil {
		t.Fatal("development seed without schema succeeded")
	}

	dsn := testdb.NewSchema(t)
	first, err := storage.OpenPostgreSQL(dsn, true)
	if err != nil {
		t.Fatalf("first OpenPostgreSQL: %v", err)
	}
	defer first.Close()
	if _, err := storage.OpenPostgreSQL(dsn, true); err == nil {
		t.Fatal("duplicate schema initialization succeeded")
	}

	empty, err := storage.OpenPostgreSQL(testdb.NewSchema(t), false)
	if err != nil {
		t.Fatalf("open uninitialized PostgreSQL: %v", err)
	}
	if err := empty.Close(); err != nil {
		t.Fatalf("close uninitialized storage: %v", err)
	}
}
