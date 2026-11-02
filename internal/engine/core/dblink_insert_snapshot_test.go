package core_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Observe real source-view ownership at existing context polls. Cancellation is
// selected only after the nested view exists, never by elapsed time or fake rows.
type dblinkInsertSnapshotContext struct {
	context.Context
	scope  *core.StatementScope
	outer  *visibility.View
	locals map[*visibility.View]*visibility.Data
	cancel context.CancelFunc
}

func (c *dblinkInsertSnapshotContext) Err() error {
	if local := c.scope.View; local != nil && local != c.outer {
		if data := local.Load(); data != nil {
			c.locals[local] = data
			if c.cancel != nil {
				c.cancel()
			}
		}
	}
	return c.Context.Err()
}

func TestDblinkInsertNestedSnapshot(t *testing.T) {
	for _, isolation := range []session.Isolation{session.ReadCommitted, session.RepeatableRead, session.Serializable} {
		for _, phase := range []string{"success", "error", "cancel"} {
			t.Run(fmt.Sprintf("%d/%s", isolation, phase), func(t *testing.T) {
				e := engine.New()
				op, done := enginetest.RoleOperation(t, e, "postgres")
				for _, q := range []string{
					"CREATE EXTENSION dblink",
					"CREATE TABLE insert_owner_outer(id integer)",
					"CREATE TABLE insert_owner_source(id integer,payload text)",
					"INSERT INTO insert_owner_source VALUES(1,'committed')",
					"CREATE FUNCTION insert_owner_stable() RETURNS text LANGUAGE SQL STABLE AS 'SELECT NULL::text'",
					"CREATE VIEW insert_owner_error AS SELECT id,(1/(id-1))::text AS payload FROM insert_owner_source",
				} {
					enginetest.RoleExecute(t, e, op, q)
				}
				done()
				reader, _ := blockingUnitOwner(t, e)
				isolationSQL := "READ COMMITTED"
				switch isolation {
				case session.RepeatableRead:
					isolationSQL = "REPEATABLE READ"
				case session.Serializable:
					isolationSQL = "SERIALIZABLE"
				}
				// Admit isolation before the first snapshot-requiring command;
				// changing only Owner.Isolation after an RC write splits its pair.
				enginetest.SessionSQL(t, reader, "SET TRANSACTION ISOLATION LEVEL "+isolationSQL)
				enginetest.SessionSQL(t, reader, "INSERT INTO insert_owner_source VALUES(2,'private')")
				scope := pgrowlocksUnitScope(t, e, reader)
				view := e.CatalogRead()
				namespace := view.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "public"})
				ref := func(name string) ids.ObjectRef {
					return e.Catalog.Domain.Ref(view.Epoch(), view.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: namespace, Name: name}))
				}
				if err := core.ScopeSnapshotDataPhase(scope); err != nil {
					t.Fatal(err)
				}
				scope.Owner.Mu.Lock()
				e.Mu.Lock()
				outer, err := core.ScopeAcquireDataViewLocked(scope, e, ref("insert_owner_outer"))
				auth, authErr := core.ScopeWriteAuthorizationLocked(scope, e)
				e.Mu.Unlock()
				scope.Owner.Mu.Unlock()
				if err != nil || authErr != nil {
					t.Fatal(err, authErr)
				}
				scope.RoutineAuth = auth
				base := outer.Load()
				stable := view.MustProcedure(view.NameAddress(ids.NameKey{ClassID: ids.ProcedureClassID, Namespace: namespace, Name: "insert_owner_stable", Signature: ""}))
				restoreStable := core.ActivateRoutineCommandPolicy(scope, stable, false)
				defer restoreStable()
				frontier, snapshot := core.ScopeSnapshotFrontier(scope), core.ScopeCurrentSnapshot(scope)
				demand := core.ScopeSQLRoutine(scope)
				writes, deletes := reflect.ValueOf(scope.OwnWrites).UnsafePointer(), reflect.ValueOf(scope.OwnDeletes).UnsafePointer()
				rls := reflect.ValueOf(core.ScopeSQLRoutineScratch(scope)).FieldByName("rlsDataRefs").Pointer()
				e.Mu.Lock()
				e.Visibility.AdvanceCommitSeqLocked()
				e.Mu.Unlock()
				source := "insert_owner_source"
				if phase == "error" {
					source = "insert_owner_error"
				}
				binder := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
				raw := enginetest.Statement(t, "SELECT dblink_build_sql_insert('"+source+"','1'::int2vector,1,'{1}'::text[],'{9}'::text[])").(*parser.Select).Targets[0].(parser.FunctionExpr)
				bound, err := binder.BindFunctionProvider(raw)
				if err != nil {
					t.Fatal(err)
				}
				n := bound.(plan.InstalledDblinkBuildSQLInsertExpr)
				scope.RoutineDependencies = binder.Dependencies
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				observed := &dblinkInsertSnapshotContext{Context: ctx, scope: scope, outer: outer, locals: make(map[*visibility.View]*visibility.Data)}
				if phase == "cancel" {
					observed.cancel = cancel
				}
				scope.Op.Ctx = observed
				evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: observed, Host: core.HostOf(scope), Catalog: auth.Catalog, Input: scope.Input, Output: scope.Output}}
				value, err := n.Eval(&evaluation)
				switch phase {
				case "success":
					text, textErr := value.Text()
					if err != nil || textErr != nil || text != "INSERT INTO insert_owner_source(id,payload) VALUES('9','committed')" {
						t.Fatal(text, textErr, err)
					}
				case "error":
					enginetest.State(t, err, "22012")
				case "cancel":
					if !errors.Is(err, context.Canceled) {
						t.Fatal("nested cancellation", err)
					}
				}
				if len(observed.locals) == 0 {
					t.Fatal("did not observe a real nested source view")
				}
				for local, data := range observed.locals {
					if local.Load() != nil || local == outer {
						t.Fatal("source view leaked or borrowed the stable caller")
					}
					if isolation.Stable() && data.Frontier != frontier || !isolation.Stable() && data.Frontier <= frontier {
						t.Fatal("wrong source command frontier", data.Frontier, frontier)
					}
					if _, admitted := data.Committed[ref("insert_owner_source").Address]; !admitted {
						t.Fatal("source view did not admit actual SQL source")
					}
				}
				if scope.View != outer || outer.Load() != base || core.ScopeSnapshotFrontier(scope) != frontier || core.ScopeCurrentSnapshot(scope) != snapshot || !core.SameSQLRoutineDemand(demand, core.ScopeSQLRoutine(scope)) || core.ScopeActiveSQLRoutineDepth(scope) != 0 {
					t.Fatal("nested source did not restore exact outer policy/snapshot/view")
				}
				if reflect.ValueOf(scope.OwnWrites).UnsafePointer() != writes || reflect.ValueOf(scope.OwnDeletes).UnsafePointer() != deletes || reflect.ValueOf(core.ScopeSQLRoutineScratch(scope)).FieldByName("rlsDataRefs").Pointer() != rls {
					t.Fatal("nested source replaced outer own-write or RLS frontier")
				}
			})
		}
	}
}
