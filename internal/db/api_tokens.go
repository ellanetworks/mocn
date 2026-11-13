package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/mattn/go-sqlite3"
)

type APIToken struct {
	ID         int64
	TokenID    string
	Name       string
	TokenHash  []byte
	UserID     int64
	CreatedAt  time.Time
	ExpiresAt  time.Time
	LastUsedAt time.Time
}

type NewAPIToken struct {
	TokenID   string
	Name      string
	TokenHash []byte
	UserID    int64
	CreatedAt time.Time
	ExpiresAt time.Time
}

func (d *DB) CreateAPIToken(ctx context.Context, t NewAPIToken) (_ int64, err error) {
	ctx, done := d.observe(ctx, "CreateAPIToken", poolWrite)
	defer done(&err)

	return write(ctx, d, "create API token", func(tx querier) (int64, error) { return createAPIToken(ctx, tx, t) })
}

func createAPIToken(ctx context.Context, tx querier, t NewAPIToken) (int64, error) {
	res, err := tx.ExecContext(ctx, `INSERT INTO api_tokens (token_id, name, token_hash, user_id, created_at,
		expires_at) VALUES (?, ?, ?, ?, ?, ?)`, t.TokenID, t.Name, t.TokenHash, t.UserID, t.CreatedAt.UnixNano(),
		nullTime(t.ExpiresAt))
	if isConstraint(err, sqlite3.ErrConstraintUnique) {
		return 0, ErrDuplicate
	}

	if err != nil {
		return 0, err
	}

	return res.LastInsertId()
}

func (d *DB) GetAPITokenByTokenID(ctx context.Context, tokenID string) (_ APIToken, err error) {
	ctx, done := d.observe(ctx, "GetAPITokenByTokenID", poolRead)
	defer done(&err)

	t, err := scanAPIToken(d.reader().QueryRowContext(ctx, `SELECT `+apiTokenColumns+` FROM api_tokens WHERE token_id = ?`,
		tokenID))
	if errors.Is(err, sql.ErrNoRows) {
		return APIToken{}, ErrNotFound
	}

	if err != nil {
		return APIToken{}, fmt.Errorf("get API token: %w", err)
	}

	return t, nil
}

func (d *DB) ListAPITokens(ctx context.Context, userID int64, page, perPage int) (_ []APIToken, _ int, err error) {
	ctx, done := d.observe(ctx, "ListAPITokens", poolRead)
	defer done(&err)

	var total int
	if err := d.reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM api_tokens WHERE user_id = ?`, userID).
		Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("list API tokens: %w", err)
	}

	tokens := []APIToken{}

	if err := each(ctx, d.reader(), `SELECT `+apiTokenColumns+` FROM api_tokens WHERE user_id = ?
		ORDER BY id LIMIT ? OFFSET ?`, func(s scanner) error {
		t, err := scanAPIToken(s)
		if err != nil {
			return err
		}

		tokens = append(tokens, t)

		return nil
	}, userID, perPage, (page-1)*perPage); err != nil {
		return nil, 0, fmt.Errorf("list API tokens: %w", err)
	}

	return tokens, total, nil
}

func (d *DB) CountAPITokens(ctx context.Context, userID int64) (_ int, err error) {
	ctx, done := d.observe(ctx, "CountAPITokens", poolRead)
	defer done(&err)

	var n int
	if err := d.reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM api_tokens WHERE user_id = ?`, userID).
		Scan(&n); err != nil {
		return 0, fmt.Errorf("count API tokens: %w", err)
	}

	return n, nil
}

func (d *DB) DeleteAPIToken(ctx context.Context, userID int64, tokenID string) (err error) {
	ctx, done := d.observe(ctx, "DeleteAPIToken", poolWrite)
	defer done(&err)

	return d.exec(ctx, "delete API token", func(tx querier) error {
		res, err := tx.ExecContext(ctx, `DELETE FROM api_tokens WHERE token_id = ? AND user_id = ?`, tokenID, userID)
		if err != nil {
			return err
		}

		return checkAffected(res)
	})
}

func (d *DB) TouchAPIToken(ctx context.Context, id int64, at time.Time) (err error) {
	ctx, done := d.observe(ctx, "TouchAPIToken", poolWrite)
	defer done(&err)

	return d.exec(ctx, "touch API token", func(tx querier) error {
		_, err := tx.ExecContext(ctx, `UPDATE api_tokens SET last_used_at = ? WHERE id = ?`, at.UnixNano(), id)

		return err
	})
}

const apiTokenColumns = `id, token_id, name, token_hash, user_id, created_at, expires_at, last_used_at` // #nosec G101

func scanAPIToken(row scanner) (APIToken, error) {
	var (
		t                     APIToken
		createdAt             int64
		expiresAt, lastUsedAt sql.Null[int64]
	)

	if err := row.Scan(&t.ID, &t.TokenID, &t.Name, &t.TokenHash, &t.UserID, &createdAt, &expiresAt,
		&lastUsedAt); err != nil {
		return APIToken{}, err
	}

	t.CreatedAt = time.Unix(0, createdAt).UTC()
	t.ExpiresAt = fromNullTime(expiresAt)
	t.LastUsedAt = fromNullTime(lastUsedAt)

	return t, nil
}

func nullTime(t time.Time) sql.Null[int64] {
	if t.IsZero() {
		return sql.Null[int64]{}
	}

	return sql.Null[int64]{V: t.UnixNano(), Valid: true}
}

func fromNullTime(t sql.Null[int64]) time.Time {
	if !t.Valid {
		return time.Time{}
	}

	return time.Unix(0, t.V).UTC()
}
