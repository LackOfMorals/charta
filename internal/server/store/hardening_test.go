package store

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestTouchConcurrentWithReaper(t *testing.T) {
	st := newTestStore(t)
	id, _ := NewTxID()
	tx, err := st.Begin(context.Background(), id, time.Hour, nil)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}

	// Meaningful under `go test -race`: Touch, ExpiresAt and ReapIdle
	// all touch the expiry concurrently.
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				tx.Touch()
				_ = tx.ExpiresAt()
				st.ReapIdle(time.Hour)
			}
		}()
	}
	wg.Wait()
	if _, ok := st.Tx(id); !ok {
		t.Fatal("a live transaction was reaped")
	}
}

func TestRunRestartsIdleClock(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	id, _ := NewTxID()
	tx, err := st.Begin(ctx, id, time.Hour, nil)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	before := tx.ExpiresAt()
	time.Sleep(10 * time.Millisecond)
	if _, err := tx.Run(ctx, "RETURN 1", nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !tx.ExpiresAt().After(before) {
		t.Fatalf("expiry %v not extended past %v by Run", tx.ExpiresAt(), before)
	}
}

func TestReapSkipsTransactionTouchedMeanwhile(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	id, _ := NewTxID()
	tx, err := st.Begin(ctx, id, 100*time.Millisecond, nil)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}

	// Hold the transaction busy past its expiry, as a long query would.
	tx.mu.Lock()
	time.Sleep(150 * time.Millisecond)
	done := make(chan int, 1)
	go func() { done <- st.ReapIdle(time.Hour) }()
	time.Sleep(20 * time.Millisecond) // reaper is now blocked on tx.mu
	tx.Touch()                        // the "query" finished and touched it
	tx.mu.Unlock()

	if n := <-done; n != 0 {
		t.Fatalf("ReapIdle = %d, want 0: transaction was touched while the reaper waited", n)
	}
	if _, ok := st.Tx(id); !ok {
		t.Fatal("transaction was reaped despite being touched")
	}
}

func TestCloseDoesNotDeadlockWithCommit(t *testing.T) {
	st, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := context.Background()
	// graingraph serialises on one connection, so only one transaction can be
	// open at a time; race Commit against Close repeatedly instead.
	id, _ := NewTxID()
	if _, err := st.Begin(ctx, id, time.Hour, nil); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	ids := []string{id}

	done := make(chan struct{})
	go func() {
		defer close(done)
		var wg sync.WaitGroup
		for _, id := range ids {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, _ = st.Commit(id) // may fail once Close has run
			}()
		}
		_ = st.Close()
		wg.Wait()
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Close and Commit deadlocked")
	}
}

func TestNewTxID(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 1000; i++ {
		id, err := NewTxID()
		if err != nil {
			t.Fatalf("NewTxID: %v", err)
		}
		if len(id) != txIDLength {
			t.Fatalf("id %q has length %d, want %d", id, len(id), txIDLength)
		}
		for _, c := range id {
			if !strings.ContainsRune(txIDAlphabet, c) {
				t.Fatalf("id %q contains %q outside the alphabet", id, c)
			}
		}
		if seen[id] {
			t.Fatalf("duplicate id %q", id)
		}
		seen[id] = true
	}
}
