package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/session"
)

func TestCommentRoleInstallerDisposition(t *testing.T) {
	for _, mode := range []string{"autocommit", "explicit", "retained"} {
		t.Run(mode, func(t *testing.T) {
			e := New()
			s := sessionStart(t, e, nil)
			if mode == "explicit" {
				ref := transactionBegin(t, s)
				defer transactionEnd(t, s, ref, false)
			}
			op := transactionOperation(t, s)
			defer op.Done()
			if mode == "retained" {
				s.Transactions.RetainedMessage = true
				defer func() { s.Transactions.RetainedMessage = false }()
			}
			e.Mu.Lock()
			defer e.Mu.Unlock()
			base := e.RoleStore.Published
			next := &access.RoleRecord{Revision: base.Revision + 1, Root: base.Root}
			installOperationRolesLocked(e, op, next)
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
			installOperationRolesLocked(e, op, last)
			if e.RoleStore.Published != base || retained.Roles != next || retained.RolesBase != base || s.Transactions.Catalog == retained || s.Transactions.Catalog.Roles != last || s.Transactions.Catalog.RolesBase != base {
				t.Fatal("installer mutated retained journal or recaptured baseline")
			}
		})
	}
}

func TestCommentRoleFailedFinish(t *testing.T) {
	for _, failure := range []string{"context", "caller"} {
		t.Run(failure, func(t *testing.T) {
			e := New()
			s := sessionStart(t, e, nil)
			sessionSQL(t, s, "CREATE ROLE comment_finish_role")
			sessionSQL(t, s, "COMMENT ON ROLE comment_finish_role IS 'old'")
			ref := transactionBegin(t, s)
			sessionSQL(t, s, "COMMENT ON ROLE comment_finish_role IS 'earlier'")
			sessionSQL(t, s, "SAVEPOINT earlier")
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
			_, err = scope.Execute(statement(t, "COMMENT ON ROLE comment_finish_role IS 'failed'"), 0, nil)
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
			sessionSQL(t, s, "ROLLBACK TO earlier")
			if s.Transactions.Catalog.Roles != before.Roles || s.Transactions.Catalog.RolesBase != before.RolesBase {
				t.Fatal("savepoint did not restore exact private role authority")
			}
			transactionEnd(t, s, ref, true)
			if role, ok := e.Roles().Lookup("comment_finish_role"); !ok || role.Comment != "earlier" {
				t.Fatal("failed role text survived recovery", role)
			}
		})
	}
}
