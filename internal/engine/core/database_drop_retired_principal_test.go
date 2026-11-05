package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestDatabaseDropRetiredPrincipalOwner(t *testing.T) {
	e, target, admin := databaseDropOwnerFixture(t)
	enginetest.SessionSQL(t, admin, "CREATE ROLE drop_retired_login LOGIN")
	enginetest.SessionSQL(t, admin, "CREATE ROLE drop_retired_assumed")
	enginetest.SessionSQL(t, admin, "GRANT drop_retired_assumed TO drop_retired_login")
	original := e.Roles().LookupRef("drop_retired_login")
	assumed := e.Roles().LookupRef("drop_retired_assumed")
	retained := e.Roles()
	open := func(runtime *core.Engine, principal authorization.PrincipalRef) *session.Session {
		t.Helper()
		s, err := enginetest.OpenSession(t.Context(), runtime, session.SessionStart{ID: enginetest.NextSessionID()}, principal)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			s.RequestEnd(session.CleanClose)
			ctx, cancel := context.WithTimeout(context.Background(), enginetest.Liveness)
			defer cancel()
			if err := s.WaitEnd(ctx); err != nil {
				t.Error(err)
			}
		})
		return s
	}
	victim := open(target, original)
	enginetest.SessionSQL(t, victim, "SET ROLE drop_retired_assumed")
	enginetest.SessionSQL(t, admin, "DROP ROLE drop_retired_login")
	enginetest.SessionSQL(t, admin, "CREATE ROLE drop_retired_login LOGIN")
	replacement := e.Roles().LookupRef("drop_retired_login")
	if replacement == original || original == (authorization.PrincipalRef{}) {
		t.Fatal("replacement adopted retired principal")
	}
	if _, ok := e.Roles().Find(original); ok {
		t.Fatal("original role still present")
	}
	if d, err := retained.Descriptor(original); err != nil || d.Ref != original || d.Name != "drop_retired_login" {
		t.Fatal("retained authority changed", d, err)
	}
	victim.Mu.Lock()
	physical, current := victim.CountedAuthenticated, victim.Authorization.State.CurrentUser
	victim.Mu.Unlock()
	if physical != original || current != assumed || physical == current {
		t.Fatal("physical authentication was rebound", physical, current)
	}
	caller := open(e, replacement)
	for _, kind := range []functions.SessionSignalKind{functions.SignalCancel, functions.SignalTerminate} {
		op, _, err := caller.AcquireCommand(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		scope, err := e.StartStatement(op)
		if err != nil {
			op.Done()
			t.Fatal(err)
		}
		e.Mu.Lock()
		auth, err := scope.RoutineAuthorizationLocked()
		if err == nil {
			err = scope.CaptureIdentityLocked(auth)
		}
		e.Mu.Unlock()
		if err == nil {
			evaluation := &exec.Context{EvalStatement: exec.EvalStatement{Context: op.Context(), Host: core.HostOf(scope)}}
			_, err = evaluation.SessionSignalValue(kind, int64(victim.Token().ID()), 0, enginetest.Usage(e, sqltype.Bool).Type)
		}
		err = scope.Finish(err)
		op.Done()
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "42501" {
			t.Fatal("replacement gained signaling authority", err)
		}
	}
	select {
	case <-victim.EndRequested():
		t.Fatal("denied replacement terminated physical owner")
	default:
	}
	ctx, cancel := context.WithTimeout(t.Context(), enginetest.Liveness)
	defer cancel()
	lease, err := victim.Acquire(ctx)
	if err != nil {
		t.Fatal("permission rejection invalidated original owner", err)
	}
	done := make(chan error, 1)
	joined := false
	defer func() {
		lease.Done()
		cancel()
		if !joined {
			cleanup, stop := context.WithTimeout(context.Background(), enginetest.Liveness)
			defer stop()
			select {
			case <-done:
			case <-cleanup.Done():
				t.Error("FORCE worker did not release ownership during cleanup", cleanup.Err())
			}
		}
	}()
	go func() {
		_, err := databaseDropOwnerExecute(ctx, e, admin, databaseDropOwnerStatement(true))
		done <- err
	}()
	select {
	case <-victim.EndRequested():
	case <-ctx.Done():
		t.Fatal("superuser FORCE did not terminate retired owner", ctx.Err())
	}
	select {
	case <-victim.Completed:
		t.Fatal("physical completion preceded lease release")
	case err := <-done:
		joined = true
		t.Fatal("FORCE completed before physical drain", err)
	default:
	}
	if core.EngineDatabaseAdmissionCount(target) != 1 || victim.CountedAuthenticated != original {
		t.Fatal("retirement lost or rebound the original admission")
	}
	lease.Done()
	err = databaseDropReceive(t, done)
	joined = true
	if err != nil {
		t.Fatal(err)
	}
	if err := victim.WaitEnd(ctx); err != nil {
		t.Fatal(err)
	}
	victim.Mu.Lock()
	state, leases, fatal := victim.State, victim.Leases, victim.Timeouts.Fatal
	for _, resource := range victim.Resources {
		if resource != nil {
			t.Error("completed physical owner retained a resource")
		}
	}
	victim.Mu.Unlock()
	want := sqlstate.Error{Severity: "FATAL", SeverityUnlocalized: "FATAL", Code: "57P01", Message: "terminating connection due to administrator command"}
	if state != session.Ended || leases != 0 || fatal == nil || *fatal != want || core.EngineDatabaseAdmissionCount(target) != 0 {
		t.Fatal("retired physical owner did not drain canonically", state, leases, fatal)
	}
	if op, err := caller.Acquire(ctx); err != nil {
		t.Fatal("unrelated replacement physical owner was terminated", err)
	} else {
		op.Done()
	}
}
