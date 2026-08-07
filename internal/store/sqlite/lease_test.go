package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/zoyluo/cronova/internal/store"
)

func newLeaseStore(t *testing.T) *Store {
	t.Helper()
	st, err := New(filepath.Join(t.TempDir(), "lease.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return st
}

func TestLeaseLifecycle(t *testing.T) {
	st := newLeaseStore(t)
	ctx := context.Background()

	// fresh DB: first holder acquires
	if err := st.AcquireLease(ctx, "a", time.Minute); err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	// re-acquire by the same holder is fine (idempotent restart of the same process)
	if err := st.AcquireLease(ctx, "a", time.Minute); err != nil {
		t.Fatalf("same-holder re-acquire: %v", err)
	}
	// a second live holder is refused with ErrLeaseHeld
	err := st.AcquireLease(ctx, "b", time.Minute)
	if !errors.Is(err, store.ErrLeaseHeld) {
		t.Fatalf("second acquire = %v, want ErrLeaseHeld", err)
	}
	// renew by the owner succeeds; by the loser it reports lost
	if err := st.RenewLease(ctx, "a", time.Minute); err != nil {
		t.Fatalf("owner renew: %v", err)
	}
	if err := st.RenewLease(ctx, "b", time.Minute); !errors.Is(err, store.ErrLeaseLost) {
		t.Fatalf("non-owner renew = %v, want ErrLeaseLost", err)
	}
	// release then a new holder acquires cleanly
	if err := st.ReleaseLease(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	if err := st.AcquireLease(ctx, "b", time.Minute); err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
}

func TestLeaseExpiredTakeover(t *testing.T) {
	st := newLeaseStore(t)
	ctx := context.Background()

	// a crashed holder leaves an expired lease behind — takeover must succeed
	if err := st.AcquireLease(ctx, "dead", -time.Second); err != nil {
		t.Fatalf("seed expired lease: %v", err)
	}
	if err := st.AcquireLease(ctx, "alive", time.Minute); err != nil {
		t.Fatalf("takeover of expired lease: %v", err)
	}
	// the dead holder's renew now reports lost
	if err := st.RenewLease(ctx, "dead", time.Minute); !errors.Is(err, store.ErrLeaseLost) {
		t.Fatalf("dead renew = %v, want ErrLeaseLost", err)
	}
}
