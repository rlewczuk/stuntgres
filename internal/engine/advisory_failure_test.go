package engine

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestAdvisorySessionLifecycleFailuresAndClose(t *testing.T) {
	t.Run("failed initialization and duplicate attachment preserve incumbent", func(t *testing.T) {
		e := New()
		s, ref := advisoryUnitOwner(t, e)
		key := lockResource{kind: advisoryBigintResource, low: 18}
		advisoryUnitGrant(t, s, ref, key, advisoryExclusive)
		injected := errors.New("advisory initialization default failed")
		_, err := e.StartSession(context.Background(), SessionStart{ID: SessionID(testSessionID.Add(1)), Defaults: &defaultProvider{err: injected}})
		if !errors.Is(err, injected) {
			t.Fatal("actual initialization failure", err)
		}
		op := transactionOperation(t, s)
		called := false
		callerOwned := &sessionOwner{fn: func(_ SessionToken, _ EndReason) error { called = true; return nil }}
		transferred, err := op.Attach(AdvisorySession, callerOwned)
		op.Done()
		if err == nil || transferred != (ResourceHandle{}) || advisoryUnitHold(s, key).exclusive != 1 {
			t.Fatal("duplicate attachment changed canonical ownership", transferred, err)
		}
		e.locks.mu.Lock()
		registrations := len(e.locks.sessions)
		canonical := e.locks.sessions[s.token] == s.advisory.ticket
		e.locks.mu.Unlock()
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
		key := lockResource{kind: advisoryPairResource, high: 19, low: 20}
		advisoryUnitGrant(t, a, ra, key, advisoryShared)
		advisoryUnitGrant(t, b, rb, key, advisoryShared)
		transactionEnd(t, b, rb, false)
		source, provenance, done := advisoryUnitScopeWait(t, b, key, advisorySession)
		e.locks.mu.Lock()
		oldManagerEpoch := e.locks.epoch
		e.locks.mu.Unlock()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := e.Reset(ctx); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		if _, err := e.ReplaceCatalog(context.Background(), CatalogRead{}); err == nil {
			t.Fatal("invalid replacement accepted")
		}
		e.locks.mu.Lock()
		entry := e.locks.entries[key]
		pending := entry != nil && len(entry.pending) == 1 && !entry.pending[0].terminal && entry.pending[0].provenance == provenance
		sourceLive := e.locks.valid(source)
		epoch := e.locks.epoch
		e.locks.mu.Unlock()
		if !pending || !sourceLive || epoch != oldManagerEpoch || e.CatalogRead().Epoch() != provenance.Epoch() || advisoryUnitHold(a, key).shared != 1 || advisoryUnitHold(b, key).shared != 1 {
			t.Fatal("failed stage fenced/discarded live state", pending, sourceLive, epoch)
		}
		advisoryUnitUnlock(t, a, key, advisoryShared, true)
		advisoryUnitScopeResult(t, done, nil)
		if h := advisoryUnitHold(b, key); h.shared != 1 || h.exclusive != 1 {
			t.Fatal("unchanged request did not transfer once", h)
		}
	})
	t.Run("physical close after completed transfer cannot resurrect", func(t *testing.T) {
		e := New()
		a, ra := advisoryUnitOwner(t, e)
		b := sessionStart(t, e, nil)
		key := lockResource{kind: advisoryBigintResource, low: 21}
		advisoryUnitGrant(t, a, ra, key, advisoryExclusive)
		_, provenance, done := advisoryUnitScopeWait(t, b, key, advisorySession)
		advisoryUnitUnlock(t, a, key, advisoryExclusive, true)
		advisoryUnitScopeResult(t, done, nil)
		if advisoryUnitHold(b, key).exclusive != 1 {
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
		if advisoryUnitHold(b, key) != (relationHold{}) || advisoryUnitHold(c, key).exclusive != 1 {
			t.Fatal("close leaked/resurrected transfer")
		}
	})
}
