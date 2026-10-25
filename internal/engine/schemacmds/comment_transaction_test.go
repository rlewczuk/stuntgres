package schemacmds_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/session"
)

func TestCommentRoleInstallerDisposition(t *testing.T) {
	for _, mode := range []string{"autocommit", "explicit", "retained"} {
		t.Run(mode, func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			if mode == "explicit" {
				ref := enginetest.TransactionBegin(t, s)
				defer enginetest.TransactionEnd(t, s, ref, false)
			}
			op := enginetest.TransactionOperation(t, s)
			defer op.Done()
			if mode == "retained" {
				s.Transactions.RetainedMessage = true
				defer func() { s.Transactions.RetainedMessage = false }()
			}
			e.Mu.Lock()
			defer e.Mu.Unlock()
			base := e.RoleStore.Published
			next := &access.RoleRecord{Revision: base.Revision + 1, Root: base.Root}
			core.InstallOperationRolesLocked(e, op, next)
			if mode == "autocommit" {
				if e.RoleStore.Published != next || s.Transactions.Catalog != nil && s.Transactions.Catalog.Roles != nil {
					t.Fatal("autocommit did not publish exactly once")
				}
				return
			}
			if e.RoleStore.Published != base || s.Transactions.Catalog == nil || s.Transactions.Catalog.Roles != next || s.Transactions.Catalog.RolesBase != base {
				t.Fatal("private role installer leaked committed root or lost baseline")
			}
			retained := s.Transactions.Catalog
			last := &access.RoleRecord{Revision: next.Revision + 1, Root: next.Root}
			core.InstallOperationRolesLocked(e, op, last)
			if e.RoleStore.Published != base || retained.Roles != next || retained.RolesBase != base || s.Transactions.Catalog == retained || s.Transactions.Catalog.Roles != last || s.Transactions.Catalog.RolesBase != base {
				t.Fatal("installer mutated retained journal or recaptured baseline")
			}
		})
	}
}

func TestCommentRoleFailedFinish(t *testing.T) {
	for _, failure := range []string{"context", "caller"} {
		t.Run(failure, func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, s, "CREATE ROLE comment_finish_role")
			enginetest.SessionSQL(t, s, "COMMENT ON ROLE comment_finish_role IS 'old'")
			ref := enginetest.TransactionBegin(t, s)
			enginetest.SessionSQL(t, s, "COMMENT ON ROLE comment_finish_role IS 'earlier'")
			enginetest.SessionSQL(t, s, "SAVEPOINT earlier")
			base := e.RoleStore.Published
			before := s.Transactions.Catalog
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			op, err := s.Acquire(ctx)
			if err != nil {
				t.Fatal(err)
			}
			scope, err := e.StartStatement(op)
			if err != nil {
				op.Done()
				t.Fatal(err)
			}
			_, err = scope.Execute(enginetest.Statement(t, "COMMENT ON ROLE comment_finish_role IS 'failed'"), 0, nil)
			if err != nil {
				scope.Finish(err)
				op.Done()
				t.Fatal(err)
			}
			injected := errors.New("comment caller completion failure")
			var finishErr error
			if failure == "context" {
				cancel()
				injected = context.Canceled
				finishErr = scope.Finish(nil)
			} else {
				finishErr = scope.Finish(injected)
			}
			if !errors.Is(finishErr, injected) || !errors.Is(scope.Finish(nil), injected) {
				op.Done()
				t.Fatal("lost/idempotent Finish error", finishErr)
			}
			op.Done()
			if e.RoleStore.Published != base || s.TransactionState() != session.TxAborted {
				t.Fatal("failed completion leaked shared root or did not abort")
			}
			enginetest.SessionSQL(t, s, "ROLLBACK TO earlier")
			if s.Transactions.Catalog.Roles != before.Roles || s.Transactions.Catalog.RolesBase != before.RolesBase {
				t.Fatal("savepoint did not restore exact private role authority")
			}
			enginetest.TransactionEnd(t, s, ref, true)
			if role, ok := e.Roles().Lookup("comment_finish_role"); !ok || role.Comment != "earlier" {
				t.Fatal("failed role text survived recovery", role)
			}
		})
	}
}
