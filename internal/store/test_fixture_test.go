package store_test

import (
	"testing"

	"github.com/idy/gizway/internal/store"
	"github.com/idy/gizway/internal/testdb"
)

const (
	userOneID    = "11000000-0000-4000-8000-000000000001"
	userOneKey   = "giz_story_user_active_1"
	accountOneID = "21000000-0000-4000-8000-000000000001"
	accountTwoID = "21000000-0000-4000-8000-000000000002"
)

func newStore(t *testing.T) *store.Store {
	t.Helper()
	database := testdb.OpenGizPayStory(t)
	return store.New(database.SQL)
}
