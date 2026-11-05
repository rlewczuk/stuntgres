package core_test

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/core/contrib"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

// Native private owners have no PostgreSQL-format counterpart. Exercise actual
// session registration, admitted statement hosts and the shared capture gateway,
// not a fabricated cohort or synthetic transaction-owner mutexes.
func TestPrivateSessionLifecycleCohort(t *testing.T) {
	e := engine.New()
	public := enginetest.StartSession(t, e, nil)
	principal := public.CountedAuthenticated
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	var private []*session.Session
	var scopes []*core.StatementScope
	var hosts []contrib.Host[core.AuthorizationView]
	for range 3 {
		s, err := e.StartRestoreSession(ctx, principal)
		if err != nil {
			t.Fatal(err)
		}
		private = append(private, s)
		t.Cleanup(func() {
			s.RequestEnd(session.CleanClose)
			if err := s.WaitEnd(context.Background()); err != nil {
				t.Error(err)
			}
		})
		op, err := s.Acquire(ctx)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(op.Done)
		scope, err := e.StartStatement(op)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = scope.Finish(context.Canceled) })
		if err := scope.Gate(&parser.Select{}); err != nil {
			t.Fatal(err)
		}
		scopes = append(scopes, scope)
		hosts = append(hosts, core.HostOf(scope).(contrib.Host[core.AuthorizationView]))
	}
	owners, err := hosts[0].LockHeapStatisticsCohort()
	if err != nil {
		t.Fatal(err)
	}
	hosts[0].UnlockHeapStatisticsCohortLocked(owners)
	if len(owners) != len(private)+1 || owners[len(private)] != public.Transactions {
		t.Fatal("cohort lost genuine private owners or public numeric order")
	}
	for i, owner := range owners[:len(private)] {
		if !slices.Contains(private, owner.Session) || owner.Session.SessionToken.ID() != 0 || owner.Session.SessionToken.Private() != owner.Session {
			t.Fatal("cohort contains a fabricated private owner")
		}
		if i > 0 && reflect.ValueOf(owners[i-1].Session).Pointer() >= reflect.ValueOf(owner.Session).Pointer() {
			t.Fatal("distinct ID0 owners lack a strict stable pointer order")
		}
	}
	start := make(chan struct{})
	results := make(chan error, len(hosts))
	var workers sync.WaitGroup
	for _, host := range hosts {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			for range 32 {
				captured, err := host.LockHeapStatisticsCohort()
				if err != nil {
					results <- err
					return
				}
				equal := slices.Equal(captured, owners)
				host.UnlockHeapStatisticsCohortLocked(captured)
				if !equal {
					results <- fmt.Errorf("opposing private capture changed owner order or membership")
					return
				}
			}
			results <- nil
		}()
	}
	close(start)
	workers.Wait()
	for range hosts {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if core.EngineDatabaseAdmissionCount(e) != 1 || core.EngineRoleSessionCountLocked(e, principal) != 1 || len(e.Statistics.CaptureActivities()) != 1 {
		t.Fatal("private captures changed public admission, role occupancy or activity")
	}
	for _, scope := range scopes {
		if err := scope.Finish(nil); err != nil {
			t.Fatal(err)
		}
		scope.Op.Done()
	}
	for _, s := range private {
		s.RequestEnd(session.CleanClose)
		if err := s.WaitEnd(ctx); err != nil {
			t.Fatal(err)
		}
	}
	core.EngineSessionMuRef(e).Lock()
	retainedPublic := len(core.EngineSessions(e)) == 1 && core.EngineSessions(e)[public.SessionToken] == public
	core.EngineSessionMuRef(e).Unlock()
	if !retainedPublic || core.EngineDatabaseAdmissionCount(e) != 1 || core.EngineRoleSessionCountLocked(e, principal) != 1 || len(e.Statistics.CaptureActivities()) != 1 {
		t.Fatal("private terminal cleanup removed public ownership or leaked occupancy")
	}
}
