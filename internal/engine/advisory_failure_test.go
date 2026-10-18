package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"stuntgres/internal/engine/ids"
)

func TestAdvisorySessionLifecycleFailuresAndClose(t *testing.T) {
	t.Run("failed initialization and duplicate attachment preserve incumbent", func(t *testing.T) {
		e := New()
		s, ref := advisoryUnitOwner(t, e)
		key := lockResource{Kind: advisoryBigintResource, Low: 18}
		advisoryUnitGrant(t, s, ref, key, advisoryExclusive)
		injected := errors.New("advisory initialization default failed")
		_, err := e.StartSession(context.Background(), SessionStart{ID: ids.SessionID(testSessionID.Add(1)), Defaults: &defaultProvider{err: injected}})
		if !errors.Is(err, injected) {
			t.Fatal("actual initialization failure", err)
		}
		op := transactionOperation(t, s)
		called := false
		callerOwned := &sessionOwner{fn: func(_ ids.SessionToken, _ EndReason) error { called = true; return nil }}
		transferred, err := op.Attach(AdvisorySession, callerOwned)
		op.Done()
		if err == nil || transferred != (ResourceHandle{}) || advisoryUnitHold(s, key).Exclusive != 1 {
			t.Fatal("duplicate attachment changed canonical ownership", transferred, err)
		}
		lockState := e.locks.Inspect()
		registrations := len(lockState.Sessions)
		canonical := lockState.Sessions[s.token] == s.advisory.ticket
		if registrations != 1 || !canonical {
			t.Fatal("failed initialization leaked/replaced owner", registrations, canonical)
		}
		s.RequestEnd(CleanClose)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err = s.WaitEnd(ctx); err != nil {
			t.Fatal(err)
		}
		if called {
			t.Fatal("failed attachment transferred caller resource")
		}
	})
	t.Run("failed replacement leaves exact pending request and counts", func(t *testing.T) {
		e := New()
		a, ra := advisoryUnitOwner(t, e)
		b, rb := advisoryUnitOwner(t, e)
		key := lockResource{Kind: advisoryPairResource, High: 19, Low: 20}
		advisoryUnitGrant(t, a, ra, key, advisoryShared)
		advisoryUnitGrant(t, b, rb, key, advisoryShared)
		transactionEnd(t, b, rb, false)
		source, provenance, done := advisoryUnitScopeWait(t, b, key, advisorySession)
		lockState := e.locks.Inspect()
		oldManagerEpoch := lockState.Epoch
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := e.Reset(ctx); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		if _, err := e.ReplaceCatalog(context.Background(), nil, CatalogRead{}); err == nil {
			t.Fatal("invalid replacement accepted")
		}
		lockState = e.locks.Inspect()
		entry := lockState.Resources[key]
		pending := entry != nil && len(entry.Pending) == 1 && !entry.Pending[0].Terminal && entry.Pending[0].Provenance == provenance
		sourceLive := lockState.Valid(source)
		epoch := lockState.Epoch
		if !pending || !sourceLive || epoch != oldManagerEpoch || e.CatalogRead().Epoch() != provenance.Epoch() || advisoryUnitHold(a, key).Shared != 1 || advisoryUnitHold(b, key).Shared != 1 {
			t.Fatal("failed stage fenced/discarded live state", pending, sourceLive, epoch)
		}
		advisoryUnitUnlock(t, a, key, advisoryShared, true)
		advisoryUnitScopeResult(t, done, nil)
		if h := advisoryUnitHold(b, key); h.Shared != 1 || h.Exclusive != 1 {
			t.Fatal("unchanged request did not transfer once", h)
		}
	})
	t.Run("physical close after completed transfer cannot resurrect", func(t *testing.T) {
		e := New()
		a, ra := advisoryUnitOwner(t, e)
		b := sessionStart(t, e, nil)
		key := lockResource{Kind: advisoryBigintResource, Low: 21}
		advisoryUnitGrant(t, a, ra, key, advisoryExclusive)
		_, provenance, done := advisoryUnitScopeWait(t, b, key, advisorySession)
		advisoryUnitUnlock(t, a, key, advisoryExclusive, true)
		advisoryUnitScopeResult(t, done, nil)
		if advisoryUnitHold(b, key).Exclusive != 1 {
			t.Fatal("completed transfer absent")
		}
		b.RequestEnd(CleanClose)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := b.WaitEnd(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := advisoryUnitAcquire(t, b, provenance, key, advisoryExclusive, true); !errors.Is(err, ErrCatalogReplaced) {
			t.Fatal("retired physical owner reacquired", err)
		}
		c, rc := advisoryUnitOwner(t, e)
		advisoryUnitGrant(t, c, rc, key, advisoryExclusive)
		if advisoryUnitHold(b, key) != (relationHold{}) || advisoryUnitHold(c, key).Exclusive != 1 {
			t.Fatal("close leaked/resurrected transfer")
		}
	})
}
