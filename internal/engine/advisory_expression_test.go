package engine

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestAdvisorySessionBindingTraversal(t *testing.T) {
	ctx := context.Background()
	e := New()
	names := []string{"pg_advisory_lock", "pg_advisory_lock_shared", "pg_try_advisory_lock", "pg_try_advisory_lock_shared", "pg_advisory_unlock", "pg_advisory_unlock_shared", "pg_advisory_unlock_all"}
	for _, name := range names {
		for _, arguments := range []string{"1", "1,2"} {
			if name == "pg_advisory_unlock_all" {
				if arguments == "1,2" {
					continue
				}
				arguments = ""
			}
			t.Run(name+"/"+arguments, func(t *testing.T) {
				b := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
				raw := statement(t, "SELECT "+name+"("+arguments+")").(*parser.Select).Targets[0]
				bound, err := b.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
				if err != nil {
					t.Fatal(err)
				}
				node, ok := bound.(functions.Advisory)
				if !ok {
					t.Fatalf("wrong producer %T", bound)
				}
				id := sqltype.Bool
				if name == "pg_advisory_lock" || name == "pg_advisory_lock_shared" || name == "pg_advisory_unlock_all" {
					id = sqltype.Void
				}
				if node.ResultUsage().Type.ID() != id || node.Properties().Volatility != expr.Volatile || plan.GenerationImmutable(node) || !pgIndexFunctionExpression(node) {
					t.Fatal("fixed identity/volatility", node.ResultUsage(), node.Properties())
				}
				if !node.ValidStored() {
					t.Fatal("bound producer lost stored signature")
				}
				if err = b.CaptureViewLiterals(raw, node, analyze.ViewTargetLiteral, 0); err != nil || len(b.Capture.Literals) != len(node.Args) {
					t.Fatal("view literal children", len(b.Capture.Literals), err)
				}
				text, err := (pgRenderer{View: e.CatalogRead()}).SQL(ctx, node, pgRenderParent{})
				if err != nil || !strings.Contains(text, name+"(") {
					t.Fatal("symbolic SQL", text, err)
				}
				var tree strings.Builder
				if err = plan.AppendBoundTree(ctx, &tree, node); err != nil || !strings.Contains(tree.String(), "AdvisorySession "+name) {
					t.Fatal("symbolic tree", tree.String(), err)
				}
				reparsed := statement(t, "SELECT "+text).(*parser.Select).Targets[0]
				rebound, err := b.Bind(reparsed, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
				if err != nil || rebound.ResultUsage() != node.ResultUsage() || rebound.Properties().Volatility != expr.Volatile {
					t.Fatal("deparse rebinding", rebound, err)
				}
				lockState := e.locks.Inspect()
				effects := len(lockState.Resources)
				if effects != 0 {
					t.Fatal("binding/deparse acquired a hold", effects)
				}
			})
		}
	}
	t.Run("inference widening and diagnostics", func(t *testing.T) {
		for _, query := range []string{"SELECT pg_advisory_lock(1::smallint)", "SELECT pg_advisory_lock(1::integer)", "SELECT pg_advisory_lock('1')", "SELECT pg_advisory_lock($1)", "SELECT pg_advisory_lock(1::smallint,2)", "SELECT pg_advisory_lock('1','2')", "SELECT pg_advisory_lock($1,$2)"} {
			b := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
			bound, err := b.Bind(statement(t, query).(*parser.Select).Targets[0], analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
			if err != nil {
				t.Fatal(query, err)
			}
			node := bound.(functions.Advisory)
			id := sqltype.Int8
			if len(node.Args) == 2 {
				id = sqltype.Int4
			}
			for _, child := range node.Args {
				if child.ResultUsage().Type.ID() != id {
					t.Fatal("signature inference", query, child.ResultUsage())
				}
			}
			if strings.Contains(query, "$") && !node.Properties().ParameterDependent {
				t.Fatal("parameter descendant lost", query)
			}
		}
		castHint := "No function matches the given name and argument types. You might need to add explicit type casts."
		for _, tc := range []struct{ query, code, message, hint string }{
			{"SELECT pg_advisory_lock(1::bigint,2)", "42883", "function pg_advisory_lock(bigint, integer) does not exist", castHint},
			{"SELECT pg_advisory_lock(key => 1)", "42883", "function pg_advisory_lock(key => integer) does not exist", castHint},
			{"SELECT pg_advisory_lock(VARIADIC '{1}'::integer[])", "42883", "function pg_advisory_lock(integer[]) does not exist", castHint},
			{"SELECT pg_advisory_lock(" + strings.Repeat("1,", 100) + "1)", "54023", "cannot pass more than 100 arguments to a function", ""},
		} {
			b := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
			_, err := b.Bind(statement(t, tc.query).(*parser.Select).Targets[0], analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
			nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: tc.code, Message: tc.message, Hint: tc.hint, Position: "8"})
		}
		query := "SELECT pg_advisory_lock(" + strings.Repeat("1,", 100) + "missing_advisory_child)"
		b := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
		_, err := b.Bind(statement(t, query).(*parser.Select).Targets[0], analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
		var d *sqlstate.Error
		if !errors.As(err, &d) || d.Code != "42703" || d.Message != "column \"missing_advisory_child\" does not exist" {
			t.Fatal("child error must precede arity limit", err)
		}
	})
	t.Run("strict folding and runtime sibling demand", func(t *testing.T) {
		b := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
		raw := statement(t, "SELECT pg_advisory_lock(NULL,CASE WHEN pg_try_advisory_lock(12) THEN 1 ELSE 0 END)").(*parser.Select).Targets[0]
		bound, err := b.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
		if err != nil {
			t.Fatal(err)
		}
		for _, preflight := range []bool{false, true} {
			var folded expr.Expr
			var changed bool
			if preflight {
				folded, changed, err = exec.PreflightCoercions(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}}, bound)
			} else {
				folded, changed, err = exec.SimplifyConditional(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}}, bound, false)
			}
			if err != nil || !changed || !plan.GenerationImmutable(folded) {
				t.Fatal("literal NULL fold", folded, changed, err)
			}
			v, err := folded.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}})
			if err != nil || !v.IsNull() || v.Type().ID() != sqltype.Void {
				t.Fatal("typed NULL VOID", v, err)
			}
		}
		_, err = b.Bind(statement(t, "SELECT pg_advisory_lock(NULL,'bad'::integer)").(*parser.Select).Targets[0], analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
		nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "22P02", Message: "invalid input syntax for type integer: \"bad\"", Position: "30"})
		// Literal casts cook during binding. Separately supply an actual deferred
		// coercion plan to exercise demanded simplification in both walkers.
		bound, err = b.Bind(statement(t, "SELECT pg_advisory_lock(NULL,1)").(*parser.Select).Targets[0], analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
		if err != nil {
			t.Fatal(err)
		}
		deferred := bound.(functions.Advisory)
		textUsage, integerUsage := b.Type(sqltype.Text).Usage(), b.Type(sqltype.Int4).Usage()
		coercionPlan, err := sqltype.PlanCoercion(textUsage, integerUsage, sqltype.Explicit)
		if err != nil {
			t.Fatal(err)
		}
		child := plan.LiteralExpr{Info: b.Info(textUsage, parser.Position{}, expr.Properties{Volatility: expr.Immutable}), Value: sqltype.String(textUsage.Type, "bad")}
		deferred.Args = append([]expr.Expr(nil), deferred.Args...)
		deferred.Args[1] = plan.CoercionExpr{Info: b.Info(integerUsage, parser.Position{}, expr.Properties{Volatility: expr.Immutable}), Child: child, Plan: coercionPlan, Mode: sqltype.Explicit}
		bound = deferred
		for _, preflight := range []bool{false, true} {
			if preflight {
				_, _, err = exec.PreflightCoercions(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}}, bound)
			} else {
				_, _, err = exec.SimplifyConditional(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}}, bound, false)
			}
			nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "22P02", Message: "invalid input syntax for type integer: \"bad\""})
		}
	})
	t.Run("lexical group aggregate remap and retained descriptors", func(t *testing.T) {
		b := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
		bound, err := b.Bind(statement(t, "SELECT pg_try_advisory_lock(1,2)").(*parser.Select).Targets[0], analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
		if err != nil {
			t.Fatal(err)
		}
		node := bound.(functions.Advisory)
		column := plan.ColumnExpr{Info: expr.Info{Usage: node.Args[0].ResultUsage(), Pos: parser.Position{Character: 99}, Property: expr.Properties{RowDependent: true}}, Scope: 1, Frame: b.FrameID(), Origin: ids.ObjectRef{Address: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 999, SubID: 1}}}
		node.Args = []expr.Expr{node.Args[0], column}
		node.Property.RowDependent = true
		visits := 0
		plan.WalkExpression(node, func(expr.Expr) { visits++ })
		if visits != 3 || analyze.FirstQueryBoundFrameVariable(node, b.FrameID()).Character != 99 {
			t.Fatal("lexical child/frame walk", visits)
		}
		grouped, err := b.RewriteGroup(node, []expr.Expr{column})
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := grouped.(functions.Advisory).Args[1].(plan.GroupExpr); !ok {
			t.Fatal("group descendant not rewritten")
		}
		if _, ok := node.Args[1].(plan.ColumnExpr); !ok {
			t.Fatal("group rewrite mutated shared source")
		}
		if !b.SameGroupExpression(node, node) {
			t.Fatal("group structural identity")
		}
		other := node
		other.Args = []expr.Expr{node.Args[1], node.Args[0]}
		if b.SameGroupExpression(node, other) {
			t.Fatal("group identity ignored argument order")
		}
		remapped, err := stored.RemapExpression(node, func(ref ids.ObjectRef) ids.ObjectRef { ref.Address.ObjectID++; return ref }, map[int32]int{1: 7})
		if err != nil {
			t.Fatal(err)
		}
		mappedColumn := remapped.(functions.Advisory).Args[1].(plan.ColumnExpr)
		if mappedColumn.Origin.Address.ObjectID != 1000 || mappedColumn.Slot != 7 || node.Args[1].(plan.ColumnExpr).Origin.Address.ObjectID != 999 {
			t.Fatal("view child identity/slot remap", mappedColumn)
		}
		types := formatUnitTypes()
		retained, changed, err := stored.RetagTypes(New().CatalogRead(), node, func(d *sqltype.Descriptor) *sqltype.Descriptor { return types[d.ID()] })
		if err != nil || !changed {
			t.Fatal(retained, changed, err)
		}
		fresh := retained.(functions.Advisory)
		if fresh.ResultUsage().Type != types[sqltype.Bool] || fresh.Args[1].ResultUsage().Type != types[sqltype.Int4] || fresh.Mode != node.Mode || fresh.Action != node.Action || node.ResultUsage().Type == fresh.ResultUsage().Type {
			t.Fatal("retained generation/compiled action")
		}
		aggregate := plan.AggregateExpr{Info: expr.Info{Usage: node.Args[0].ResultUsage(), Property: expr.Properties{StageDependent: true}}, Slot: 7}
		node.Args = []expr.Expr{node.Args[0], aggregate}
		if found := analyze.AggregateOccurrence(node); found == nil || found.Slot != 7 {
			t.Fatal("aggregate descendant lost")
		}
	})
	t.Run("stored consumer policy and signature validation", func(t *testing.T) {
		s := sessionStart(t, e, nil)
		sessionSQL(t, s, "CREATE TABLE advisory_render_owner(v INT)")
		view := e.CatalogRead()
		ref := catalogRef(t, e, view, "advisory_render_owner")
		owner := view.MustRelation(ref.Address)
		b := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
		raw := statement(t, "SELECT pg_try_advisory_lock(1,2)").(*parser.Select).Targets[0]
		bound, err := b.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
		if err != nil {
			t.Fatal(err)
		}
		node := bound.(functions.Advisory)
		text, err := (scalarRenderer{Owner: owner}).SQL(ctx, view, node)
		if err != nil || !strings.Contains(text, "pg_catalog.pg_try_advisory_lock(") {
			t.Fatal("stored CHECK/view symbolic rendering", text, err)
		}
		if _, err = stored.DefaultSQL(ctx, view, node); err == nil {
			t.Fatal("volatile default rendering widened")
		}
		if _, err = b.BindDefault(raw, catalog.Column{Name: "v", Usage: node.ResultUsage()}); err == nil {
			t.Fatal("DEFAULT binding widened")
		}
		damaged := node
		damaged.Action = functions.AdvisoryUnlock
		if damaged.ValidStored() {
			t.Fatal("inconsistent compiled action admitted")
		}
		if _, err = (scalarRenderer{Owner: owner}).SQL(ctx, view, damaged); err == nil {
			t.Fatal("malformed stored producer rendered")
		}
	})
	t.Run("nonnull VOID uses admitted scope", func(t *testing.T) {
		s := sessionStart(t, e, nil)
		op := transactionOperation(t, s)
		scope, err := e.StartStatement(op)
		if err != nil {
			t.Fatal(err)
		}
		rawStatement := statement(t, "SELECT pg_advisory_lock(42)")
		if err = scope.gate(rawStatement); err != nil {
			_ = scope.Finish(err)
			op.Done()
			t.Fatal(err)
		}
		b := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
		bound, err := b.Bind(rawStatement.(*parser.Select).Targets[0], analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
		if err != nil {
			_ = scope.Finish(err)
			op.Done()
			t.Fatal(err)
		}
		v, err := bound.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: op.Context(), Host: hostOf(scope)}})
		finish := scope.Finish(err)
		op.Done()
		if err != nil || finish != nil || v.IsNull() || v.Type().ID() != sqltype.Void || advisoryUnitHold(s, lock.Resource{Kind: lock.AdvisoryBigintResource, Low: 42}).Exclusive != 1 {
			t.Fatal("actual VOID grant", v, err, finish)
		}
		if err = e.locks.UnlockAdvisoryAll(s.Advisory.Ticket); err != nil {
			t.Fatal(err)
		}
	})
}

func TestAdvisorySessionCheckPreparationCache(t *testing.T) {
	ctx := context.Background()
	e := New()
	b := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
	bind := func(text string) expr.Expr {
		t.Helper()
		node, err := b.Bind(statement(t, "SELECT "+text).(*parser.Select).Targets[0], analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
		if err != nil {
			t.Fatal(err)
		}
		return node
	}
	trueNode, falseNode := bind("true"), bind("false")
	trueValue, err := trueNode.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}})
	if err != nil {
		t.Fatal(err)
	}
	falseValue, err := falseNode.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}})
	if err != nil {
		t.Fatal(err)
	}
	column := plan.ColumnExpr{Info: expr.Info{Usage: trueNode.ResultUsage(), Property: expr.Properties{RowDependent: true}}, Scope: 1, Frame: 1}
	stored := bind("pg_try_advisory_lock(NULL::integer,CASE WHEN pg_try_advisory_lock(3201) THEN 2 ELSE 3 END)")
	var before strings.Builder
	if err := plan.AppendBoundTree(ctx, &before, stored); err != nil {
		t.Fatal(err)
	}
	d := &catalog.Relation{Constraints: catalog.RelationConstraints{Check: []catalog.CheckConstraint{{Name: "a_row", Expression: column}, {Name: "b_null", Expression: stored}}}}
	oldRow := []sqltype.Value{falseValue}
	eval := &exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}, EvalFrame: exec.EvalFrame{Scope: 9, Frame: 7, Row: oldRow}}
	for i := 0; i < 2; i++ {
		failed, err := validateCheckRow(d, []sqltype.Value{trueValue}, eval)
		if err != nil || failed != nil {
			t.Fatal("prepared constant NULL must not demand a session", failed, err)
		}
	}
	cached, ok := eval.PreparedChecks[d]
	if !ok || len(cached) != 2 || !reflect.DeepEqual(cached[0], column) {
		t.Fatal("unchanged CHECK references or descriptor cache lost", cached, ok)
	}
	if _, ok := cached[1].(plan.LiteralExpr); !ok {
		t.Fatal("constant-NULL executable CHECK was not detached", cached[1])
	}
	first := &cached[0]
	failed, err := validateCheckRow(d, []sqltype.Value{falseValue}, eval)
	if err != nil || failed != &d.Constraints.Check[0] || &eval.PreparedChecks[d][0] != first {
		t.Fatal("row changes must reuse preparation and return original constraint", failed, err)
	}
	if eval.Scope != 9 || eval.Frame != 7 || &eval.Row[0] != &oldRow[0] {
		t.Fatal("CHECK evaluator leaked its row frame")
	}
	var after strings.Builder
	if err := plan.AppendBoundTree(ctx, &after, d.Constraints.Check[1].Expression); err != nil || after.String() != before.String() {
		t.Fatal("execution preparation changed stored analyzed syntax", err, before.String(), after.String())
	}
	// Descriptor identity, not a relation name/address, bounds cache reuse;
	// fresh command contexts never inherit a previous command's preparation.
	other := *d
	if failed, err := validateCheckRow(&other, []sqltype.Value{trueValue}, eval); err != nil || failed != nil || &eval.PreparedChecks[&other][0] == first {
		t.Fatal("distinct descriptor reused executable cache", failed, err)
	}
	fresh := &exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}}
	if failed, err := validateCheckRow(d, []sqltype.Value{trueValue}, fresh); err != nil || failed != nil || &fresh.PreparedChecks[d][0] == first {
		t.Fatal("new command inherited executable CHECK cache", failed, err)
	}
	plain := &catalog.Relation{Constraints: catalog.RelationConstraints{Check: []catalog.CheckConstraint{{Name: "plain", Expression: trueNode}}}}
	if failed, err := validateCheckRow(plain, nil, eval); err != nil || failed != nil {
		t.Fatal(failed, err)
	}
	if nodes, ok := eval.PreparedChecks[plain]; !ok || nodes != nil {
		t.Fatal("unchanged CHECK preparation allocated an expression slice", nodes, ok)
	}
	emptyEval := &exec.Context{}
	if failed, err := validateCheckRow(&catalog.Relation{}, nil, emptyEval); err != nil || failed != nil || emptyEval.PreparedChecks != nil {
		t.Fatal("no-CHECK path initialized preparation state", failed, err)
	}
}

func TestAdvisorySessionCheckPrepareAllBeforeEvaluation(t *testing.T) {
	ctx := context.Background()
	e := New()
	b := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
	var nodes [2]expr.Expr
	for i, text := range []string{"pg_try_advisory_lock(3205)", "pg_try_advisory_lock(NULL::integer,1/0)"} {
		var err error
		nodes[i], err = b.Bind(statement(t, "SELECT "+text).(*parser.Select).Targets[0], analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
		if err != nil {
			t.Fatal("immutable CHECK child must remain analyzed until execution preparation", err)
		}
	}
	d := &catalog.Relation{Constraints: catalog.RelationConstraints{Check: []catalog.CheckConstraint{{Name: "a_effect", Expression: nodes[0]}, {Name: "z_error", Expression: nodes[1]}}}}
	eval := &exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}, EvalFrame: exec.EvalFrame{Scope: 9, Frame: 7}}
	failed, err := validateCheckRow(d, nil, eval)
	nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "22012", Message: "division by zero"})
	if failed != nil || eval.PreparedChecks != nil || eval.Scope != 9 || eval.Frame != 7 {
		t.Fatal("failed preparation cached a partial set or leaked a frame", failed)
	}
	// If volatile evaluation had started, the first producer would require an
	// admitted session instead of reporting the later immutable child error.
	if !reflect.DeepEqual(d.Constraints.Check[0].Expression, nodes[0]) || !reflect.DeepEqual(d.Constraints.Check[1].Expression, nodes[1]) {
		t.Fatal("failed CHECK preparation mutated analyzed expressions")
	}
}
