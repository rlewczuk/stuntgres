package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/session"
)

func TestDatabaseDropSubmittedAdmission(t *testing.T) {
	e, target, admin := databaseDropOwnerFixture(t)
	enginetest.SessionSQL(t, admin, "CREATE ROLE drop_submitted_owner LOGIN")
	enginetest.SessionSQL(t, admin, "CREATE ROLE drop_submitted_other LOGIN")
	enginetest.SessionSQL(t, admin, "ALTER DATABASE drop_owner_target OWNER TO drop_submitted_owner")
	principal := e.Roles().LookupRef("drop_submitted_owner")
	issuer, err := enginetest.OpenSession(t.Context(), e, session.SessionStart{ID: enginetest.NextSessionID()}, principal)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		issuer.RequestEnd(session.CleanClose)
		if err := issuer.WaitEnd(context.Background()); err != nil {
			t.Error(err)
		}
	}()
	id := enginetest.NextSessionID()
	a, err := e.AdmitDatabase(t.Context(), principal, id, core.DatabaseSelector{Name: target.DatabaseName, Explicit: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		a.Release()
		state, _, owner, _, _, _ := core.DatabaseAdmissionOwnership(a)
		if state == 1 && owner == nil {
			// Release deliberately cannot abandon a consumed token. Resume the
			// real initializer with an already-canceled context so its canonical
			// pre-bind failure path releases the ticket without running defaults.
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if s, _ := core.EngineStartClaimedSession(target, ctx, a, session.SessionStart{ID: id}); s != nil {
				s.RequestEnd(session.CleanClose)
				_ = s.WaitEnd(context.Background())
			}
		}
	}()
	defaults := &databaseDropDefaults{entered: make(chan struct{}), canceled: make(chan struct{}), release: make(chan struct{})}
	close(defaults.release) // An erroneous late callback must fail assertions, not hang.
	start := session.SessionStart{ID: id, Defaults: defaults}
	copied := *a
	for _, invalid := range []struct {
		a     *core.DatabaseAdmission
		e     *core.Engine
		start session.SessionStart
	}{
		{&copied, target, start},
		{a, e, start},
		{a, target, session.SessionStart{ID: enginetest.NextSessionID()}},
	} {
		if err := core.DatabaseAdmissionClaim(invalid.a, invalid.e, invalid.start); err == nil {
			t.Fatal("copied/foreign identity consumed genuine admission")
		}
	}
	if err := core.DatabaseAdmissionClaim(a, target, start); err != nil {
		t.Fatal(err)
	}
	// This is the actual StartSession handoff: claim has consumed the genuine
	// token, but startSession has not yet allocated or bound an owner. A defaults
	// callback would be too late to distinguish this state.
	state, revoked, owner, ticket, databaseCount, principalCount := core.DatabaseAdmissionOwnership(a)
	if state != 1 || revoked || owner != nil || ticket == nil || ticket.OwnershipEnded() || databaseCount != 1 || principalCount != 2 || defaults.touched.Load() {
		t.Fatal("fixture did not stop at consumed-before-binding ownership", state, revoked, owner, ticket, databaseCount, principalCount)
	}
	a.Release() // Caller abandonment cannot release a submitted ticket.
	if err := core.DatabaseAdmissionClaim(a, target, start); err == nil {
		t.Fatal("submitted admission was reusable")
	}
	other, err := e.AdmitDatabase(t.Context(), e.Roles().LookupRef("drop_submitted_other"), enginetest.NextSessionID(), core.DatabaseSelector{Name: target.DatabaseName, Explicit: true})
	if err != nil {
		t.Fatal(err)
	}
	defer other.Release()
	_, err = databaseDropOwnerExecute(t.Context(), e, issuer, databaseDropOwnerStatement(true))
	enginetest.State(t, err, "42501")
	state, revoked, owner, retained, databaseCount, principalCount := core.DatabaseAdmissionOwnership(a)
	if state != 1 || revoked || owner != nil || retained != ticket || ticket.OwnershipEnded() || databaseCount != 2 || principalCount != 2 {
		t.Fatal("failed all-principal preflight changed consumed ownerless authority")
	}
	other.Release()
	if _, err := databaseDropOwnerExecute(t.Context(), e, issuer, databaseDropOwnerStatement(true)); err != nil {
		t.Fatal(err)
	}
	state, revoked, owner, retained, databaseCount, principalCount = core.DatabaseAdmissionOwnership(a)
	if state != 2 || !revoked || owner != nil || retained != nil || !ticket.OwnershipEnded() || databaseCount != 0 || principalCount != 1 {
		t.Fatal("FORCE did not revoke/drain consumed token exactly once", state, revoked, owner, retained, databaseCount, principalCount)
	}
	// Resume the exact production continuation, not StartSession's claim again.
	// A second claim alone would mask a broken owner-binding handoff.
	s, err := core.EngineStartClaimedSession(target, t.Context(), a, start)
	if s != nil {
		s.RequestEnd(session.CleanClose)
		_ = s.WaitEnd(context.Background())
		t.Fatal("late consumed continuation published an owner")
	}
	enginetest.State(t, err, "3D000")
	for range 2 {
		a.Release()
		copied.Release()
		if s, err := target.StartSession(t.Context(), a, start); s != nil || err == nil {
			if s != nil {
				s.RequestEnd(session.CleanClose)
				_ = s.WaitEnd(context.Background())
			}
			t.Fatal("revoked token regained authority", s, err)
		}
	}
	state, revoked, owner, retained, databaseCount, principalCount = core.DatabaseAdmissionOwnership(a)
	if defaults.touched.Load() || state != 2 || !revoked || owner != nil || retained != nil || databaseCount != 0 || principalCount != 1 || !ticket.OwnershipEnded() {
		t.Fatal("late continuation double-released or ran defaults")
	}
	core.EngineSessionMuRef(target).Lock()
	published := len(core.EngineSessions(target))
	core.EngineSessionMuRef(target).Unlock()
	if published != 0 || len(core.EnginePrivateStarts(target)) != 0 {
		t.Fatal("late continuation left lifecycle resources")
	}
	if op, err := issuer.Acquire(t.Context()); err != nil {
		t.Fatal("peer issuer lost independent ownership", err)
	} else {
		op.Done()
	}
}
