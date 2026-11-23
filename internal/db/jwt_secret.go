package db

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
)

const (
	jwtSecretLength     = 32
	oidcCookieKeyLength = 32
)

type Secrets struct {
	JWT           []byte
	OIDCCookieKey []byte
}

func (d *DB) InitSecrets(ctx context.Context) (_ Secrets, err error) {
	ctx, done := d.observe(ctx, "InitSecrets", poolWrite)
	defer done(&err)

	s, err := readSecrets(ctx, d.reader())
	if err == nil {
		return s, nil
	}

	if !errors.Is(err, sql.ErrNoRows) {
		return Secrets{}, fmt.Errorf("read secrets: %w", err)
	}

	candidate := Secrets{JWT: make([]byte, jwtSecretLength), OIDCCookieKey: make([]byte, oidcCookieKeyLength)}
	_, _ = rand.Read(candidate.JWT)
	_, _ = rand.Read(candidate.OIDCCookieKey)

	return write(ctx, d, "init secrets", func(tx querier) (Secrets, error) {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO jwt_secret (id, secret) VALUES (1, ?)`,
			candidate.JWT); err != nil {
			return Secrets{}, err
		}

		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO oidc_cookie_key (id, key) VALUES (1, ?)`,
			candidate.OIDCCookieKey); err != nil {
			return Secrets{}, err
		}

		return readSecrets(ctx, tx)
	})
}

func (d *DB) JWTSecret(ctx context.Context) ([]byte, error) {
	s, err := d.InitSecrets(ctx)
	return s.JWT, err
}

func (d *DB) OIDCCookieKey(ctx context.Context) ([]byte, error) {
	s, err := d.InitSecrets(ctx)
	return s.OIDCCookieKey, err
}

func readSecrets(ctx context.Context, q querier) (Secrets, error) {
	var s Secrets

	err := q.QueryRowContext(ctx, `SELECT j.secret, o.key FROM jwt_secret j, oidc_cookie_key o
		WHERE j.id = 1 AND o.id = 1`).Scan(&s.JWT, &s.OIDCCookieKey)

	return s, err
}
