package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/mattn/go-sqlite3"
)

type Identity struct {
	ID          int64
	UserID      int64
	Issuer      string
	Subject     string
	Email       string
	CreatedAt   time.Time
	LastLoginAt time.Time
}

type NewIdentity struct {
	Issuer  string
	Subject string
	Email   string
	At      time.Time
}

func (d *DB) GetIdentity(ctx context.Context, issuer, subject string) (_ Identity, err error) {
	ctx, done := d.observe(ctx, "GetIdentity", poolRead)
	defer done(&err)

	var (
		i                    Identity
		createdAt, lastLogin int64
	)

	err = d.reader().QueryRowContext(ctx, `SELECT id, user_id, issuer, subject, email, created_at, last_login_at
		FROM user_identities WHERE issuer = ? AND subject = ?`, issuer, subject).Scan(&i.ID, &i.UserID, &i.Issuer,
		&i.Subject, &i.Email, &createdAt, &lastLogin)
	if errors.Is(err, sql.ErrNoRows) {
		return Identity{}, ErrNotFound
	}

	if err != nil {
		return Identity{}, fmt.Errorf("get identity: %w", err)
	}

	i.CreatedAt = time.Unix(0, createdAt).UTC()
	i.LastLoginAt = time.Unix(0, lastLogin).UTC()

	return i, nil
}

func (d *DB) CreateIdentity(ctx context.Context, userID int64, i NewIdentity) (_ int64, err error) {
	ctx, done := d.observe(ctx, "CreateIdentity", poolWrite)
	defer done(&err)

	return write(ctx, d, "create identity", func(tx querier) (int64, error) {
		return createIdentity(ctx, tx, userID, i)
	})
}

func createIdentity(ctx context.Context, tx querier, userID int64, i NewIdentity) (int64, error) {
	res, err := tx.ExecContext(ctx, `INSERT INTO user_identities
		(user_id, issuer, subject, email, created_at, last_login_at) VALUES (?, ?, ?, ?, ?, ?)`,
		userID, i.Issuer, i.Subject, i.Email, i.At.UnixNano(), i.At.UnixNano())
	if isConstraint(err, sqlite3.ErrConstraintUnique) {
		return 0, ErrDuplicate
	}

	if err != nil {
		return 0, err
	}

	return res.LastInsertId()
}

func (d *DB) DeleteIdentities(ctx context.Context, userID int64) (err error) {
	ctx, done := d.observe(ctx, "DeleteIdentities", poolWrite)
	defer done(&err)

	return d.exec(ctx, "delete identities", func(tx querier) error {
		var linked bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM user_identities JOIN oidc
			ON oidc.issuer = user_identities.issuer WHERE user_identities.user_id = ?)`, userID).Scan(&linked); err != nil {
			return err
		}

		if !linked {
			return ErrNotFound
		}

		_, err := tx.ExecContext(ctx, `DELETE FROM user_identities WHERE user_id = ?`, userID)

		return err
	})
}
