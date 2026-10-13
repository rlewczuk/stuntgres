package engine

import (
	"context"
	"errors"
	"testing"
	"time"
)

type advisoryScopeOutcome struct{ err, finish error }

func advisoryUnitScopeWait(t *testing.T, s *Session, key lockResource) (*relationTicket, TxRef, <-chan advisoryScopeOutcome) {
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
		_, err := scope.acquireAdvisory(op.Context(), key, advisoryExclusive, false)
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
	storage := make(map[ObjectAddress]*relationStorage)
	source.mu.Lock()
	for address, relation := range source.storage {
		storage[address] = &relationStorage{versions: append([]rowVersion(nil), relation.versions...)}
	}
	sequences, err := copySequenceState(source, replacement)
	source.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	e.mu.Lock()
	next, err := e.remapPreparedCatalog(cloneCatalog(replacement.state), sourceRoles, targetRoles, nil)
	if err == nil {
		nextRevision := e.nextRevision
		for _, descriptor := range next.descriptors {
			if err = validateACL(e.roleRead(CatalogRead{e, next}, e.roles), descriptor); err != nil {
				break
			}
			if revision := descriptor.Revision(); revision >= nextRevision {
				nextRevision = revision + 1
			}
		}
		if err == nil {
			err = e.installForeignCatalogLocked(context.Background(), next, storage, sequences, nextRevision)
		}
	}
	epoch := e.state.epoch
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	e.locks.fence(epoch)
}
func TestAdvisorySessionLifecycleReplacement(t *testing.T) {
	for _, grantBeforeFence := range []bool{false, true} {
		t.Run(map[bool]string{false: "pending replacement", true: "grant before fenced readmission"}[grantBeforeFence], func(t *testing.T) {
			e := New()
			a, ra := advisoryUnitOwner(t, e)
			b, rb := advisoryUnitOwner(t, e)
			key := lockResource{kind: advisoryBigintResource, high: 0x80000000, low: 9}
			advisoryUnitGrant(t, a, ra, key, advisoryShared)
			advisoryUnitGrant(t, b, rb, key, advisoryShared)
			transactionEnd(t, b, rb, false)
			ticket, provenance, done := advisoryUnitScopeWait(t, b, key)
			m := e.locks
			m.mu.Lock()
			request := m.entries[key].pending[0]
			_, journaled := ticket.resources[key]
			m.mu.Unlock()
			if request.provenance != provenance || request.ticket != b.advisory.ticket || !journaled {
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
				advisoryUnitUnlock(t, a, key, advisoryShared, true)
				m.mu.Lock()
				granted := request.terminal && request.err == nil
				count := m.entries[key].holders[b.advisory.ticket]
				m.mu.Unlock()
				if !granted || count.shared != 1 || count.exclusive != 1 {
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
			advisoryUnitScopeResult(t, done, ErrCatalogReplaced)
			m.mu.Lock()
			liveSource := m.valid(ticket)
			journal := len(ticket.resources)
			persistentLive := m.valid(b.advisory.ticket)
			m.mu.Unlock()
			count := advisoryUnitHold(b, key)
			wantExclusive := uint64(0)
			if grantBeforeFence {
				wantExclusive = 1
			}
			if liveSource || journal != 0 || !persistentLive || count.shared != 1 || count.exclusive != wantExclusive || b.TransactionState() != TxIdle {
				t.Fatal("fence crossed physical lifetime or lost actual grant", liveSource, journal, persistentLive, count, b.TransactionState())
			}
			c, rc := advisoryUnitOwner(t, e)
			if _, err := advisoryUnitAcquire(t, c, rc, key, advisoryExclusive, true); !errors.Is(err, errRelationUnavailable) {
				t.Fatal("new epoch ignored physical holds", err)
			}
			if err := m.unlockAdvisoryAll(a.advisory.ticket); err != nil {
				t.Fatal(err)
			}
			if err := m.unlockAdvisoryAll(b.advisory.ticket); err != nil {
				t.Fatal(err)
			}
			advisoryUnitGrant(t, c, rc, key, advisoryExclusive)
		})
	}
	t.Run("physical delegate after lease drain and transaction cleanup", func(t *testing.T) {
		e := New()
		a, ra := advisoryUnitOwner(t, e)
		b, rb := advisoryUnitOwner(t, e)
		key := lockResource{kind: advisoryPairResource, high: 10, low: 11}
		advisoryUnitGrant(t, a, ra, key, advisoryShared)
		advisoryUnitGrant(t, b, rb, key, advisoryShared)
		transactionEnd(t, b, rb, false)
		observed := make(chan bool, 1)
		// Inject a cleanup observer in place of the now-installed SQL-name owner.
		if err := b.Detach(b.resources[PreparedSQL].handle, CleanClose); err != nil {
			t.Fatal(err)
		}
		op := transactionOperation(t, b)
		_, err := op.Attach(PreparedSQL, &sessionOwner{fn: func(_ SessionToken, _ EndReason) error {
			observed <- b.TransactionState() == TxIdle && advisoryUnitHold(b, key).shared == 1
			return nil
		}})
		op.Done()
		if err != nil {
			t.Fatal(err)
		}
		ticket, _, done := advisoryUnitScopeWait(t, b, key)
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
		e.locks.mu.Lock()
		persistentLive := e.locks.valid(b.advisory.ticket)
		sourceLive := e.locks.valid(ticket)
		journal := len(b.advisory.ticket.resources)
		e.locks.mu.Unlock()
		if persistentLive || sourceLive || journal != 0 || advisoryUnitHold(b, key) != (relationHold{}) || advisoryUnitHold(a, key).shared != 1 {
			t.Fatal("physical cleanup scope", persistentLive, sourceLive, journal)
		}
		if err = b.advisory.EndSession(b.token, CleanClose); err != nil {
			t.Fatal("idempotent physical cleanup", err)
		}
	})
}
