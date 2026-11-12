package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mattn/go-sqlite3"
)

var (
	ErrNotFound          = errors.New("not found")
	ErrDuplicate         = errors.New("duplicate")
	ErrLastPasswordAdmin = errors.New("the last admin with a password")
	ErrTooManyUsers      = errors.New("too many users")
	ErrNoPassword        = errors.New("the user has no password")
	ErrForeignPLMN       = errors.New("the PLMN is not one of the operator's")
)

type DB struct {
	writer      *sql.DB
	readers     atomic.Pointer[sql.DB]
	checkpoints *checkpointer
	path        string
	metrics     metrics
	proposer    atomic.Pointer[proposerRef]
	applyMu     sync.Mutex
	applied     atomic.Uint64
	appliedMu   sync.Mutex
	appliedCh   chan struct{}
	schema      atomic.Int64
}

const (
	baselineSchema      = 1
	readConns           = 8
	retiredReadersGrace = 30 * time.Second
)

var migrations = []string{
	`CREATE TABLE users (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		email TEXT NOT NULL UNIQUE COLLATE NOCASE,
		role_id INTEGER NOT NULL CHECK (role_id IN (1, 2, 3)),
		role_pinned INTEGER NOT NULL DEFAULT 0 CHECK (role_pinned IN (0, 1)),
		password_hash TEXT CHECK (password_hash != '')
	);
	CREATE TABLE user_identities (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
		issuer TEXT NOT NULL CHECK (issuer != ''),
		subject TEXT NOT NULL CHECK (length(CAST(subject AS BLOB)) BETWEEN 1 AND 255),
		email TEXT NOT NULL,
		created_at INTEGER NOT NULL,
		last_login_at INTEGER NOT NULL,
		UNIQUE (issuer, subject),
		UNIQUE (user_id, issuer)
	);
	CREATE TABLE sessions (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
		token_hash BLOB NOT NULL UNIQUE,
		method TEXT NOT NULL CHECK (method IN ('password', 'oidc')),
		identity_id INTEGER REFERENCES user_identities (id) ON DELETE CASCADE,
		created_at INTEGER NOT NULL,
		expires_at INTEGER NOT NULL,
		CHECK ((method = 'oidc') = (identity_id IS NOT NULL))
	);
	CREATE INDEX sessions_user_id ON sessions (user_id, created_at);
	CREATE INDEX sessions_expires_at ON sessions (expires_at);
	CREATE INDEX sessions_identity_id ON sessions (identity_id);
	CREATE TABLE api_tokens (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		token_id TEXT NOT NULL UNIQUE,
		name TEXT NOT NULL,
		token_hash BLOB NOT NULL,
		user_id INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
		created_at INTEGER NOT NULL,
		expires_at INTEGER,
		last_used_at INTEGER,
		UNIQUE (user_id, name)
	);
	CREATE TABLE jwt_secret (
		id INTEGER PRIMARY KEY CHECK (id = 1),
		secret BLOB NOT NULL CHECK (length(secret) >= 32)
	);
	CREATE TABLE fsm_state (
		id INTEGER PRIMARY KEY CHECK (id = 1),
		applied_index INTEGER NOT NULL,
		applied_term INTEGER NOT NULL,
		leader_time INTEGER NOT NULL
	);
	INSERT INTO fsm_state VALUES (1, 0, 0, 0);
	CREATE TABLE applied_requests (
		request_id BLOB PRIMARY KEY CHECK (length(request_id) = 16),
		result BLOB NOT NULL,
		applied_at INTEGER NOT NULL
	) WITHOUT ROWID;
	CREATE INDEX applied_requests_applied_at ON applied_requests (applied_at);
	CREATE TABLE operators (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL UNIQUE CHECK (name != ''),
		pws_operator INTEGER NOT NULL DEFAULT 0 CHECK (pws_operator IN (0, 1))
	);
	CREATE UNIQUE INDEX operators_pws_operator ON operators (pws_operator) WHERE pws_operator = 1;
	CREATE TABLE operator_plmns (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		operator_id INTEGER NOT NULL REFERENCES operators (id) ON DELETE CASCADE,
		mcc TEXT NOT NULL CHECK (length(mcc) = 3 AND mcc NOT GLOB '*[^0-9]*'),
		mnc TEXT NOT NULL CHECK (length(mnc) IN (2, 3) AND mnc NOT GLOB '*[^0-9]*'),
		UNIQUE (mcc, mnc)
	);
	CREATE INDEX operator_plmns_operator_id ON operator_plmns (operator_id);
	CREATE TABLE global_enb_ids (
		operator_id INTEGER PRIMARY KEY REFERENCES operators (id) ON DELETE CASCADE,
		operator_plmn_id INTEGER NOT NULL REFERENCES operator_plmns (id),
		enb_id_type TEXT NOT NULL CHECK (enb_id_type IN ('macro', 'home', 'short_macro', 'long_macro')),
		enb_id INTEGER NOT NULL CHECK (enb_id >= 0 AND enb_id < CASE enb_id_type
			WHEN 'macro' THEN 1 << 20 WHEN 'home' THEN 1 << 28 WHEN 'short_macro' THEN 1 << 18 ELSE 1 << 21 END)
	);
	CREATE TABLE global_gnb_ids (
		operator_id INTEGER PRIMARY KEY REFERENCES operators (id) ON DELETE CASCADE,
		operator_plmn_id INTEGER NOT NULL REFERENCES operator_plmns (id),
		gnb_id INTEGER NOT NULL,
		gnb_id_length INTEGER NOT NULL CHECK (gnb_id_length BETWEEN 22 AND 32),
		CHECK (gnb_id >= 0 AND gnb_id < 1 << gnb_id_length)
	);
	CREATE TABLE mmes (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		operator_id INTEGER NOT NULL REFERENCES operators (id) ON DELETE CASCADE,
		address TEXT NOT NULL UNIQUE CHECK (address != '')
	);
	CREATE INDEX mmes_operator_id ON mmes (operator_id);
	CREATE TABLE amfs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		operator_id INTEGER NOT NULL REFERENCES operators (id) ON DELETE CASCADE,
		address TEXT NOT NULL UNIQUE CHECK (address != '')
	);
	CREATE INDEX amfs_operator_id ON amfs (operator_id);`,
}

func Open(ctx context.Context, path string) (*DB, error) {
	if err := restrict(path); err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	d := &DB{path: path, metrics: newMetrics()}

	if err := d.openConns(ctx); err != nil {
		return nil, err
	}

	state, err := readFSMState(ctx, d.writer)
	if err != nil {
		_ = d.Close()
		return nil, fmt.Errorf("open database: read state: %w", err)
	}

	d.setApplied(state.appliedIndex)
	d.proposer.Store(&proposerRef{&localProposer{d: d, now: time.Now, last: state.leaderTime}})

	return d, nil
}

const dsnParams = "_foreign_keys=on&_journal_mode=WAL&_busy_timeout=5000&_stmt_cache_size=64"

func dsn(path string) string {
	return "file:" + (&url.URL{Path: path}).EscapedPath()
}

func (d *DB) openConns(ctx context.Context) error {
	writer, err := sql.Open("sqlite3", dsn(d.path)+"?"+dsnParams+"&_txlock=immediate")
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}

	writer.SetMaxOpenConns(1)
	writer.SetMaxIdleConns(1)

	for _, pragma := range []string{"PRAGMA wal_autocheckpoint = 0", "PRAGMA temp_store = MEMORY"} {
		if _, err := writer.ExecContext(ctx, pragma); err != nil {
			_ = writer.Close()
			return fmt.Errorf("open database: %w", err)
		}
	}

	d.writer = writer

	if err := d.migrate(ctx); err != nil {
		_ = writer.Close()
		return err
	}

	if err := checkForeignKeys(ctx, writer); err != nil {
		_ = writer.Close()
		return err
	}

	reader, err := sql.Open("sqlite3", dsn(d.path)+"?"+dsnParams+"&_query_only=1")
	if err != nil {
		_ = writer.Close()
		return fmt.Errorf("open database: %w", err)
	}

	reader.SetMaxOpenConns(readConns)
	reader.SetMaxIdleConns(readConns)

	if err := reader.PingContext(ctx); err != nil {
		_ = writer.Close()
		_ = reader.Close()

		return fmt.Errorf("open database: %w", err)
	}

	checkpoints, err := startCheckpointer(ctx, d.path, writer)
	if err != nil {
		_ = writer.Close()
		_ = reader.Close()

		return fmt.Errorf("open database: %w", err)
	}

	d.checkpoints = checkpoints

	if old := d.readers.Swap(reader); old != nil {
		time.AfterFunc(retiredReadersGrace, func() { _ = old.Close() })
	}

	return nil
}

func (d *DB) closeConns() error {
	return errors.Join(d.closeWriter(), d.reader().Close())
}

func (d *DB) closeWriter() error {
	return errors.Join(d.checkpoints.stop(), d.writer.Close())
}

func (d *DB) reader() *sql.DB {
	return d.readers.Load()
}

func (d *DB) Ping(ctx context.Context) error {
	return d.reader().PingContext(ctx)
}

func (d *DB) Close() error {
	return d.closeConns()
}

func (d *DB) migrate(ctx context.Context) error {
	var version int
	if err := d.writer.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}

	if version > len(migrations) {
		return fmt.Errorf("schema version %d is newer than this binary supports (%d): use a newer binary, or a new database",
			version, len(migrations))
	}

	d.schema.Store(int64(version))

	if version > 0 {
		return nil
	}

	if err := migrateFile(ctx, d.writer, migrations[:baselineSchema], "the database"); err != nil {
		return err
	}

	d.schema.Store(baselineSchema)

	return nil
}

func migrateFile(ctx context.Context, conn *sql.DB, steps []string, what string) error {
	var version int
	if err := conn.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("read the schema version of %s: %w", what, err)
	}

	if version > len(steps) {
		return fmt.Errorf("the schema version %d of %s is newer than this binary supports (%d): use a newer binary",
			version, what, len(steps))
	}

	for i := version; i < len(steps); i++ {
		tx, err := conn.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("migrate %s to %d: %w", what, i+1, err)
		}

		if _, err := tx.ExecContext(ctx, steps[i]); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("migrate %s to %d: %w", what, i+1, err)
		}

		if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", i+1)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("migrate %s to %d: %w", what, i+1, err)
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("migrate %s to %d: %w", what, i+1, err)
		}
	}

	return nil
}

func checkForeignKeys(ctx context.Context, q querier) error {
	rows, err := q.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return fmt.Errorf("check foreign keys: %w", err)
	}

	defer func() { _ = rows.Close() }()

	if rows.Next() {
		var (
			table  string
			rowid  sql.NullInt64
			parent string
			fkid   int
		)

		if err := rows.Scan(&table, &rowid, &parent, &fkid); err != nil {
			return fmt.Errorf("check foreign keys: %w", err)
		}

		return fmt.Errorf("database: a row of %s references no row of %s: the database was changed outside the MOCN", table, parent)
	}

	return rows.Err()
}

func restrict(path string) error {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600) // #nosec G304
	if err != nil {
		return err
	}

	if err := f.Close(); err != nil {
		return err
	}

	return os.Chmod(path, 0o600)
}

type scanner interface {
	Scan(dest ...any) error
}

func isConstraint(err error, code sqlite3.ErrNoExtended) bool {
	var e sqlite3.Error

	return errors.As(err, &e) && e.ExtendedCode == code
}

func checkAffected(res sql.Result) error {
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}

	if n == 0 {
		return ErrNotFound
	}

	return nil
}

func nullableString(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}
