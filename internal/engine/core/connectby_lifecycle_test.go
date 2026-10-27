package core_test

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Observe existing context polls and real view owners, never install an engine
// callback. The phase context below uses the existing stack-aware cancellation
// seam; its embedded real context alone owns Done and the terminal error.
type connectbyOwnershipContext struct {
	context.Context
	scope              *core.StatementScope
	outer              *visibility.View
	locals             map[*visibility.View]*visibility.Data
	policyMaterialized bool
}

func (c *connectbyOwnershipContext) Err() error {
	if local := c.scope.View; local != nil && local != c.outer {
		if data := local.Load(); data != nil {
			c.locals[local] = data
			var pcs [64]uintptr
			frames := runtime.CallersFrames(pcs[:runtime.Callers(2, pcs[:])])
			for {
				frame, more := frames.Next()
				if strings.HasSuffix(frame.Function, ".(*Engine).materializePolicySourcesLocked") {
					c.policyMaterialized = true
				}
				if !more {
					break
				}
			}
		}
	}
	return c.Context.Err()
}

// Phase evidence counts actual child-query entries, not general Err polls.
// The second connectbyQuery parse and connectbyStatement activation can only
// occur after emit has completed a non-root row. Cancellation then waits for
// the exact traversal frame, excluding Connectby's emit/query closures.
type connectbyPhaseContext struct {
	context.Context
	cancel                   context.CancelFunc
	sort, traversal, entered bool
	queries, admitted        int
}

func (c *connectbyPhaseContext) Err() error {
	if err := c.Context.Err(); err != nil {
		return err
	}
	var pcs [64]uintptr
	frames := runtime.CallersFrames(pcs[:runtime.Callers(2, pcs[:])])
	direct, caller := "", ""
	statement, sorting := false, false
	for i := 0; ; i++ {
		frame, more := frames.Next()
		if i == 0 {
			direct = frame.Function
		} else if i == 1 {
			caller = frame.Function
		}
		statement = statement || strings.HasSuffix(frame.Function, ".(*StatementScope).connectbyStatement")
		sorting = sorting || strings.HasSuffix(frame.Function, ".CompareQueryOrder")
		if !more {
			break
		}
	}
	if strings.HasSuffix(direct, "/parser.ParseWithOptions") && strings.HasSuffix(caller, ".(*StatementScope).connectbyQuery") {
		c.queries++
	}
	if statement {
		c.admitted = c.queries
	}
	if c.cancel != nil && (c.sort && sorting || c.traversal && c.admitted >= 2 && strings.HasSuffix(direct, ".(*statementHost).Connectby")) {
		c.entered = true
		c.cancel()
	}
	return c.Context.Err()
}

func connectbyLifecycleExpression(t *testing.T, e *core.Engine, order string) (plan.InstalledConnectbyExpr, []catalog.ExpressionDependency) {
	t.Helper()
	b := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	b.Clause.AggregateClause = "SELECT"
	raw := enginetest.Statement(t, "SELECT connectby('cb_source','k','p','"+order+"','root',0)").(*parser.Select).Targets[0].(parser.FunctionExpr)
	bound, err := b.BindFunctionProvider(raw)
	if err != nil {
		t.Fatal(err)
	}
	completed, err := b.CompleteFunctionDefinitions(raw, bound, []parser.FunctionColumnDefinition{
		{Name: parser.Identifier{Text: "node"}, Type: parser.TypeName{Name: "text"}},
		{Name: parser.Identifier{Text: "parent"}, Type: parser.TypeName{Name: "text"}},
		{Name: parser.Identifier{Text: "level"}, Type: parser.TypeName{Name: "integer"}},
		{Name: parser.Identifier{Text: "serial"}, Type: parser.TypeName{Name: "integer"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return completed.(plan.InstalledConnectbyExpr), b.Dependencies
}

func TestConnectbyAdmittedExecutionOwnership(t *testing.T) {
	for _, phase := range []string{"success", "nested_failure", "nested_sort_cancel", "accumulated_traversal_cancel"} {
		t.Run(phase, func(t *testing.T) {
			e := engine.New()
			op, done := enginetest.RoleOperation(t, e, "postgres")
			for _, sql := range []string{
				"CREATE EXTENSION tablefunc",
				"CREATE USER cb_lifecycle",
				"CREATE TABLE cb_target(v integer)",
				"CREATE TABLE cb_source(k text,p text,pos integer)",
				"CREATE TABLE cb_allow(k text)",
				"CREATE TABLE cb_gate(k text)",
				"INSERT INTO cb_source VALUES('a','root',3),('b','root',2),('c','root',1),('aa','a',1)",
				"INSERT INTO cb_allow VALUES('a'),('b'),('c'),('aa'),('private')",
				"INSERT INTO cb_gate VALUES('a'),('b'),('c'),('aa'),('private')",
				"GRANT SELECT ON cb_source,cb_allow,cb_gate TO cb_lifecycle",
				"GRANT INSERT,SELECT ON cb_target TO cb_lifecycle",
				"ALTER TABLE cb_source ENABLE ROW LEVEL SECURITY",
				"ALTER TABLE cb_allow ENABLE ROW LEVEL SECURITY",
				"CREATE POLICY cb_gate_policy ON cb_allow TO cb_lifecycle USING(EXISTS(SELECT 1 FROM cb_gate WHERE cb_gate.k=cb_allow.k))",
				"CREATE POLICY cb_source_policy ON cb_source TO cb_lifecycle USING(EXISTS(SELECT 1 FROM cb_allow WHERE cb_allow.k=cb_source.k))",
			} {
				enginetest.RoleExecute(t, e, op, sql)
			}
			done()
			order := "pos"
			if phase == "nested_failure" {
				order = "k::integer"
			}
			n, dependencies := connectbyLifecycleExpression(t, e, order)
			reader, _ := blockingUnitOwner(t, e)
			sessionSQL(t, reader, "INSERT INTO cb_source VALUES('private','a',2)")
			sessionSQL(t, reader, "DELETE FROM cb_source WHERE k='b'")
			sessionSQL(t, reader, "SET SESSION AUTHORIZATION cb_lifecycle")
			scope := pgrowlocksUnitScope(t, e, reader)
			scope.RoutineDependencies = dependencies
			core.SetRoutineDemandPLDiagnostic(core.ScopeSQLRoutineRef(scope), "outer demand")
			demand := core.ScopeSQLRoutine(scope)
			writes, deletes := maps.Clone(scope.OwnWrites), maps.Clone(scope.OwnDeletes)
			if len(writes) == 0 || len(deletes) == 0 {
				t.Fatal("missing transaction-private writes/deletes")
			}
			if err := core.ScopeSnapshotDataPhase(scope); err != nil {
				t.Fatal(err)
			}
			ref := func(name string) ids.ObjectRef {
				address := e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "public"}), Name: name})
				return e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), address)
			}
			target := ref("cb_target")
			scope.Owner.Mu.Lock()
			e.Mu.Lock()
			outer, err := core.ScopeAcquireDataViewLocked(scope, e, target)
			auth, authErr := core.ScopeWriteAuthorizationLocked(scope, e)
			e.Mu.Unlock()
			scope.Owner.Mu.Unlock()
			if err != nil || authErr != nil {
				t.Fatal(err, authErr)
			}
			scope.RoutineAuth = auth
			base := outer.Load()
			if len(base.Committed) != 1 {
				t.Fatal("outer capture is not target-only", base.Committed)
			}
			for _, name := range []string{"cb_source", "cb_allow", "cb_gate"} {
				if _, ok := base.DynamicRoots[ref(name).Address]; !ok {
					t.Fatal("missing retained dynamic root", name)
				}
			}
			frontier := core.ScopeSnapshotFrontier(scope)
			members := maps.Clone(base.Committed)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			point := &connectbyPhaseContext{Context: ctx}
			switch phase {
			case "nested_sort_cancel":
				point.sort, point.cancel = true, cancel
			case "accumulated_traversal_cancel":
				point.traversal, point.cancel = true, cancel
			}
			observed := &connectbyOwnershipContext{Context: ctx, scope: scope, outer: outer, locals: make(map[*visibility.View]*visibility.Data)}
			point.Context = observed
			scope.Op.Ctx = point
			evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: point, Host: core.HostOf(scope), Catalog: auth.Catalog, Input: scope.Input, Output: scope.Output}}
			cursor, err := n.OpenSource(&evaluation)
			if strings.HasSuffix(phase, "_cancel") {
				if !point.entered || !errors.Is(err, context.Canceled) {
					t.Fatal("admitted phase not canceled", point.entered, err)
				}
				var diagnostic interface{ SQLState() string }
				if errors.As(err, &diagnostic) {
					t.Fatal("context error became SQL diagnostic", err)
				}
			} else if phase == "nested_failure" {
				enginetest.State(t, err, "22P02")
			} else if err != nil {
				t.Fatal(err)
			}
			if phase == "accumulated_traversal_cancel" && (point.queries < 2 || point.admitted < 2 || len(observed.locals) < 2) {
				t.Fatal("missing independent post-nonroot child-query activation", point.queries, point.admitted, len(observed.locals))
			}
			if phase != "success" && (cursor.Advance != nil || cursor.Values != nil || cursor.State != nil) {
				t.Fatal("failed demand published a partial source", cursor)
			}
			cursor.Close()
			if len(observed.locals) == 0 || !observed.policyMaterialized {
				t.Fatal("real recursive policy materialization was not observed")
			}
			for local, data := range observed.locals {
				if local.Load() != nil || data.Frontier != base.Frontier {
					t.Fatal("local lifetime/frontier changed")
				}
				source := ref("cb_source").Address
				if len(data.Own[source]) != 1 || data.Deleted[source] == nil {
					t.Fatal("local capture lost private insertion or deletion")
				}
				for _, name := range []string{"cb_source", "cb_allow", "cb_gate"} {
					if _, ok := data.Committed[ref(name).Address]; !ok {
						t.Fatal("incomplete recursive RLS closure", name)
					}
				}
			}
			if scope.View != outer || outer.Load() != base || core.ScopeSnapshotFrontier(scope) != frontier || !reflect.DeepEqual(base.Committed, members) || len(base.Committed) != 1 {
				t.Fatal("nested demand mutated outer ownership")
			}
			if !core.SameSQLRoutineDemand(demand, core.ScopeSQLRoutine(scope)) || core.ScopeActiveSQLRoutineDepth(scope) != 0 {
				t.Fatal("invocation scratch or depth leaked")
			}
			if !reflect.DeepEqual(scope.OwnWrites, writes) || !reflect.DeepEqual(scope.OwnDeletes, deletes) {
				t.Fatal("restoration lost private state")
			}
			// A fresh ordinary context on the same live owner can demand again.
			scope.Op.Ctx = context.Background()
			good, _ := connectbyLifecycleExpression(t, e, "pos")
			evaluation.Context = context.Background()
			next, nextErr := good.OpenSource(&evaluation)
			if nextErr != nil || len(next.Values) != 5 {
				t.Fatal("subsequent demand", nextErr, len(next.Values))
			}
			next.Close()
			if scope.View != outer || outer.Load() != base || !core.SameSQLRoutineDemand(demand, core.ScopeSQLRoutine(scope)) {
				t.Fatal("subsequent demand lost outer owner")
			}
		})
	}
}

// The public operation wrapper intentionally owns a derived cancellation
// context. Install the existing phase seam on the admitted operation itself,
// then execute the real DML owner and inspect publication, not SQL row echoes.
func TestConnectbyAdmittedDMLCancellation(t *testing.T) {
	for _, traversal := range []bool{false, true} {
		name := "nested_sort"
		if traversal {
			name = "accumulated_traversal"
		}
		t.Run(name, func(t *testing.T) {
			e := engine.New()
			setup, done := enginetest.RoleOperation(t, e, "postgres")
			for _, sql := range []string{
				"CREATE EXTENSION tablefunc",
				"CREATE TABLE cb_source(k text,p text,pos integer)",
				"INSERT INTO cb_source VALUES('a','root',1),('b','root',2),('aa','a',1)",
				"CREATE TABLE cb_target(v integer)",
				"INSERT INTO cb_target VALUES(99)",
			} {
				enginetest.RoleExecute(t, e, setup, sql)
			}
			done()
			namespace := e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "public"})
			address := e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: namespace, Name: "cb_target"})
			before := e.Storage.Relations[address]
			versions := append(before.Versions[:0:0], before.Versions...)
			ends := before.Ends
			op, cleanup, err := enginetest.Operation(e, context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()
			original := op.Context()
			ctx, cancel := context.WithCancel(original)
			defer cancel()
			point := &connectbyPhaseContext{Context: ctx, sort: !traversal, traversal: traversal, cancel: cancel}
			op.Ctx = point
			statement := enginetest.Statement(t, "INSERT INTO cb_target SELECT level FROM connectby('cb_source','k','p','pos','root',0) AS t(node text,parent text,level integer,serial integer)")
			result, err := e.Execute(op, statement, 0, nil)
			if !point.entered || !errors.Is(err, context.Canceled) {
				t.Fatal("real INSERT phase not canceled", point.entered, err)
			}
			if traversal && (point.queries < 2 || point.admitted < 2) {
				t.Fatal("INSERT canceled before post-nonroot child-query activation", point.queries, point.admitted)
			}
			var diagnostic interface{ SQLState() string }
			if errors.As(err, &diagnostic) {
				t.Fatal("ordinary context error became SQL diagnostic", err)
			}
			if result.Count != 0 || result.Rows != nil || len(result.RowSets) != 0 {
				t.Fatal("canceled DML published output", result)
			}
			if e.Storage.Relations[address] != before || before.Ends != ends || !reflect.DeepEqual(before.Versions, versions) {
				t.Fatal("canceled DML changed the target root")
			}
			// Canceling this child context did not cancel the operation's
			// source context or session. The same owner can now publish DML.
			op.Ctx = original
			next, nextErr := e.Execute(op, statement, 0, nil)
			if nextErr != nil || next.Count != 4 || next.Rows != nil || len(next.RowSets) != 0 {
				t.Fatal("subsequent real DML demand", next, nextErr)
			}
			if len(e.Storage.Relations[address].Versions) != len(versions)+4 {
				t.Fatal("successful DML did not publish its complete target batch")
			}
		})
	}
}
