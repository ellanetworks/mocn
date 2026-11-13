package db

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
)

const jwtSecretLength = 32

type Secrets struct {
	JWT []byte
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

	candidate := Secrets{JWT: make([]byte, jwtSecretLength)}
	_, _ = rand.Read(candidate.JWT)

	return write(ctx, d, "init secrets", func(tx querier) (Secrets, error) {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO jwt_secret (id, secret) VALUES (1, ?)`,
			candidate.JWT); err != nil {
			return Secrets{}, err
		}

		return readSecrets(ctx, tx)
	})
}

func (d *DB) JWTSecret(ctx context.Context) ([]byte, error) {
	s, err := d.InitSecrets(ctx)
	return s.JWT, err
}

func readSecrets(ctx context.Context, q querier) (Secrets, error) {
	var s Secrets

	err := q.QueryRowContext(ctx, `SELECT secret FROM jwt_secret WHERE id = 1`).Scan(&s.JWT)

	return s, err
}
