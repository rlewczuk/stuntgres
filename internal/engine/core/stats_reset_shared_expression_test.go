package core_test

import (
	"context"
	"reflect"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/statistics"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestStatsResetSharedExpressionOwnership(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	for _, tc := range []struct {
		query  string
		kind   functions.StatsResetSharedKind
		oid    uint32
		strict expr.Strict
	}{
		{"SELECT pg_stat_reset_shared()", functions.ResetShared, 3775, expr.StrictNone},
		{"SELECT pg_stat_reset_slru(target => NULL)", functions.ResetSLRU, 2307, expr.StrictNone},
		{"SELECT pg_stat_reset_backend_stats(0)", functions.ResetBackendStats, 6387, expr.StrictAfter},
	} {
		op, err := s.Acquire(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		query := enginetest.BindSelect(t, e, op, tc.query)
		project, ok := query.Root.(*plan.ProjectNode)
		if !ok || len(project.Expressions) != 1 {
			op.Done()
			t.Fatal("typed projection")
		}
		node, ok := project.Expressions[0].(functions.StatsResetShared)
		if !ok || node.Validate() != nil || node.Kind != tc.kind || node.Argument.ResultUsage().Type.ID() != tc.kind.ArgumentType() || node.ResultUsage().Type.ID() != sqltype.Void || node.RoutineReference == nil || uint32(node.RoutineReference.RoutineOID()) != tc.oid || node.StrictNull() != tc.strict || node.IntrinsicVolatility() != expr.Volatile || node.PlanningFold() != expr.FoldNever || node.PreflightFold() != expr.PreflightKeep {
			op.Done()
			t.Fatal("typed declaration ownership", node)
		}
		original := node.Argument
		children := node.Children()
		children[0] = nil
		if !reflect.DeepEqual(node.Argument, original) {
			t.Fatal("borrowed child backing")
		}
		rebuilt, err := node.Rebuild(node.Info, []expr.Expr{original})
		if err != nil || !node.SameGroup(rebuilt, func(a, b expr.Expr) bool { return reflect.DeepEqual(a, b) }, false) {
			t.Fatal("immutable reconstruction", err)
		}
		if _, err := node.Rebuild(node.Info, nil); err == nil {
			t.Fatal("invalid reconstruction")
		}
		rendering, err := node.Render()
		if err != nil || rendering.Name != node.Kind.Name() || len(rendering.Args) != 1 {
			t.Fatal("render", err)
		}
		capture, accepted, err := node.Capture(expr.CaptureFunction)
		if err != nil || !accepted || capture.Count != 1 || !capture.Indexed {
			t.Fatal("capture", err)
		}
		child, err := node.CaptureOperand(0)
		if err != nil || !reflect.DeepEqual(child, original) {
			t.Fatal("capture operand", err)
		}
		if node.ValidateStoredProvider() != nil {
			t.Fatal("stored validation")
		}
		op.Done()
	}
	statsFunctionExec(t, s, "CREATE TABLE shared_expression_control(v integer)")
	key := statistics.Key{Object: enginetest.RoleTableRef(t, e, "shared_expression_control"), Category: statistics.IO}
	sharedStatsSeed(e, key)
	before := e.Statistics.Read(key)
	ast, d := enginetest.PreparedDescription(t, e, s, "SELECT pg_stat_reset_shared($1::text)")
	if e.Statistics.Read(key) != before {
		t.Fatal("Prepare executed reset")
	}
	found := false
	for _, dep := range d.Analysis.Dependencies {
		if dep.Ref.Address.ObjectID == 3775 {
			found = true
		}
	}
	if !found {
		t.Fatal("routine dependency missing")
	}
	statsFunctionExec(t, s, "CREATE VIEW shared_expression_view AS SELECT pg_stat_reset_shared('io') IS NULL AS v")
	read := e.CatalogRead()
	ref := enginetest.CatalogRef(t, e, read, "shared_expression_view")
	definition := stored.ViewOf(read.MustRelation(ref.Address))
	root := definition.Query().Root.(*plan.ProjectNode)
	backing := &root.Expressions[0]
	revision := definition.DefinitionRevision()
	expression := root.Expressions[0]
	typed := false
	plan.WalkExpression(expression, func(child expr.Expr) {
		if node, ok := child.(functions.StatsResetShared); ok {
			typed = true
			if node.ValidateStoredProvider() != nil || node.RoutineReference == nil || node.RoutineReference.RoutineOID() != 3775 {
				t.Fatal("stored provider ownership")
			}
		}
	})
	if !typed {
		t.Fatal("stored typed expression absent")
	}
	_, viewDescription := enginetest.PreparedDescription(t, e, s, "SELECT v FROM shared_expression_view")
	defer e.RetirePreparedDescription(&viewDescription)
	if stored.ViewOf(e.CatalogRead().MustRelation(ref.Address)) != definition || definition.Query().Root != root || &root.Expressions[0] != backing || definition.DefinitionRevision() != revision || !reflect.DeepEqual(root.Expressions[0], expression) || e.Statistics.Read(key) != before {
		t.Fatal("stored Prepare mutated backing/state")
	}
	arg := "io"
	statsFunctionExec(t, s, "CREATE ROLE shared_expression_reader", "SET ROLE shared_expression_reader")
	if err := resetStatsPrepared(e, s, ast, &d, &arg); err == nil || e.Statistics.Read(key) != before {
		t.Fatal("held statement retained privilege", err)
	}
	statsFunctionExec(t, s, "RESET ROLE", "GRANT EXECUTE ON FUNCTION pg_stat_reset_shared(text) TO shared_expression_reader", "SET ROLE shared_expression_reader")
	if err := resetStatsPrepared(e, s, ast, &d, &arg); err != nil {
		t.Fatal(err)
	}
	statsFunctionExec(t, s, "RESET ROLE", "REVOKE EXECUTE ON FUNCTION pg_stat_reset_shared(text) FROM shared_expression_reader", "SET ROLE shared_expression_reader")
	after := e.Statistics.Read(key)
	if err := resetStatsPrepared(e, s, ast, &d, &arg); err == nil || e.Statistics.Read(key) != after {
		t.Fatal("revoked held execution", err)
	}
	statsFunctionExec(t, s, "RESET ROLE")
}
