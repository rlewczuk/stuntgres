package core_test

import (
	"context"
	"runtime"
	"testing"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

func TestDatabaseDropPrivateMaintenance(t *testing.T) {
	e, target, admin := databaseDropOwnerFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), enginetest.Liveness)
	defer cancel()
	public := enginetest.StartSession(t, target, nil)
	enginetest.SessionSQL(t, public, "CREATE TEMP TABLE drop_private_temp(v integer)")
	enginetest.SessionSQL(t, public, "INSERT INTO drop_private_temp VALUES(11)")
	namespace := public.TemporaryNamespace
	address := target.CatalogRead().NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: namespace, Name: "drop_private_temp"})
	stored := target.Storage.Relations[address]
	if namespace == (ids.ObjectAddress{}) || stored == nil {
		t.Fatal("fixture lacks genuine TEMP backing")
	}
	publicLease, err := public.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer publicLease.Done()
	var private []*session.Session
	var operations []*session.Operation
	var scopes []*core.StatementScope
	for range 2 {
		s, err := target.StartRestoreSession(ctx, admin.CountedAuthenticated)
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
		operations = append(operations, op)
		t.Cleanup(op.Done)
		scope, err := target.StartStatement(op)
		if err != nil {
			t.Fatal(err)
		}
		scopes = append(scopes, scope)
		t.Cleanup(func() { scope.Finish(context.Canceled) })
		if err := scope.Gate(&parser.Select{}); err != nil {
			t.Fatal(err)
		}
	}
	blocker := scopes[1]
	ref := target.Catalog.Domain.Ref(target.CatalogRead().Epoch(), address)
	if err := blocker.AcquireSequence(ref, lock.AccessExclusive, true); err != nil {
		t.Fatal(err)
	}
	if core.EngineDatabaseAdmissionCount(target) != 1 {
		t.Fatal("private owners added public occupancy")
	}
	done := make(chan error, 1)
	joined := false
	defer func() {
		publicLease.Done()
		for i, scope := range scopes {
			_ = scope.Finish(context.Canceled)
			operations[i].Done()
		}
		if !joined {
			<-done
		}
	}()
	go func() {
		_, err := databaseDropOwnerExecute(ctx, e, admin, databaseDropOwnerStatement(true))
		done <- err
	}()
	for _, s := range private {
		select {
		case <-s.EndRequested():
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	select {
	case <-public.EndRequested():
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	publicLease.Done()
	// Real public end creates a separate ID0 maintenance owner after FORCE has
	// closed unrelated private admission. Its actual TEMP relation AX waits on
	// the old private transaction above, not a fabricated test callback.
	for {
		core.EngineSessionMuRef(target).Lock()
		count := 0
		for token := range core.EngineSessions(target) {
			if token.Private() != nil {
				count++
			}
		}
		core.EngineSessionMuRef(target).Unlock()
		if count >= 3 {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("causal TEMP maintenance never registered", ctx.Err())
		default:
			runtime.Gosched()
		}
	}
	if _, err := target.StartRestoreSession(ctx, admin.CountedAuthenticated); err == nil {
		t.Fatal("unrelated private admission entered draining target")
	}
	_ = e.CatalogRead()
	_ = target.CatalogRead()
	select {
	case err := <-done:
		joined = true
		t.Fatal("DROP published before private resources drained", err)
	default:
	}
	for i, scope := range scopes {
		_ = scope.Finish(context.Canceled)
		operations[i].Done()
	}
	dropErr := databaseDropReceive(t, done)
	joined = true
	if dropErr != nil {
		t.Fatal(dropErr)
	}
	for _, s := range append(private, public) {
		select {
		case <-s.Completed:
		default:
			t.Fatal("retirement preceded terminal completion")
		}
	}
	if public.TemporaryNamespace != (ids.ObjectAddress{}) || len(target.Storage.Relations) != 0 {
		t.Fatal("canonical TEMP/backing cleanup did not complete")
	}
	if len(stored.Versions) != 1 {
		t.Fatal("retirement mutated detached TEMP backing")
	}
}
