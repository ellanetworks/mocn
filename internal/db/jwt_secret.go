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

func (d *DB) Secrets(ctx context.Context) (Secrets, error) {
	return d.InitSecrets(ctx, d.currentProposer())
}

func (d *DB) ReadSecrets(ctx context.Context) (_ Secrets, err error) {
	defer d.observe(poolRead, &err)()

	return readSecrets(ctx, d.reader())
}

func (d *DB) InitSecrets(ctx context.Context, via Proposer) (_ Secrets, err error) {
	defer d.observe(poolWrite, &err)()

	s, err := readSecrets(ctx, d.reader())
	if err == nil {
		return s, nil
	}

	if !errors.Is(err, sql.ErrNoRows) {
		return Secrets{}, fmt.Errorf("read secrets: %w", err)
	}

	candidate := Secrets{JWT: make([]byte, jwtSecretLength)}
	_, _ = rand.Read(candidate.JWT)

	s, _, err = submitVia[Secrets](ctx, via, cmdInitSecrets, candidate)
	if err != nil {
		return Secrets{}, fmt.Errorf("init secrets: %w", err)
	}

	return s, nil
}

func (d *DB) JWTSecret(ctx context.Context) ([]byte, error) {
	s, err := d.Secrets(ctx)
	return s.JWT, err
}

func applyInitSecrets(a *applyCtx, candidate Secrets) (Secrets, error) {
	if _, err := a.tx.ExecContext(a.ctx, `INSERT OR IGNORE INTO jwt_secret (id, secret) VALUES (1, ?)`,
		candidate.JWT); err != nil {
		return Secrets{}, err
	}

	return readSecrets(a.ctx, a.tx)
}

func readSecrets(ctx context.Context, q querier) (Secrets, error) {
	var s Secrets

	err := q.QueryRowContext(ctx, `SELECT secret FROM jwt_secret WHERE id = 1`).Scan(&s.JWT)

	return s, err
}

func init() {
	register(cmdInitSecrets, handle(applyInitSecrets))
}
