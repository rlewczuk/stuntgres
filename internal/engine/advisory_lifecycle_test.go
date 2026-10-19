package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
)

type advisoryScopeOutcome struct{ err, finish error }

func advisoryUnitScopeWait(t *testing.T, s *Session, key lock.Resource, lifetime advisoryLifetime) (*lock.Ticket, ids.TxRef, <-chan advisoryScopeOutcome) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	op, err := s.Acquire(ctx)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	scope, err := s.Engine().StartStatement(op)
	if err != nil {
		op.Done()
		cancel()
		t.Fatal(err)
	}
	if err = scope.gate(statement(t, "SELECT pg_advisory_lock(1)")); err != nil {
		_ = scope.Finish(err)
		op.Done()
		cancel()
		t.Fatal(err)
	}
	ticket, provenance := scope.ticket, scope.txRef
	done := make(chan advisoryScopeOutcome, 1)
	joined := make(chan struct{})
	go func() {
		_, err := scope.acquireAdvisory(op.Context(), key, lock.AdvisoryExclusive, lifetime, false)
		finish := scope.Finish(err)
		op.Done()
		done <- advisoryScopeOutcome{err, finish}
		close(joined)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-joined:
		case <-time.After(5 * time.Second):
			t.Error("advisory scope worker failed to join")
		}
	})
	lockPending(t, s.Engine(), 1)
	return ticket, provenance, done
}
func advisoryUnitScopeResult(t *testing.T, done <-chan advisoryScopeOutcome, want error) {
	t.Helper()
	select {
	case got := <-done:
		if !errors.Is(got.err, want) || !errors.Is(got.finish, want) {
			t.Fatal("scope terminal/Finish identity", got, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("scope outcome missing")
	}
}

// The caller owns replacement admission, just like the granted-readmission
// fixture for DELETE. Use the real foreign-generation publication boundary.
func advisoryUnitReplaceReserved(t *testing.T, e *Engine) {
	t.Helper()
	source := New()
	replacement := source.CatalogRead()
	sourceRoles, targetRoles := source.Roles(), e.Roles()
	storage := make(map[ids.ObjectAddress]*relationStorage)
	source.mu.Lock()
	for address, relation := range source.storage.relations {
		storage[address] = &relationStorage{versions: append([]rowVersion(nil), relation.versions...)}
	}
	sequences, err := copySequenceState(source, replacement)
	indexes, indexErr := source.captureIndexSnapshot(context.Background(), replacement, source.storage.relations, source.storage.indexRoots, source.commitSeq)
	source.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if indexErr != nil {
		t.Fatal(indexErr)
	}
	e.mu.Lock()
	next, err := e.remapPreparedCatalog(catalog.CloneState(replacement.State()), sourceRoles, targetRoles, nil)
	if err == nil {
		nextRevision := e.catalog.nextRevision
		for _, descriptor := range next.Descriptors() {
			if err = validateACL(e.roleRead(e.catalog.read(next), e.roles), descriptor); err != nil {
				break
			}
			if revision := descriptor.Revision(); revision >= nextRevision {
				nextRevision = revision + 1
			}
		}
		if err == nil {
			var prepared remintedIndexStorage
			prepared, err = e.prepareForeignIndexStorage(context.Background(), next, storage, indexes)
			if err == nil {
				err = e.installForeignCatalogLocked(context.Background(), next, storage, sequences, nextRevision, prepared)
			}
		}
	}
	epoch := e.catalog.state.Epoch()
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	e.locks.Fence(epoch)
}
func TestAdvisorySessionLifecycleReplacement(t *testing.T) {
	for _, grantBeforeFence := range []bool{false, true} {
		t.Run(map[bool]string{false: "pending replacement", true: "grant before fenced readmission"}[grantBeforeFence], func(t *testing.T) {
			e := New()
			a, ra := advisoryUnitOwner(t, e)
			b, rb := advisoryUnitOwner(t, e)
			key := lock.Resource{Kind: lock.AdvisoryBigintResource, High: 0x80000000, Low: 9}
			advisoryUnitGrant(t, a, ra, key, lock.AdvisoryShare)
			advisoryUnitGrant(t, b, rb, key, lock.AdvisoryShare)
			transactionEnd(t, b, rb, false)
			ticket, provenance, done := advisoryUnitScopeWait(t, b, key, advisorySession)
			m := e.locks
			lockState := m.Inspect(ticket)
			waiter := lockState.Resources[key].Pending[0]
			request := waiter.Request
			_, journaled := lockState.Journals[ticket][key]
			if waiter.Provenance != provenance || waiter.Ticket != b.advisory.ticket || !journaled {
				t.Fatal("persistent request lost current transient provenance")
			}
			if grantBeforeFence {
				e.replacementMu.Lock()
				held := true
				t.Cleanup(func() {
					if held {
						e.replacementMu.Unlock()
					}
				})
				advisoryUnitUnlock(t, a, key, lock.AdvisoryShare, true)
				granted := request.Terminal() && request.Err() == nil
				count := m.Inspect().Resources[key].Holders[b.advisory.ticket]
				if !granted || count.Shared != 1 || count.Exclusive != 1 {
					t.Fatal("actual transfer before readmission", granted, count)
				}
				select {
				case outcome := <-done:
					t.Fatal("scope crossed reserved readmission", outcome)
				default:
				}
				advisoryUnitReplaceReserved(t, e)
				e.replacementMu.Unlock()
				held = false
			} else {
				if err := e.Reset(context.Background()); err != nil {
					t.Fatal(err)
				}
			}
			advisoryUnitScopeResult(t, done, ids.ErrCatalogReplaced)
			lockState = m.Inspect(ticket)
			liveSource := lockState.Valid(ticket)
			journal := len(lockState.Journals[ticket])
			persistentLive := lockState.Valid(b.advisory.ticket)
			count := advisoryUnitHold(b, key)
			wantExclusive := uint64(0)
			if grantBeforeFence {
				wantExclusive = 1
			}
			if liveSource || journal != 0 || !persistentLive || count.Shared != 1 || count.Exclusive != wantExclusive || b.TransactionState() != TxIdle {
				t.Fatal("fence crossed physical lifetime or lost actual grant", liveSource, journal, persistentLive, count, b.TransactionState())
			}
			c, rc := advisoryUnitOwner(t, e)
			if _, err := advisoryUnitAcquire(t, c, rc, key, lock.AdvisoryExclusive, true); !errors.Is(err, lock.ErrUnavailable) {
				t.Fatal("new epoch ignored physical holds", err)
			}
			if err := m.UnlockAdvisoryAll(a.advisory.ticket); err != nil {
				t.Fatal(err)
			}
			if err := m.UnlockAdvisoryAll(b.advisory.ticket); err != nil {
				t.Fatal(err)
			}
			advisoryUnitGrant(t, c, rc, key, lock.AdvisoryExclusive)
		})
	}
	t.Run("physical delegate after lease drain and transaction cleanup", func(t *testing.T) {
		e := New()
		a, ra := advisoryUnitOwner(t, e)
		b, rb := advisoryUnitOwner(t, e)
		key := lock.Resource{Kind: lock.AdvisoryPairResource, High: 10, Low: 11}
		advisoryUnitGrant(t, a, ra, key, lock.AdvisoryShare)
		advisoryUnitGrant(t, b, rb, key, lock.AdvisoryShare)
		transactionEnd(t, b, rb, false)
		observed := make(chan bool, 1)
		// Inject a cleanup observer in place of the now-installed SQL-name owner.
		if err := b.Detach(b.resources[PreparedSQL].handle, CleanClose); err != nil {
			t.Fatal(err)
		}
		op := transactionOperation(t, b)
		_, err := op.Attach(PreparedSQL, &sessionOwner{fn: func(_ ids.SessionToken, _ EndReason) error {
			observed <- b.TransactionState() == TxIdle && advisoryUnitHold(b, key).Shared == 1
			return nil
		}})
		op.Done()
		if err != nil {
			t.Fatal(err)
		}
		ticket, _, done := advisoryUnitScopeWait(t, b, key, advisorySession)
		b.RequestEnd(CleanClose)
		advisoryUnitScopeResult(t, done, context.Canceled)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err = b.WaitEnd(ctx); err != nil {
			t.Fatal(err)
		}
		select {
		case validOrder := <-observed:
			if !validOrder {
				t.Fatal("delegate ordering lost transaction cleanup/prior count")
			}
		default:
			t.Fatal("prepared resource cleanup callback missing")
		}
		lockState := e.locks.Inspect(b.advisory.ticket)
		persistentLive := lockState.Valid(b.advisory.ticket)
		sourceLive := lockState.Valid(ticket)
		journal := len(lockState.Journals[b.advisory.ticket])
		if persistentLive || sourceLive || journal != 0 || advisoryUnitHold(b, key) != (lock.Hold{}) || advisoryUnitHold(a, key).Shared != 1 {
			t.Fatal("physical cleanup scope", persistentLive, sourceLive, journal)
		}
		if err = b.advisory.EndSession(b.token, CleanClose); err != nil {
			t.Fatal("idempotent physical cleanup", err)
		}
	})
}
