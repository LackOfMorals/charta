package store

import (
	"context"
	"path/filepath"
	"testing"
)

func openFile(t *testing.T, readConns int) *SQLiteStore {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "g.db"), Config{ReadConns: readConns})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestReadPoolCreatedOnlyForFiles(t *testing.T) {
	if !openFile(t, 3).HasReadPool() {
		t.Error("a file database with ReadConns > 0 should have a read pool")
	}
	if openFile(t, 0).HasReadPool() {
		t.Error("ReadConns = 0 disables the pool")
	}
	mem, err := Open(":memory:", Config{ReadConns: 3})
	if err != nil {
		t.Fatal(err)
	}
	defer mem.Close()
	if mem.HasReadPool() {
		t.Error("an in-memory database must not get a read pool")
	}
}

func TestReadTxCannotWrite(t *testing.T) {
	ctx := context.Background()
	s := openFile(t, 2)
	if _, err := s.InsertNode(ctx, Labels{"A"}, `{}`); err != nil {
		t.Fatal(err)
	}
	tx, err := s.BeginReadTx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM nodes`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("read through the pool: %d %v", n, err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO nodes (labels, props) VALUES ('B', '{}')`); err == nil {
		t.Error("a write on the read pool must fail")
	}
}

func TestReadTxSeesOneSnapshot(t *testing.T) {
	ctx := context.Background()
	s := openFile(t, 2)
	if _, err := s.InsertNode(ctx, Labels{"A"}, `{}`); err != nil {
		t.Fatal(err)
	}
	tx, err := s.BeginReadTx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	count := func() int {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM nodes`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	before := count()
	// A concurrent writer commits while the read transaction is open.
	if _, err := s.InsertNode(ctx, Labels{"B"}, `{}`); err != nil {
		t.Fatal(err)
	}
	if after := count(); after != before {
		t.Errorf("snapshot changed inside a read transaction: %d -> %d", before, after)
	}
}

func TestReadTxFallsBackWithoutPool(t *testing.T) {
	ctx := context.Background()
	mem, _ := Open(":memory:", Config{})
	defer mem.Close()
	tx, err := mem.BeginReadTx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
}
