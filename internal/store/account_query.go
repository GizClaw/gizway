package store

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strconv"
)

const (
	defaultAccountPageLimit = 20
	maxAccountPageLimit     = 100
)

// AccountListQuery is the bounded public-list contract. The cursor is an
// opaque API value represented internally as an offset. Keeping parsing in the
// Store as well as the HTTP layer protects non-HTTP callers from accidentally
// issuing an unbounded production query.
type AccountListQuery struct {
	Cursor   string
	Limit    int
	APIKeyID string
	AsOf     string
}

// AccountPage keeps the data and continuation decision together so handlers
// cannot accidentally return `has_more:false` for a truncated query.
type AccountPage[T any] struct {
	Items      []T
	NextCursor *string
	HasMore    bool
	AsOf       string
}

type receivedUsageCursor struct {
	Offset int    `json:"offset"`
	AsOf   string `json:"as_of"`
}

func decodeReceivedUsageCursor(raw, initialAsOf string) (int, string, error) {
	if raw == "" {
		return 0, initialAsOf, nil
	}
	encoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return 0, "", errors.New("invalid received Usage cursor")
	}
	var cursor receivedUsageCursor
	if json.Unmarshal(encoded, &cursor) != nil || cursor.Offset < 0 || cursor.AsOf == "" {
		return 0, "", errors.New("invalid received Usage cursor")
	}
	return cursor.Offset, cursor.AsOf, nil
}

func encodeReceivedUsageCursor(offset int, asOf string) string {
	encoded, _ := json.Marshal(receivedUsageCursor{Offset: offset, AsOf: asOf})
	return base64.RawURLEncoding.EncodeToString(encoded)
}

func normalizeAccountListQuery(query AccountListQuery) (limit, offset int, err error) {
	limit = query.Limit
	if limit == 0 {
		limit = defaultAccountPageLimit
	}
	if limit < 1 || limit > maxAccountPageLimit {
		return 0, 0, errors.New("account page limit must be between 1 and 100")
	}
	if query.Cursor == "" {
		return limit, 0, nil
	}
	offset, err = strconv.Atoi(query.Cursor)
	if err != nil || offset < 0 {
		return 0, 0, errors.New("invalid account page cursor")
	}
	return limit, offset, nil
}

func accountPage[T any](items []T, limit, offset int) AccountPage[T] {
	page := AccountPage[T]{Items: items}
	if len(items) > limit {
		page.HasMore = true
		page.Items = items[:limit]
		cursor := strconv.Itoa(offset + limit)
		page.NextCursor = &cursor
	}
	return page
}
