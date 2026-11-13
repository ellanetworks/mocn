package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/mattn/go-sqlite3"
)

type Role int

const (
	RoleAdmin          Role = 1
	RoleReadOnly       Role = 2
	RoleNetworkManager Role = 3
)

type User struct {
	ID           int64
	Email        string
	Role         Role
	RolePinned   bool
	PasswordHash string
	HasIdentity  bool
}

type NewUser struct {
	Email        string
	Role         Role
	RolePinned   bool
	PasswordHash string
}

func (d *DB) CountUsers(ctx context.Context) (_ int, err error) {
	ctx, done := d.observe(ctx, "CountUsers", poolRead)
	defer done(&err)

	var n int
	if err := d.reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
		return 0, fmt.Errorf("count users: %w", err)
	}

	return n, nil
}

func (d *DB) CreateUser(ctx context.Context, u NewUser, maxUsers int) (id int64, err error) {
	ctx, done := d.observe(ctx, "CreateUser", poolWrite)
	defer done(&err)

	return write(ctx, d, "create user", func(tx querier) (int64, error) { return createUser(ctx, tx, u, maxUsers) })
}

func createUser(ctx context.Context, tx querier, u NewUser, maxUsers int) (int64, error) {
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
		return 0, err
	}

	if n >= maxUsers {
		return 0, ErrTooManyUsers
	}

	res, err := tx.ExecContext(ctx, `INSERT INTO users (email, role_id, role_pinned, password_hash) VALUES (?, ?, ?, ?)`,
		u.Email, u.Role, u.RolePinned, nullableString(u.PasswordHash))
	if isConstraint(err, sqlite3.ErrConstraintUnique) {
		return 0, ErrDuplicate
	}

	if err != nil {
		return 0, err
	}

	return res.LastInsertId()
}

func (d *DB) GetUser(ctx context.Context, email string) (_ User, err error) {
	ctx, done := d.observe(ctx, "GetUser", poolRead)
	defer done(&err)

	return getUser(d.reader().QueryRowContext(ctx, `SELECT `+userColumns+` FROM users WHERE email = ?`, email))
}

func (d *DB) GetUserByID(ctx context.Context, id int64) (_ User, err error) {
	ctx, done := d.observe(ctx, "GetUserByID", poolRead)
	defer done(&err)

	return getUser(d.reader().QueryRowContext(ctx, `SELECT `+userColumns+` FROM users WHERE id = ?`, id))
}

func (d *DB) ListUsers(ctx context.Context, page, perPage int) (_ []User, _ int, err error) {
	ctx, done := d.observe(ctx, "ListUsers", poolRead)
	defer done(&err)

	var total int
	if err := d.reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("list users: %w", err)
	}

	users := []User{}

	if err := each(ctx, d.reader(), `SELECT `+userColumns+` FROM users ORDER BY id LIMIT ? OFFSET ?`,
		func(s scanner) error {
			u, err := scanUser(s)
			if err != nil {
				return err
			}

			users = append(users, u)

			return nil
		}, perPage, (page-1)*perPage); err != nil {
		return nil, 0, fmt.Errorf("list users: %w", err)
	}

	return users, total, nil
}

func (d *DB) UpdateUserRole(ctx context.Context, id int64, role Role, pinned bool) (err error) {
	ctx, done := d.observe(ctx, "UpdateUserRole", poolWrite)
	defer done(&err)

	return d.exec(ctx, "update user role", func(tx querier) error { return updateUserRole(ctx, tx, id, role, pinned) })
}

func updateUserRole(ctx context.Context, tx querier, id int64, role Role, pinned bool) error {
	var current Role

	err := tx.QueryRowContext(ctx, `SELECT role_id FROM users WHERE id = ?`, id).Scan(&current)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}

	if err != nil {
		return err
	}

	if role != RoleAdmin {
		if err := keepPasswordAdmin(ctx, tx, id); err != nil {
			return err
		}
	}

	if _, err := tx.ExecContext(ctx, `UPDATE users SET role_id = ?, role_pinned = ? WHERE id = ?`, role, pinned,
		id); err != nil {
		return err
	}

	if role == current {
		return nil
	}

	_, err = tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, id)

	return err
}

func (d *DB) UpdateUserPassword(ctx context.Context, id int64, passwordHash string, keepSession []byte) (err error) {
	ctx, done := d.observe(ctx, "UpdateUserPassword", poolWrite)
	defer done(&err)

	return d.exec(ctx, "update user password", func(tx querier) error {
		return updateUserPassword(ctx, tx, id, passwordHash, keepSession)
	})
}

func updateUserPassword(ctx context.Context, tx querier, id int64, passwordHash string, keepSession []byte) error {
	if passwordHash == "" {
		if err := keepPasswordAdmin(ctx, tx, id); err != nil {
			return err
		}
	}

	res, err := tx.ExecContext(ctx, `UPDATE users SET password_hash = ? WHERE id = ? AND (? OR password_hash IS NOT NULL)`,
		nullableString(passwordHash), id, passwordHash != "")
	if err != nil {
		return err
	}

	n, err := res.RowsAffected()
	if err != nil {
		return err
	}

	if n == 0 {
		return missingPassword(ctx, tx, id)
	}

	_, err = tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ? AND token_hash IS NOT ?`, id, keepSession)

	return err
}

func missingPassword(ctx context.Context, tx querier, id int64) error {
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM users WHERE id = ?)`, id).Scan(&exists); err != nil {
		return err
	}

	if exists {
		return ErrNoPassword
	}

	return ErrNotFound
}

func (d *DB) DeleteUser(ctx context.Context, id int64) (err error) {
	ctx, done := d.observe(ctx, "DeleteUser", poolWrite)
	defer done(&err)

	return d.exec(ctx, "delete user", func(tx querier) error {
		if err := keepPasswordAdmin(ctx, tx, id); err != nil {
			return err
		}

		return updateUser(ctx, tx, `DELETE FROM users WHERE id = ?`, id)
	})
}

func keepPasswordAdmin(ctx context.Context, tx querier, id int64) error {
	var ok bool
	if err := tx.QueryRowContext(ctx, `SELECT
		NOT EXISTS (SELECT 1 FROM users WHERE id = ? AND role_id = ? AND password_hash IS NOT NULL)
		OR EXISTS (SELECT 1 FROM users WHERE id != ? AND role_id = ? AND password_hash IS NOT NULL)`,
		id, RoleAdmin, id, RoleAdmin).Scan(&ok); err != nil {
		return err
	}

	if !ok {
		return ErrLastPasswordAdmin
	}

	return nil
}

const userColumns = `id, email, role_id, role_pinned, password_hash,
	EXISTS (SELECT 1 FROM user_identities WHERE user_identities.user_id = users.id)`

func getUser(row scanner) (User, error) {
	u, err := scanUser(row)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}

	if err != nil {
		return User{}, fmt.Errorf("get user: %w", err)
	}

	return u, nil
}

func scanUser(row scanner) (User, error) {
	var (
		u    User
		hash sql.NullString
	)

	err := row.Scan(&u.ID, &u.Email, &u.Role, &u.RolePinned, &hash, &u.HasIdentity)
	u.PasswordHash = hash.String

	return u, err
}

func updateUser(ctx context.Context, tx querier, query string, args ...any) error {
	res, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}

	return checkAffected(res)
}
