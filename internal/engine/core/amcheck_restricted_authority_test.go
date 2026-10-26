package core_test

import (
	"context"
	"strings"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"testing"
	"time"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// The real routine and host boundaries must agree even when both enclosing
// action authorities differ from the caller and restricted table owner.
func TestAmcheckRestrictedAuthorityPrecedence(t *testing.T) {
	for _, grant := range []string{"am_caller", "am_ri", "am_rule", "am_owner", "queued_owner", "queued_am_caller", "queued_am_ri", "queued_am_rule"} {
		t.Run(grant, func(t *testing.T) {
			e, d, _ := amcheckUnitRoot(t, false)
			for _, name := range []string{"am_caller", "am_ri", "am_rule", "am_owner"} {
				enginetest.Run(t, e, "CREATE ROLE "+name)
			}
			enginetest.Run(t, e, "ALTER TABLE t OWNER TO am_owner")
			parent := e.Catalog.State.MustRelation(d.SQLIndex.Parent)
			allowed := grant
			queued := strings.HasPrefix(grant, "queued_")
			if queued {
				allowed = "am_owner"
			}
			enginetest.Run(t, e, "GRANT EXECUTE ON FUNCTION bt_index_parent_check(regclass) TO "+allowed)
			if queued && grant != "queued_owner" {
				enginetest.Run(t, e, "GRANT EXECUTE ON FUNCTION bt_index_parent_check(regclass) TO "+strings.TrimPrefix(grant, "queued_"))
			}
			binder := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
			node, err := binder.BindFunctionProvider(parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "bt_index_parent_check"}}}, Args: []parser.Expr{parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "i"}}}})
			if err != nil {
				t.Fatal(err)
			}
			n := node.(plan.InstalledAmcheckExpr)
			reader, ticket := blockingUnitOwner(t, e)
			sessionSQL(t, reader, "SET ROLE am_caller")
			scope := pgrowlocksUnitScope(t, e, reader)
			scope.RoutineDependencies = binder.Dependencies
			e.Mu.Lock()
			caller, err := e.AuthorizationLocked(scope.Op)
			e.Mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			ri, _ := e.Roles().Lookup("am_ri")
			rule, _ := e.Roles().Lookup("am_rule")
			riAuth, ruleAuth := core.AuthorizationActingAs(caller, ri.Ref), core.AuthorizationActingAs(caller, rule.Ref)
			scope.ForeignActionAuth = &riAuth
			ruleScratch := core.ScopeRuleScratch(scope)
			ruleScratch.RuleAction.Auth = &ruleAuth
			core.ScopeInstallRuleScratch(scope, ruleScratch)
			beforeRole := scope.Op.RoleState()
			// Ordinary writes still use RI-over-rule, and a rule-only write
			// still uses the rule actor. This fix must not change either.
			e.Mu.Lock()
			write, err := core.ScopeWriteAuthorizationLocked(scope, e)
			e.Mu.Unlock()
			if err != nil || write.State.CurrentUser != ri.Ref {
				t.Fatal("ordinary RI write authority", err)
			}
			scope.ForeignActionAuth = nil
			e.Mu.Lock()
			write, err = core.ScopeWriteAuthorizationLocked(scope, e)
			e.Mu.Unlock()
			if err != nil || write.State.CurrentUser != rule.Ref {
				t.Fatal("ordinary rule write authority", err)
			}
			scope.ForeignActionAuth = &riAuth
			indexResource := lock.Resource{ObjectRef: e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), d.Address())}
			var holder *lock.Ticket
			if queued {
				_, holder = blockingUnitOwner(t, e)
				amcheckUnitGrant(t, e, holder, indexResource, lock.AccessExclusive)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			scope.Op.Ctx = ctx
			invoke := func() error {
				return scope.WithAmcheckOwner(parent, func(evaluation *exec.Context) error {
					e.Mu.Lock()
					routine, err := scope.RoutineAuthorizationLocked()
					e.Mu.Unlock()
					if err != nil {
						return err
					}
					if routine.State.CurrentUser != parent.Owner {
						t.Error("restricted routine actor is not table owner")
					}
					// This is a retained recipe callback under an invocation-local
					// authority. Carry its actual binder manifest through the same
					// recipe admission route used by executable stored expressions.
					evaluation.RoutineRecipe = true
					evaluation.RoutineChecks = binder.Dependencies
					if err := exec.AdmitRoutineCall(evaluation, n, true, false); err != nil {
						return err
					}
					_, err = n.Eval(evaluation)
					return err
				})
			}
			if queued {
				done := make(chan error, 1)
				stopped := make(chan struct{})
				go func() { defer close(stopped); done <- invoke() }()
				defer func() {
					cancel()
					// Queue observation may already have consumed the error.
					// Join completion independently, never consume it twice.
					select {
					case <-stopped:
					case <-time.After(5 * time.Second):
						t.Error("amcheck worker did not stop")
					}
				}()
				amcheckUnitQueue(t, e, ticket, indexResource, lock.Share, done)
				if grant != "queued_owner" {
					state := catalog.CloneState(e.Catalog.State)
					proc := *state.MustProcedure(n.Ref.Address)
					entries := make([]authorization.ACLEntry, 0, len(proc.ACL.Entries))
					for _, entry := range proc.ACL.Entries {
						if entry.Grantee.Role != parent.Owner {
							entries = append(entries, entry)
						}
					}
					proc.ObjectRevision++
					proc.ACL.Explicit = true
					proc.ACL.Entries = entries
					state.SetDescriptor(proc.Address(), &proc)
					e.Mu.Lock()
					e.Catalog.PublishLocked(state)
					e.Mu.Unlock()
				}
				e.EndLockOwner(holder)
				err = amcheckUnitJoin(t, done)
			} else {
				err = invoke()
			}
			if grant == "am_owner" || grant == "queued_owner" {
				if err != nil {
					t.Fatal("owner EXECUTE refused", err)
				}
			} else {
				amcheckUnitDiagnostic(t, err, sqlstate.Error{Code: "42501", Message: "permission denied for function bt_index_parent_check"})
			}
			if scope.Op.Restricted != nil || scope.Op.RoleState() != beforeRole || scope.ForeignActionAuth != &riAuth || core.ScopeRuleScratch(scope).RuleAction.Auth != &ruleAuth {
				t.Fatal("enclosing authority not restored")
			}
			e.Mu.Lock()
			restored, err := scope.RoutineAuthorizationLocked()
			write, writeErr := core.ScopeWriteAuthorizationLocked(scope, e)
			e.Mu.Unlock()
			if err != nil || writeErr != nil || restored.State.CurrentUser != ri.Ref || write.State.CurrentUser != ri.Ref {
				t.Fatal("restored RI authority", err, writeErr)
			}
			for _, resource := range []lock.Resource{indexResource, {ObjectRef: e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), d.SQLIndex.Parent)}} {
				if e.Locks.HasRelationMode(ticket, resource, lock.Share) {
					t.Fatal("authority failure leaked checker grant")
				}
			}
		})
	}
}
