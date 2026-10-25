package core_test

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"time"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
)

// This is a native admitted-program invariant, not a PostgreSQL SQL diagnostic
// claim: the real importer parks on a captured helper identity, then observes a
// role-root publication before it can publish any executable policy program.
func TestRLSPolicyFreshness(t *testing.T) {
	for _, changed := range []bool{false, true} {
		t.Run(map[bool]string{false: "same_selection", true: "changed_selection"}[changed], func(t *testing.T) {
			e, op, done, _ := policyUnitFixture(t)
			defer done()
			for _, q := range []string{
				"CREATE ROLE freshness_actor", "CREATE ROLE freshness_group",
				"GRANT freshness_group TO freshness_actor WITH INHERIT TRUE",
				"GRANT USAGE ON SCHEMA public TO freshness_actor",
				"GRANT SELECT ON helper TO freshness_actor",
				"ALTER TABLE t ENABLE ROW LEVEL SECURITY",
				"CREATE POLICY p ON t TO freshness_group USING(EXISTS(SELECT 1 FROM helper h WHERE h.id=t.id))",
			} {
				enginetest.RoleExecute(t, e, op, q)
			}
			admin, closeAdmin := enginetest.RoleOperation(t, e, "postgres")
			defer closeAdmin()
			holder, closeHolder := enginetest.RoleOperation(t, e, "postgres")
			defer closeHolder()
			tx, err := e.Begin(holder, session.TxOptions{})
			if err != nil {
				t.Fatal(err)
			}
			defer e.Rollback(holder, tx)
			helper := roleTableRef(t, e, "helper")
			resource := lock.Resource{ObjectRef: helper}
			ticket := holder.Session.Transactions.Ticket
			request, err := e.Locks.StartAcquire(holder.Context(), ticket, resource, lock.AccessExclusive, lock.Policy{})
			if err == nil && request != nil {
				err = request.Wait()
			}
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(op.Ctx, 5*time.Second)
			defer cancel()
			op.Ctx = ctx
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			var operationErr error
			defer func() {
				err := scope.Finish(operationErr)
				if operationErr == nil && err != nil {
					t.Error(err)
				}
			}()
			if _, err := scope.Describe(enginetest.Statement(t, "SELECT id FROM t")); err != nil {
				t.Fatal(err)
			}
			target := roleTableRef(t, e, "t")
			e.Mu.Lock()
			descriptor := e.PrivateCatalogLocked(scope.Owner.Catalog).MustRelation(target.Address)
			actor := e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("freshness_actor")
			e.Mu.Unlock()
			type outcome struct {
				program *exec.RLSProgram
				err     error
			}
			results := make(chan outcome, 1)
			joined := false
			defer func() {
				if !joined {
					cancel()
					e.Rollback(holder, tx)
					select {
					case <-results:
					case <-time.After(5 * time.Second):
						t.Error("policy importer failed to join")
					}
				}
			}()
			go func() {
				e.Mu.Lock()
				p, err := core.EngineCaptureRowSecurityLocked(e, scope, descriptor, actor, catalog.PolicySelect, false, false)
				e.Mu.Unlock()
				results <- outcome{p, err}
			}()
			waiting := false
			for ctx.Err() == nil {
				select {
				case out := <-results:
					joined = true
					operationErr = out.err
					t.Fatalf("importer exited before helper lock wait: %v", out.err)
				default:
				}
				state := e.Locks.Inspect(ticket)
				if entry := state.Resources[resource]; entry != nil {
					for _, waiter := range entry.Pending {
						if !waiter.Terminal && waiter.Mode == lock.AccessShare {
							waiting = true
							break
						}
					}
				}
				if waiting {
					break
				}
				runtime.Gosched()
			}
			if !waiting {
				t.Fatal("missing actual imported-source wait", ctx.Err())
			}
			if changed {
				enginetest.RoleExecute(t, e, admin, "REVOKE freshness_group FROM freshness_actor")
			} else {
				enginetest.RoleExecute(t, e, admin, "CREATE ROLE freshness_unrelated")
			}
			if _, err := e.Rollback(holder, tx); err != nil {
				t.Fatal(err)
			}
			var out outcome
			select {
			case out = <-results:
				joined = true
			case <-ctx.Done():
				t.Fatal("importer did not complete", ctx.Err())
			}
			operationErr = out.err
			if changed {
				if !errors.Is(out.err, ids.ErrCatalogReplaced) || out.program != nil {
					t.Fatal("changed selection published a stale grant", out.program, out.err)
				}
			} else if out.err != nil || out.program == nil || out.program.Denied() {
				t.Fatal("unrelated role revision invalidated same program", out.program, out.err)
			}
			if len(scope.Stage.Rows) != 0 || len(scope.Stage.Deletes) != 0 {
				t.Fatal("admission published row side effects")
			}
		})
	}
}

// A later sibling's wait must fence earlier guarded, bypass and transitive
// selections too. The public executor cannot receive a stale executable source.
func TestRLSPolicyDependencyFreshness(t *testing.T) {
	for _, kind := range []string{"earlier_program", "earlier_bypass", "nested_program", "cancel_later"} {
		t.Run(kind, func(t *testing.T) {
			e, op, done, _ := policyUnitFixture(t)
			defer done()
			for _, sql := range []string{
				"CREATE ROLE dependency_actor", "CREATE ROLE dependency_group",
				"GRANT dependency_group TO dependency_actor WITH INHERIT TRUE",
				"GRANT USAGE ON SCHEMA public TO dependency_actor",
				"CREATE TABLE later(id INT)", "CREATE TABLE early(id INT)",
				"GRANT SELECT ON t,later,early,helper TO dependency_actor",
				"INSERT INTO t VALUES(1,TRUE)", "INSERT INTO later VALUES(1)", "INSERT INTO early VALUES(1)", "INSERT INTO helper VALUES(1)",
				"ALTER TABLE t ENABLE ROW LEVEL SECURITY", "ALTER TABLE later ENABLE ROW LEVEL SECURITY",
				"CREATE POLICY later_p ON later USING(EXISTS(SELECT 1 FROM helper WHERE helper.id=later.id))",
			} {
				enginetest.RoleExecute(t, e, op, sql)
			}
			switch kind {
			case "earlier_bypass":
				enginetest.RoleExecute(t, e, op, "CREATE POLICY p ON t USING(FALSE)")
				enginetest.RoleExecute(t, e, op, "ALTER TABLE t OWNER TO dependency_group")
			case "nested_program":
				enginetest.RoleExecute(t, e, op, "ALTER TABLE early ENABLE ROW LEVEL SECURITY")
				enginetest.RoleExecute(t, e, op, "CREATE POLICY early_p ON early TO dependency_group USING(TRUE)")
				enginetest.RoleExecute(t, e, op, "CREATE POLICY p ON t USING(EXISTS(SELECT 1 FROM early WHERE early.id=t.id))")
			default:
				enginetest.RoleExecute(t, e, op, "CREATE POLICY p ON t TO dependency_group USING(TRUE)")
			}
			enginetest.RoleExecute(t, e, op, "SET ROLE dependency_actor")
			admin, closeAdmin := enginetest.RoleOperation(t, e, "postgres")
			defer closeAdmin()
			holder, closeHolder := enginetest.RoleOperation(t, e, "postgres")
			defer closeHolder()
			tx, err := e.Begin(holder, session.TxOptions{})
			if err != nil {
				t.Fatal(err)
			}
			defer e.Rollback(holder, tx)
			resource := lock.Resource{ObjectRef: roleTableRef(t, e, "helper")}
			ticket := holder.Session.Transactions.Ticket
			request, err := e.Locks.StartAcquire(holder.Context(), ticket, resource, lock.AccessExclusive, lock.Policy{})
			if err == nil && request != nil {
				err = request.Wait()
			}
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(op.Ctx, 5*time.Second)
			defer cancel()
			op.Ctx = ctx
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			var operationErr error
			defer func() {
				err := scope.Finish(operationErr)
				if operationErr == nil && err != nil {
					t.Error(err)
				}
			}()
			if _, err := scope.Describe(enginetest.Statement(t, "SELECT t.id FROM t JOIN later ON t.id=later.id")); err != nil {
				t.Fatal(err)
			}
			if len(scope.QuerySources) != 2 {
				t.Fatal("fixture source order", scope.QuerySources)
			}
			type outcome struct {
				source *exec.Source
				err    error
			}
			results := make(chan outcome, 1)
			joined := false
			defer func() {
				if !joined {
					cancel()
					e.Rollback(holder, tx)
					select {
					case <-results:
					case <-time.After(5 * time.Second):
						t.Error("dependency importer failed to join")
					}
				}
			}()
			go func() {
				e.Mu.Lock()
				source, err := e.CaptureQuerySourcesLocked(scope, scope.QuerySources, false)
				e.Mu.Unlock()
				results <- outcome{source, err}
			}()
			waiting := false
			for ctx.Err() == nil {
				select {
				case out := <-results:
					joined = true
					operationErr = out.err
					if out.source != nil {
						out.source.Close()
					}
					t.Fatalf("later importer exited before actual wait: %v", out.err)
				default:
				}
				state := e.Locks.Inspect(ticket)
				if entry := state.Resources[resource]; entry != nil {
					for _, waiter := range entry.Pending {
						if !waiter.Terminal && waiter.Mode == lock.AccessShare {
							waiting = true
							break
						}
					}
				}
				if waiting {
					break
				}
				runtime.Gosched()
			}
			if !waiting {
				t.Fatal("missing later helper wait", ctx.Err())
			}
			if kind == "cancel_later" {
				cancel()
			} else {
				enginetest.RoleExecute(t, e, admin, "REVOKE dependency_group FROM dependency_actor")
			}
			if _, err := e.Rollback(holder, tx); err != nil {
				t.Fatal(err)
			}
			var out outcome
			select {
			case out = <-results:
				joined = true
			case <-time.After(5 * time.Second):
				t.Fatal("dependency importer did not join")
			}
			operationErr = out.err
			if out.source != nil {
				defer out.source.Close()
			}
			expected := ids.ErrCatalogReplaced
			if kind == "cancel_later" {
				expected = context.Canceled
			}
			if !errors.Is(out.err, expected) || out.source != nil {
				t.Fatal("earlier stale selection reached the executor", kind, out.err, out.source)
			}
			if core.ScopeRLSAdmissionDepth(scope) != 0 || len(core.ScopeRLSActive(scope)) != 0 {
				t.Fatal("nested admission leaked depth/active state", core.ScopeRLSAdmissionDepth(scope), core.ScopeRLSActive(scope))
			}
			if len(scope.Stage.Rows) != 0 || len(scope.Stage.Deletes) != 0 {
				t.Fatal("failed final fence published row work")
			}
		})
	}
}

// The selected PUBLIC policy stays identical. Only the original command's
// inherited SELECT grant changes while its policy helper admission is parked.
func TestRLSPolicyOriginalAuthority(t *testing.T) {
	for _, retained := range []bool{false, true} {
		t.Run(map[bool]string{false: "revoked", true: "retained"}[retained], func(t *testing.T) {
			e, op, done, _ := policyUnitFixture(t)
			defer done()
			for _, sql := range []string{
				"CREATE ROLE acl_actor", "CREATE ROLE acl_group",
				"GRANT acl_group TO acl_actor WITH INHERIT TRUE",
				"GRANT USAGE ON SCHEMA public TO acl_actor",
				"GRANT SELECT ON t TO acl_group", "GRANT SELECT ON helper TO acl_actor",
				"INSERT INTO t VALUES(1,TRUE)", "INSERT INTO helper VALUES(1)",
				"CREATE POLICY p ON t USING(EXISTS(SELECT 1 FROM helper WHERE helper.id=t.id))",
				"ALTER TABLE t ENABLE ROW LEVEL SECURITY",
			} {
				enginetest.RoleExecute(t, e, op, sql)
			}
			if retained {
				enginetest.RoleExecute(t, e, op, "GRANT SELECT ON t TO acl_actor")
			}
			enginetest.RoleExecute(t, e, op, "SET ROLE acl_actor")
			admin, closeAdmin := enginetest.RoleOperation(t, e, "postgres")
			defer closeAdmin()
			holder, closeHolder := enginetest.RoleOperation(t, e, "postgres")
			defer closeHolder()
			tx, err := e.Begin(holder, session.TxOptions{})
			if err != nil {
				t.Fatal(err)
			}
			defer e.Rollback(holder, tx)
			resource := lock.Resource{ObjectRef: roleTableRef(t, e, "helper")}
			ticket := holder.Session.Transactions.Ticket
			request, err := e.Locks.StartAcquire(holder.Context(), ticket, resource, lock.AccessExclusive, lock.Policy{})
			if err == nil && request != nil {
				err = request.Wait()
			}
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(op.Ctx, 5*time.Second)
			defer cancel()
			op.Ctx = ctx
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			var operationErr error
			defer func() {
				err := scope.Finish(operationErr)
				if operationErr == nil && err != nil {
					t.Error(err)
				}
			}()
			type outcome struct {
				result core.Result
				err    error
			}
			results := make(chan outcome, 1)
			joined := false
			defer func() {
				if !joined {
					cancel()
					e.Rollback(holder, tx)
					select {
					case out := <-results:
						if out.result.Rows != nil {
							out.result.Rows.Close()
						}
					case <-time.After(5 * time.Second):
						t.Error("original authority worker failed to join")
					}
				}
			}()
			go func() {
				result, err := scope.Execute(enginetest.Statement(t, "SELECT id FROM t"), 0, nil)
				results <- outcome{result, err}
			}()
			waiting := false
			for ctx.Err() == nil {
				select {
				case out := <-results:
					joined = true
					operationErr = out.err
					if out.result.Rows != nil {
						out.result.Rows.Close()
					}
					t.Fatalf("executor exited before policy helper wait: %v", out.err)
				default:
				}
				state := e.Locks.Inspect(ticket)
				if entry := state.Resources[resource]; entry != nil {
					for _, waiter := range entry.Pending {
						if !waiter.Terminal && waiter.Mode == lock.AccessShare {
							waiting = true
							break
						}
					}
				}
				if waiting {
					break
				}
				runtime.Gosched()
			}
			if !waiting {
				t.Fatal("missing original command policy wait", ctx.Err())
			}
			enginetest.RoleExecute(t, e, admin, "REVOKE acl_group FROM acl_actor")
			if _, err := e.Rollback(holder, tx); err != nil {
				t.Fatal(err)
			}
			var out outcome
			select {
			case out = <-results:
				joined = true
			case <-time.After(5 * time.Second):
				t.Fatal("original authority worker did not join")
			}
			operationErr = out.err
			if out.result.Rows != nil {
				defer out.result.Rows.Close()
			}
			if retained {
				if out.err != nil {
					t.Fatal("unchanged selected program/current grant rejected", out.err)
				}
			} else {
				var denied *sqlstate.Error
				if !errors.As(out.err, &denied) || denied.Code != "42501" || denied.Message != "permission denied for table t" {
					t.Fatal("original ACL was not freshly enforced", out.err)
				}
				if out.result.Rows != nil {
					t.Fatal("revoked original grant published rows")
				}
			}
			if core.ScopeRLSImportWaits(scope) == 0 || core.ScopeRLSAdmissionDepth(scope) != 0 || core.ScopeRLSCommand(scope) != nil {
				t.Fatal("policy wait or lexical command authority leaked", core.ScopeRLSImportWaits(scope), core.ScopeRLSAdmissionDepth(scope))
			}
		})
	}
}

func TestRLSAuthorityFrame(t *testing.T) {
	e, op, done, _ := policyUnitFixture(t)
	defer done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	defer scope.Finish(nil)
	outer := core.NewPreparedCommandExecution(core.PreparedCommandExecutionFields{PolicyWaits: 1})
	inner := core.NewPreparedCommandExecution(core.PreparedCommandExecutionFields{PolicyWaits: 2})
	*core.ScopeRLSCommandRef(scope), *core.ScopeRLSImportWaitsRef(scope) = outer, 1
	rule := scope.RuleScratch()
	core.SetScopeRLSCommand(scope, inner)
	*core.ScopeRLSImportWaitsRef(scope)++
	scope.InstallRuleScratch(rule)
	if core.ScopeRLSCommand(scope) != outer || core.ScopeRLSImportWaits(scope) != 2 || core.PreparedCommandExecutionPolicyWaits(outer) != 1 {
		t.Fatal("rule frame lost pending original authority")
	}
	targetRef := roleTableRef(t, e, "t")
	core.SetScopeRLSDataRefs(scope, []ids.ObjectRef{targetRef})
	routine := core.ScopeSQLRoutineScratch(scope)
	core.SetScopeRLSCommand(scope, inner)
	*core.ScopeRLSImportWaitsRef(scope)++
	core.SetScopeRLSDataRefs(scope, nil)
	core.ScopeInstallSQLRoutineScratch(scope, routine)
	if core.ScopeRLSCommand(scope) != outer || core.ScopeRLSImportWaits(scope) != 3 || core.PreparedCommandExecutionPolicyWaits(outer) != 1 || len(core.ScopeRLSDataRefs(scope)) != 1 || core.ScopeRLSDataRefs(scope)[0] != targetRef {
		t.Fatal("routine frame lost pending original authority")
	}
	core.SetScopeRLSCommand(scope, nil)
}
