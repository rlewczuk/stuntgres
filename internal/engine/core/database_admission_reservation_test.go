package core_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
)

func databaseAdmissionLimit(e *core.Engine, limit int32) {
	e.Mu.Lock()
	defer e.Mu.Unlock()
	next := catalog.CloneState(e.Catalog.State)
	db := *next.MustDatabase(e.Catalog.DatabaseAddress())
	db.ObjectRevision++
	db.AllowConnections, db.ConnectionLimit = true, limit
	next.SetDescriptor(db.Address(), &db)
	e.Catalog.PublishLocked(next)
}

func databaseAdmissionReserve(t *testing.T, e *core.Engine, principal authorization.PrincipalRef) *core.DatabaseAdmission {
	t.Helper()
	a, err := e.AdmitDatabase(t.Context(), principal, core.DatabaseSelector{Name: e.DatabaseName, Explicit: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Release)
	return a
}

func databaseAdmissionCount(t *testing.T, e *core.Engine, want int) {
	t.Helper()
	if got := core.EngineDatabaseAdmissionCount(e); got != want {
		t.Fatalf("physical reservations %d, want %d", got, want)
	}
}

func databaseAdmissionFull(t *testing.T, e *core.Engine, principal authorization.PrincipalRef) {
	t.Helper()
	a, err := e.AdmitDatabase(t.Context(), principal, core.DatabaseSelector{})
	if a != nil {
		a.Release()
		t.Fatal("full database admitted a reservation")
	}
	want := &sqlstate.Error{Severity: "FATAL", SeverityUnlocalized: "FATAL", Code: "53300", Message: fmt.Sprintf(`too many connections for database "%s"`, e.DatabaseName)}
	var got *sqlstate.Error
	if !errors.As(err, &got) || *got != *want {
		t.Fatalf("database limit: %#v, want %#v", err, want)
	}
}

func TestDatabaseAdmissionReservation(t *testing.T) {
	t.Run("reserved and consumed release", func(t *testing.T) {
		e, login := databaseAdmissionFixture()
		databaseAdmissionLimit(e, 1)
		a := databaseAdmissionReserve(t, e, login)
		databaseAdmissionCount(t, e, 1)
		databaseAdmissionFull(t, e, login)
		a.Release()
		a.Release()
		databaseAdmissionCount(t, e, 0)
		a = databaseAdmissionReserve(t, e, login)
		s, err := e.StartSession(t.Context(), a, session.SessionStart{ID: ids.SessionID(testSessionID.Add(1))})
		if err != nil || s.CountedAuthenticated != login || core.EngineOf(s) != e {
			t.Fatal("admission did not retain identity/target", s, err)
		}
		defer roleLimitClose(t, s)
		a.Release()
		databaseAdmissionCount(t, e, 1)
		databaseAdmissionFull(t, e, login)
		roleLimitClose(t, s)
		databaseAdmissionCount(t, e, 0)
	})
	t.Run("initializing reservation", func(t *testing.T) {
		e, login := databaseAdmissionFixture()
		databaseAdmissionLimit(e, 1)
		a := databaseAdmissionReserve(t, e, login)
		gate := &roleLimitGate{entered: make(chan struct{}), release: make(chan struct{})}
		var once sync.Once
		unblock := func() { once.Do(func() { close(gate.release) }) }
		result := make(chan roleLimitStartResult, 1)
		go func() {
			s, err := e.StartSession(t.Context(), a, session.SessionStart{ID: ids.SessionID(testSessionID.Add(1)), Defaults: gate})
			result <- roleLimitStartResult{s, err}
		}()
		joined := false
		defer func() {
			unblock()
			if !joined {
				r := roleLimitReceive(t, result)
				if r.s != nil {
					roleLimitClose(t, r.s)
				}
			}
		}()
		select {
		case <-gate.entered:
		case <-time.After(5 * time.Second):
			t.Fatal("startup callback not entered")
		}
		databaseAdmissionCount(t, e, 1)
		databaseAdmissionFull(t, e, login)
		if len(core.EngineSessions(e)) != 0 {
			t.Fatal("initializer published before defaults completed")
		}
		unblock()
		r := roleLimitReceive(t, result)
		joined = true
		if r.err != nil {
			t.Fatal(r.err)
		}
		roleLimitClose(t, r.s)
		databaseAdmissionCount(t, e, 0)
	})
	for _, mode := range []string{"canceled", "zero identity", "defaults failure", "role limit"} {
		t.Run(mode, func(t *testing.T) {
			e, login := databaseAdmissionFixture()
			databaseAdmissionLimit(e, 2)
			var incumbent *session.Session
			if mode == "role limit" {
				var err error
				incumbent, err = enginetest.OpenSession(t.Context(), e, session.SessionStart{ID: ids.SessionID(testSessionID.Add(1))}, login)
				if err != nil {
					t.Fatal(err)
				}
				defer roleLimitClose(t, incumbent)
			}
			a := databaseAdmissionReserve(t, e, login)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			start := session.SessionStart{ID: ids.SessionID(testSessionID.Add(1))}
			sentinel := errors.New("defaults failure")
			switch mode {
			case "canceled":
				cancel()
			case "zero identity":
				start.ID = 0
			case "defaults failure":
				start.Defaults = &defaultProvider{err: sentinel}
			}
			s, err := e.StartSession(ctx, a, start)
			if s != nil || err == nil {
				t.Fatal("failed initialization published", s, err)
			}
			if mode == "canceled" && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if mode == "defaults failure" && !errors.Is(err, sentinel) {
				t.Fatal(err)
			}
			want := 0
			if incumbent != nil {
				want = 1
			}
			a.Release()
			databaseAdmissionCount(t, e, want)
			if incumbent != nil {
				roleLimitClose(t, incumbent)
			}
			next := databaseAdmissionReserve(t, e, login)
			next.Release()
			databaseAdmissionCount(t, e, 0)
		})
	}
}

func TestDatabaseAdmissionProvenance(t *testing.T) {
	e, login := databaseAdmissionFixture()
	databaseAdmissionLimit(e, 2)
	other := engine.New()
	admin := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, admin, "CREATE DATABASE admission_peer")
	peer := e.Cluster.Targets["admission_peer"]
	a := databaseAdmissionReserve(t, e, login)
	copy := *a
	before := len(core.EngineSessions(e))
	for _, candidate := range []struct {
		target *core.Engine
		token  *core.DatabaseAdmission
	}{
		{e, nil}, {e, &copy}, {other, a}, {peer, a},
	} {
		s, err := candidate.target.StartSession(t.Context(), candidate.token, session.SessionStart{ID: ids.SessionID(testSessionID.Add(1)), Defaults: &databaseAdmissionDefaults{apply: func() { t.Error("invalid token invoked startup defaults") }}})
		if s != nil || err == nil || len(core.EngineSessions(e)) != before {
			t.Fatal("foreign/copied admission accepted or mutated resources", s, err)
		}
		var diagnostic interface{ SQLState() string }
		if errors.As(err, &diagnostic) {
			t.Fatal("internal provenance failure fabricated SQLSTATE", err)
		}
		databaseAdmissionCount(t, e, 2) // The superuser setup owner is counted too.
		copy.Release()
	}
	peerToken, err := e.AdmitDatabase(t.Context(), login, core.DatabaseSelector{Name: peer.DatabaseName, Explicit: true})
	if err != nil || peerToken.Target() != peer {
		t.Fatal("independent target reservation failed", peerToken, err)
	}
	databaseAdmissionCount(t, peer, 1)
	peerToken.Release()
	databaseAdmissionCount(t, peer, 0)
	if a.Target() != e || copy.Target() != nil {
		t.Fatal("copied admission retained authority")
	}
	if token, err := e.AdmitDatabase(t.Context(), other.RoleStore.Domain.Principal(10), core.DatabaseSelector{}); token != nil || err == nil {
		t.Fatal("foreign principal reserved a target", token, err)
	}
	a.Release()
	if s, err := e.StartSession(t.Context(), a, session.SessionStart{ID: 1}); s != nil || err == nil {
		t.Fatal("released token consumed", s, err)
	}
	roleLimitClose(t, admin)
	databaseAdmissionCount(t, e, 0)
	a = databaseAdmissionReserve(t, e, login)
	s, err := e.StartSession(t.Context(), a, session.SessionStart{ID: ids.SessionID(testSessionID.Add(1))})
	if err != nil {
		t.Fatal(err)
	}
	defer roleLimitClose(t, s)
	if second, err := e.StartSession(t.Context(), a, session.SessionStart{ID: ids.SessionID(testSessionID.Add(1))}); second != nil || err == nil {
		t.Fatal("consumed token reused", second, err)
	}
	databaseAdmissionCount(t, e, 1)
}

func TestDatabaseAdmissionConcurrent(t *testing.T) {
	e, login := databaseAdmissionFixture()
	databaseAdmissionLimit(e, 1)
	ready := make(chan struct{})
	type outcome struct {
		token *core.DatabaseAdmission
		err   error
	}
	results := make(chan outcome, 16)
	for range cap(results) {
		go func() {
			<-ready
			a, err := e.AdmitDatabase(t.Context(), login, core.DatabaseSelector{})
			results <- outcome{a, err}
		}()
	}
	close(ready)
	var winner *core.DatabaseAdmission
	success := 0
	for range cap(results) {
		r := <-results
		if r.err == nil {
			success++
			winner = r.token
			t.Cleanup(r.token.Release)
		} else {
			var d *sqlstate.Error
			if !errors.As(r.err, &d) || d.Code != "53300" {
				t.Error(r.err)
			}
		}
	}
	if success != 1 {
		t.Fatalf("successful reservations %d, want 1", success)
	}
	databaseAdmissionCount(t, e, 1)
	winner.Release()
	next := databaseAdmissionReserve(t, e, login)
	next.Release()
	databaseAdmissionCount(t, e, 0)
}

func TestDatabaseAdmissionEnding(t *testing.T) {
	e, login := databaseAdmissionFixture()
	databaseAdmissionLimit(e, 1)
	a := databaseAdmissionReserve(t, e, login)
	s, err := e.StartSession(t.Context(), a, session.SessionStart{ID: ids.SessionID(testSessionID.Add(1))})
	if err != nil {
		t.Fatal(err)
	}
	gate := &roleLimitEndGate{entered: make(chan struct{}), release: make(chan struct{})}
	var once sync.Once
	unblock := func() { once.Do(func() { close(gate.release) }) }
	defer func() { unblock(); roleLimitClose(t, s) }()
	s.Mu.Lock()
	entry := s.Resources[session.Authorization]
	gate.owner, entry.Owner = entry.Owner, gate
	s.Mu.Unlock()
	s.RequestEnd(session.CleanClose)
	select {
	case <-gate.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("terminal cleanup not entered")
	}
	databaseAdmissionCount(t, e, 1)
	databaseAdmissionFull(t, e, login)
	a.Release()
	databaseAdmissionCount(t, e, 1)
	unblock()
	roleLimitClose(t, s)
	databaseAdmissionCount(t, e, 0)
	next := databaseAdmissionReserve(t, e, login)
	next.Release()
	next = databaseAdmissionReserve(t, e, login)
	failed, err := e.StartSession(t.Context(), next, session.SessionStart{ID: ids.SessionID(testSessionID.Add(1))})
	if err != nil {
		t.Fatal(err)
	}
	sentinel := errors.New("terminal owner cleanup failed")
	failed.Mu.Lock()
	authEntry := failed.Resources[session.Authorization]
	original := authEntry.Owner
	authEntry.Owner = &sessionOwner{fn: func(token ids.SessionToken, reason session.EndReason) error {
		return errors.Join(original.EndSession(token, reason), sentinel)
	}}
	failed.Mu.Unlock()
	failed.RequestEnd(session.CleanClose)
	if err := failed.WaitEnd(t.Context()); !errors.Is(err, sentinel) {
		t.Fatal("cleanup failure lost", err)
	}
	databaseAdmissionCount(t, e, 0)
	last := databaseAdmissionReserve(t, e, login)
	last.Release()
}
