package db

import (
	"os"
	"path/filepath"
	"testing"
)

func openTestDB(t *testing.T) *DB {
	t.Helper()

	d, err := Open(t.Context(), filepath.Join(t.TempDir(), "mocn.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	t.Cleanup(func() { _ = d.Close() })

	return d
}

func TestOpenReachesLatestVersion(t *testing.T) {
	t.Parallel()

	d := openTestDB(t)

	var version int
	if err := d.writer.QueryRowContext(t.Context(), "PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}

	if version != len(migrations) {
		t.Fatalf("user_version = %d, want %d", version, len(migrations))
	}
}

func TestOpenIsIdempotent(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "mocn.db")

	for range 2 {
		d, err := Open(t.Context(), path)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}

		if err := d.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestOpenRejectsNewerSchema(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "mocn.db")

	d, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	if _, err := d.writer.ExecContext(ctx, "PRAGMA user_version = 99"); err != nil {
		t.Fatal(err)
	}

	_ = d.Close()

	if d, err := Open(ctx, path); err == nil {
		_ = d.Close()

		t.Fatal("Open succeeded on a newer schema")
	}
}

func TestReadersDoNotWrite(t *testing.T) {
	t.Parallel()

	d := openTestDB(t)

	createTestUser(t, d, "a@example.com", RoleAdmin)

	if _, err := d.reader().ExecContext(t.Context(), `DELETE FROM users`); err == nil {
		t.Fatal("the readers wrote")
	}
}

func TestDatabaseIsPrivate(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "mocn.db")

	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	d, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = d.Close() })

	if _, err := d.CreateUser(t.Context(), NewUser{Email: "a@example.com", Role: RoleAdmin}, 100); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"mocn.db", "mocn.db-wal", "mocn.db-shm"} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}

		if mode := info.Mode().Perm(); mode != 0o600 {
			t.Errorf("%s mode = %o, want 600", name, mode)
		}
	}
}

func TestDatabaseOfAnotherUserKeepsItsMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mocn.db")

	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := os.Chmod(path, 0o660); err != nil {
		t.Fatal(err)
	}

	self := euid
	euid = func() int { return os.Geteuid() + 1 }

	t.Cleanup(func() { euid = self })

	d, err := Open(t.Context(), path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	t.Cleanup(func() { _ = d.Close() })

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	if mode := info.Mode().Perm(); mode != 0o660 {
		t.Errorf("mode = %o, want 660", mode)
	}
}
