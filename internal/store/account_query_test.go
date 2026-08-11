package store

import "testing"

func TestAccountPageContract(t *testing.T) {
	limit, offset, err := normalizeAccountListQuery(AccountListQuery{})
	if err != nil || limit != defaultAccountPageLimit || offset != 0 {
		t.Fatalf("default query = %d,%d,%v", limit, offset, err)
	}
	limit, offset, err = normalizeAccountListQuery(AccountListQuery{Limit: 2, Cursor: "4"})
	if err != nil || limit != 2 || offset != 4 {
		t.Fatalf("explicit query = %d,%d,%v", limit, offset, err)
	}
	for _, query := range []AccountListQuery{{Limit: -1}, {Limit: 101}, {Cursor: "bad"}, {Cursor: "-1"}} {
		if _, _, err := normalizeAccountListQuery(query); err == nil {
			t.Fatalf("invalid query accepted: %+v", query)
		}
	}
	page := accountPage([]int{1, 2, 3}, 2, 4)
	if !page.HasMore || len(page.Items) != 2 || page.NextCursor == nil || *page.NextCursor != "6" {
		t.Fatalf("page = %+v", page)
	}
}
