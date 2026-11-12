package db

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func openTestDB(t *testing.T) *DB {
	t.Helper()

	d, err := Open(context.Background(), filepath.Join(t.TempDir(), "mocn.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	t.Cleanup(func() { _ = d.Close() })

	return d
}

func TestOpenReachesLatestVersion(t *testing.T) {
	d := openTestDB(t)

	var version int
	if err := d.writer.QueryRowContext(context.Background(), "PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}

	if version != len(migrations) {
		t.Fatalf("user_version = %d, want %d", version, len(migrations))
	}
}

func TestOpenIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mocn.db")

	for range 2 {
		d, err := Open(context.Background(), path)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}

		if err := d.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestOpenRejectsNewerSchema(t *testing.T) {
	ctx := context.Background()
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
	d := openTestDB(t)

	createTestUser(t, d, "a@example.com", RoleAdmin)

	if _, err := d.reader().ExecContext(t.Context(), `DELETE FROM users`); err == nil {
		t.Fatal("the readers wrote")
	}
}

func TestDatabaseIsPrivate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mocn.db")

	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	d, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = d.Close() })

	if _, err := d.CreateUser(context.Background(), NewUser{Email: "a@example.com", Role: RoleAdmin}, 100); err != nil {
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

func TestAppliedIndexSurvivesReopening(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "mocn.db")

	d, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := d.CreateUser(ctx, NewUser{Email: "a@example.com", Role: RoleAdmin}, 100); err != nil {
		t.Fatal(err)
	}

	applied := d.AppliedIndex()

	if err := d.Close(); err != nil {
		t.Fatal(err)
	}

	if applied == 0 {
		t.Fatal("AppliedIndex = 0 after a write")
	}

	d, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = d.Close() })

	if got := d.AppliedIndex(); got != applied {
		t.Fatalf("AppliedIndex after reopening = %d, want %d", got, applied)
	}
}
