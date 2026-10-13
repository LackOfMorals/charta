package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	st, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestRun(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	out, err := st.Run(ctx, "CREATE (n:Person {name: 'A'}) RETURN n.name AS name", nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(out.Fields) != 1 || out.Fields[0] != "name" {
		t.Fatalf("fields = %v", out.Fields)
	}
	if len(out.Values) != 1 || out.Values[0][0] != "A" {
		t.Fatalf("values = %v", out.Values)
	}
	if out.Counters == nil || out.Counters.NodesCreated != 1 {
		t.Fatalf("counters = %+v", out.Counters)
	}
}

func TestTransactionCommitAndRollback(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	id1, _ := NewTxID()
	tx, err := st.Begin(ctx, id1, time.Minute, nil)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if _, err := tx.Run(ctx, "CREATE (n:Person {name: 'InTx'})", nil); err != nil {
		t.Fatalf("tx.Run: %v", err)
	}
	if bm, err := st.Commit(id1); err != nil || !strings.HasPrefix(bm, "FB:") {
		t.Fatalf("Commit: %v %q", err, bm)
	}
	if _, ok := st.Tx(id1); ok {
		t.Fatal("tx still registered after commit")
	}

	// Uncommitted transaction rolls back.
	id2, _ := NewTxID()
	tx, err = st.Begin(ctx, id2, time.Minute, nil)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if _, err := tx.Run(ctx, "CREATE (n:Person {name: 'Doomed'})", nil); err != nil {
		t.Fatalf("tx.Run: %v", err)
	}
	if err := st.Rollback(id2); err != nil {
		t.Fatalf("Rollback: %v", err)
	}

	out, err := st.Run(ctx, "MATCH (n:Person) RETURN n.name AS name", nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(out.Values) != 1 || out.Values[0][0] != "InTx" {
		t.Fatalf("values = %v (want only InTx)", out.Values)
	}
}

func TestTransactionErrors(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	if _, err := st.Commit("nope"); !errors.Is(err, ErrTransactionNotFound) {
		t.Fatalf("Commit(unknown) = %v, want ErrTransactionNotFound", err)
	}
	if err := st.Rollback("nope"); !errors.Is(err, ErrTransactionNotFound) {
		t.Fatalf("Rollback(unknown) = %v, want ErrTransactionNotFound", err)
	}

	// Running after commit is an error.
	id, _ := NewTxID()
	tx, _ := st.Begin(ctx, id, time.Minute, nil)
	if _, err := tx.Run(ctx, "RETURN 1", nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if _, err := st.Commit(id); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if _, err := tx.Run(ctx, "RETURN 1", nil); err == nil {
		t.Fatal("Run after commit succeeded, want error")
	}
}

func TestReapIdle(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	id, _ := NewTxID()
	tx, err := st.Begin(ctx, id, 20*time.Millisecond, nil)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	time.Sleep(40 * time.Millisecond)
	if n := st.ReapIdle(time.Hour); n != 1 {
		t.Fatalf("ReapIdle = %d, want 1", n)
	}
	if _, ok := st.Tx(id); ok {
		t.Fatal("idle tx still registered")
	}
	_ = tx

	// A touched transaction survives.
	id2, _ := NewTxID()
	tx2, _ := st.Begin(ctx, id2, 20*time.Millisecond, nil)
	time.Sleep(30 * time.Millisecond)
	tx2.Touch()
	if n := st.ReapIdle(time.Hour); n != 0 {
		t.Fatalf("ReapIdle = %d, want 0 (tx was touched)", n)
	}
}

func TestBookmarkUniqueness(t *testing.T) {
	st := newTestStore(t)
	seen := map[string]bool{}
	for i := 0; i < 1000; i++ {
		b := st.NewBookmark()
		if !strings.HasPrefix(b, "FB:") {
			t.Fatalf("bookmark %q lacks FB: prefix", b)
		}
		if seen[b] {
			t.Fatalf("duplicate bookmark %q", b)
		}
		seen[b] = true
	}
}
