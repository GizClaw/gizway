package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jmoiron/sqlx"
)

type sqlStateError interface{ SQLState() string }

func isSerializationFailure(err error) bool {
	var state sqlStateError
	return errors.As(err, &state) && (state.SQLState() == "40001" || state.SQLState() == "40P01")
}

// retrySerializable reruns the complete database command after PostgreSQL
// serialization/deadlock aborts. Retrying only Commit is invalid because the
// transaction snapshot and every decision derived from it must be rebuilt.
func retrySerializable[T any](ctx context.Context, operation func() (T, error)) (T, error) {
	// A Store command invoked inside ExecuteAPICommand has borrowed the outer
	// transaction. PostgreSQL marks that whole transaction aborted after
	// SQLSTATE 40001/40P01, so retrying an inner savepoint can never recover it.
	// The command-level owner records the SQLSTATE and rebuilds the full handler.
	if commandTransaction(ctx) != nil {
		return operation()
	}
	var zero T
	for attempt := range 4 {
		value, err := operation()
		if !isSerializationFailure(err) {
			return value, err
		}
		if attempt == 3 {
			return zero, err
		}
		timer := time.NewTimer(time.Duration(attempt+1) * 5 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return zero, ctx.Err()
		case <-timer.C:
		}
	}
	return zero, errors.New("unreachable serialization retry state")
}

func retrySerializableError(ctx context.Context, operation func() error) error {
	_, err := retrySerializable(ctx, func() (struct{}, error) { return struct{}{}, operation() })
	return err
}

// boundDB keeps Store queries readable while sqlx rewrites question-mark
// placeholders to PostgreSQL positional parameters.
type boundDB struct {
	*sqlx.DB
	savepointSequence atomic.Uint64
}

type commandTransactionContextKey struct{}
type commandRetryCollectorContextKey struct{}

type commandRetryCollector struct {
	mu  sync.Mutex
	err error
}

func (collector *commandRetryCollector) record(err error) {
	if collector == nil || !isSerializationFailure(err) {
		return
	}
	collector.mu.Lock()
	defer collector.mu.Unlock()
	if collector.err == nil {
		collector.err = err
	}
}

func (collector *commandRetryCollector) failure() error {
	if collector == nil {
		return nil
	}
	collector.mu.Lock()
	defer collector.mu.Unlock()
	return collector.err
}

func withCommandRetryCollector(ctx context.Context, collector *commandRetryCollector) context.Context {
	return context.WithValue(ctx, commandRetryCollectorContextKey{}, collector)
}

func recordCommandRetryFailure(ctx context.Context, err error) error {
	collector, _ := ctx.Value(commandRetryCollectorContextKey{}).(*commandRetryCollector)
	collector.record(err)
	return err
}

func scanCommandRow(ctx context.Context, row *sqlx.Row, destination ...any) error {
	return recordCommandRetryFailure(ctx, row.Scan(destination...))
}

func commandTransaction(ctx context.Context) *sqlx.Tx {
	tx, _ := ctx.Value(commandTransactionContextKey{}).(*sqlx.Tx)
	return tx
}

func withCommandTransaction(ctx context.Context, tx *sqlx.Tx) context.Context {
	return context.WithValue(ctx, commandTransactionContextKey{}, tx)
}

func (db *boundDB) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	var result sql.Result
	var err error
	if tx := commandTransaction(ctx); tx != nil {
		result, err = tx.ExecContext(ctx, db.Rebind(query), args...)
	} else {
		result, err = db.DB.ExecContext(ctx, db.Rebind(query), args...)
	}
	return result, recordCommandRetryFailure(ctx, err)
}

func (db *boundDB) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	var rows *sql.Rows
	var err error
	if tx := commandTransaction(ctx); tx != nil {
		rows, err = tx.QueryContext(ctx, db.Rebind(query), args...)
	} else {
		rows, err = db.DB.QueryContext(ctx, db.Rebind(query), args...)
	}
	return rows, recordCommandRetryFailure(ctx, err)
}

func (db *boundDB) QueryxContext(ctx context.Context, query string, args ...any) (*sqlx.Rows, error) {
	var rows *sqlx.Rows
	var err error
	if tx := commandTransaction(ctx); tx != nil {
		rows, err = tx.QueryxContext(ctx, db.Rebind(query), args...)
	} else {
		rows, err = db.DB.QueryxContext(ctx, db.Rebind(query), args...)
	}
	return rows, recordCommandRetryFailure(ctx, err)
}

func (db *boundDB) QueryRowxContext(ctx context.Context, query string, args ...any) *sqlx.Row {
	if tx := commandTransaction(ctx); tx != nil {
		return tx.QueryRowxContext(ctx, db.Rebind(query), args...)
	}
	return db.DB.QueryRowxContext(ctx, db.Rebind(query), args...)
}

func (db *boundDB) GetContext(ctx context.Context, destination any, query string, args ...any) error {
	var err error
	if tx := commandTransaction(ctx); tx != nil {
		err = tx.GetContext(ctx, destination, db.Rebind(query), args...)
	} else {
		err = db.DB.GetContext(ctx, destination, db.Rebind(query), args...)
	}
	return recordCommandRetryFailure(ctx, err)
}

func (db *boundDB) SelectContext(ctx context.Context, destination any, query string, args ...any) error {
	var err error
	if tx := commandTransaction(ctx); tx != nil {
		err = tx.SelectContext(ctx, destination, db.Rebind(query), args...)
	} else {
		err = db.DB.SelectContext(ctx, destination, db.Rebind(query), args...)
	}
	return recordCommandRetryFailure(ctx, err)
}

func (db *boundDB) BeginTxx(ctx context.Context, options *sql.TxOptions) (*boundTx, error) {
	if tx := commandTransaction(ctx); tx != nil {
		name := fmt.Sprintf("gizway_nested_%d", db.savepointSequence.Add(1))
		if _, err := tx.ExecContext(ctx, "SAVEPOINT "+name); err != nil {
			return nil, err
		}
		return &boundTx{Tx: tx, rebind: db.Rebind, borrowed: true, savepoint: name}, nil
	}
	// Store transactions default to SERIALIZABLE because balance checks,
	// reservations, ledger postings and last-active-admin checks span multiple
	// rows. PostgreSQL READ COMMITTED permits write skew across those reads.
	// Callers may still pass an explicit stronger/read-only policy when needed.
	if options == nil {
		options = &sql.TxOptions{Isolation: sql.LevelSerializable}
	}
	tx, err := db.DB.BeginTxx(ctx, options)
	if err != nil {
		return nil, err
	}
	return &boundTx{Tx: tx, rebind: db.Rebind}, nil
}

type boundTx struct {
	*sqlx.Tx
	rebind    func(string) string
	borrowed  bool
	savepoint string
	done      bool
}

// Commit and Rollback are no-ops for a transaction borrowed from the atomic
// API-command scope. The outer command owns the one real commit/rollback, so
// existing Store methods can retain their normal transaction structure.
func (tx *boundTx) Commit() error {
	if tx.borrowed {
		if tx.done {
			return nil
		}
		_, err := tx.Tx.Exec("RELEASE SAVEPOINT " + tx.savepoint)
		tx.done = err == nil
		return err
	}
	return tx.Tx.Commit()
}

func (tx *boundTx) Rollback() error {
	if tx.borrowed {
		if tx.done {
			return nil
		}
		if _, err := tx.Tx.Exec("ROLLBACK TO SAVEPOINT " + tx.savepoint); err != nil {
			return err
		}
		_, err := tx.Tx.Exec("RELEASE SAVEPOINT " + tx.savepoint)
		tx.done = err == nil
		return err
	}
	return tx.Tx.Rollback()
}

func (tx *boundTx) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	result, err := tx.Tx.ExecContext(ctx, tx.rebind(query), args...)
	return result, recordCommandRetryFailure(ctx, err)
}

func (tx *boundTx) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	rows, err := tx.Tx.QueryContext(ctx, tx.rebind(query), args...)
	return rows, recordCommandRetryFailure(ctx, err)
}

func (tx *boundTx) QueryxContext(ctx context.Context, query string, args ...any) (*sqlx.Rows, error) {
	rows, err := tx.Tx.QueryxContext(ctx, tx.rebind(query), args...)
	return rows, recordCommandRetryFailure(ctx, err)
}

func (tx *boundTx) QueryRowxContext(ctx context.Context, query string, args ...any) *sqlx.Row {
	return tx.Tx.QueryRowxContext(ctx, tx.rebind(query), args...)
}

func (tx *boundTx) GetContext(ctx context.Context, destination any, query string, args ...any) error {
	return recordCommandRetryFailure(ctx, tx.Tx.GetContext(ctx, destination, tx.rebind(query), args...))
}

func (tx *boundTx) SelectContext(ctx context.Context, destination any, query string, args ...any) error {
	return recordCommandRetryFailure(ctx, tx.Tx.SelectContext(ctx, destination, tx.rebind(query), args...))
}
