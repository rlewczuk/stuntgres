package engine

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
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
				bound, err := b.bind(raw, expressionPolicy{context: projectionContext})
				if err != nil {
					t.Fatal(err)
				}
				node, ok := bound.(advisoryExpression)
				if !ok {
					t.Fatalf("wrong producer %T", bound)
				}
				id := sqltype.Bool
				if name == "pg_advisory_lock" || name == "pg_advisory_lock_shared" || name == "pg_advisory_unlock_all" {
					id = sqltype.Void
				}
				if node.ResultUsage().Type.ID() != id || node.Properties().Volatility != Volatile || generationImmutable(node) || !pgIndexFunctionExpression(node) {
					t.Fatal("fixed identity/volatility", node.ResultUsage(), node.Properties())
				}
				if !node.validStored() {
					t.Fatal("bound producer lost stored signature")
				}
				if err = b.captureViewLiterals(raw, node, viewTargetLiteral, 0); err != nil || len(b.viewLiterals) != len(node.args) {
					t.Fatal("view literal children", len(b.viewLiterals), err)
				}
				text, err := (pgRenderer{view: e.CatalogRead()}).sql(ctx, node, pgRenderParent{})
				if err != nil || !strings.Contains(text, name+"(") {
					t.Fatal("symbolic SQL", text, err)
				}
				var tree strings.Builder
				if err = appendBoundTree(ctx, &tree, node); err != nil || !strings.Contains(tree.String(), "AdvisorySession "+name) {
					t.Fatal("symbolic tree", tree.String(), err)
				}
				reparsed := statement(t, "SELECT "+text).(*parser.Select).Targets[0]
				rebound, err := b.bind(reparsed, expressionPolicy{context: projectionContext})
				if err != nil || rebound.ResultUsage() != node.ResultUsage() || rebound.Properties().Volatility != Volatile {
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
			bound, err := b.bind(statement(t, query).(*parser.Select).Targets[0], expressionPolicy{context: projectionContext})
			if err != nil {
				t.Fatal(query, err)
			}
			node := bound.(advisoryExpression)
			id := sqltype.Int8
			if len(node.args) == 2 {
				id = sqltype.Int4
			}
			for _, child := range node.args {
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
			_, err := b.bind(statement(t, tc.query).(*parser.Select).Targets[0], expressionPolicy{context: projectionContext})
			nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: tc.code, Message: tc.message, Hint: tc.hint, Position: "8"})
		}
		query := "SELECT pg_advisory_lock(" + strings.Repeat("1,", 100) + "missing_advisory_child)"
		b := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
		_, err := b.bind(statement(t, query).(*parser.Select).Targets[0], expressionPolicy{context: projectionContext})
		var d *sqlstate.Error
		if !errors.As(err, &d) || d.Code != "42703" || d.Message != "column \"missing_advisory_child\" does not exist" {
			t.Fatal("child error must precede arity limit", err)
		}
	})
	t.Run("strict folding and runtime sibling demand", func(t *testing.T) {
		b := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
		raw := statement(t, "SELECT pg_advisory_lock(NULL,CASE WHEN pg_try_advisory_lock(12) THEN 1 ELSE 0 END)").(*parser.Select).Targets[0]
		bound, err := b.bind(raw, expressionPolicy{context: projectionContext})
		if err != nil {
			t.Fatal(err)
		}
		for _, preflight := range []bool{false, true} {
			var folded BoundExpr
			var changed bool
			if preflight {
				folded, changed, err = preflightCoercions(&EvalContext{Context: ctx}, bound)
			} else {
				folded, changed, err = simplifyConditional(&EvalContext{Context: ctx}, bound, false)
			}
			if err != nil || !changed || !generationImmutable(folded) {
				t.Fatal("literal NULL fold", folded, changed, err)
			}
			v, err := folded.Eval(&EvalContext{Context: ctx})
			if err != nil || !v.IsNull() || v.Type().ID() != sqltype.Void {
				t.Fatal("typed NULL VOID", v, err)
			}
		}
		_, err = b.bind(statement(t, "SELECT pg_advisory_lock(NULL,'bad'::integer)").(*parser.Select).Targets[0], expressionPolicy{context: projectionContext})
		nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "22P02", Message: "invalid input syntax for type integer: \"bad\"", Position: "30"})
		// Literal casts cook during binding. Separately supply an actual deferred
		// coercion plan to exercise demanded simplification in both walkers.
		bound, err = b.bind(statement(t, "SELECT pg_advisory_lock(NULL,1)").(*parser.Select).Targets[0], expressionPolicy{context: projectionContext})
		if err != nil {
			t.Fatal(err)
		}
		deferred := bound.(advisoryExpression)
		textUsage, integerUsage := b.typ(sqltype.Text).Usage(), b.typ(sqltype.Int4).Usage()
		plan, err := sqltype.PlanCoercion(textUsage, integerUsage, sqltype.Explicit)
		if err != nil {
			t.Fatal(err)
		}
		child := literalExpression{expressionInfo: b.info(textUsage, parser.Position{}, ExprProperties{Volatility: Immutable}), value: sqltype.String(textUsage.Type, "bad")}
		deferred.args = append([]BoundExpr(nil), deferred.args...)
		deferred.args[1] = coercionExpression{b.info(integerUsage, parser.Position{}, ExprProperties{Volatility: Immutable}), child, plan, sqltype.Explicit, false}
		bound = deferred
		for _, preflight := range []bool{false, true} {
			if preflight {
				_, _, err = preflightCoercions(&EvalContext{Context: ctx}, bound)
			} else {
				_, _, err = simplifyConditional(&EvalContext{Context: ctx}, bound, false)
			}
			nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "22P02", Message: "invalid input syntax for type integer: \"bad\""})
		}
		types := formatUnitTypes()
		var order []int
		one, err := sqltype.Integer(types[sqltype.Int4], 1)
		if err != nil {
			t.Fatal(err)
		}
		node := advisoryExpression{expressionInfo: expressionInfo{Usage: types[sqltype.Bool].Usage(), Property: ExprProperties{Volatility: Volatile}}, name: "pg_try_advisory_lock", mode: advisoryExclusive, action: advisoryTry, args: []BoundExpr{
			mathObservedArgument{expressionInfo: expressionInfo{Usage: types[sqltype.Int4].Usage()}, value: sqltype.Null(types[sqltype.Int4]), order: &order, index: 0},
			mathObservedArgument{expressionInfo: expressionInfo{Usage: types[sqltype.Int4].Usage()}, value: one, order: &order, index: 1},
		}}
		v, err := node.Eval(&EvalContext{Context: ctx})
		if err != nil || !v.IsNull() || v.Type().ID() != sqltype.Bool || !reflect.DeepEqual(order, []int{0, 1}) {
			t.Fatal("runtime children exactly once left to right", v, order, err)
		}
		failure := errors.New("demanded advisory sibling")
		order = nil
		node.args[1] = mathObservedArgument{expressionInfo: expressionInfo{Usage: types[sqltype.Int4].Usage()}, order: &order, index: 1, err: failure}
		if _, err = node.Eval(&EvalContext{Context: ctx}); !errors.Is(err, failure) || !reflect.DeepEqual(order, []int{0, 1}) {
			t.Fatal("runtime NULL suppressed demanded error", order, err)
		}
	})
	t.Run("lexical group aggregate remap and retained descriptors", func(t *testing.T) {
		b := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
		bound, err := b.bind(statement(t, "SELECT pg_try_advisory_lock(1,2)").(*parser.Select).Targets[0], expressionPolicy{context: projectionContext})
		if err != nil {
			t.Fatal(err)
		}
		node := bound.(advisoryExpression)
		column := columnExpression{expressionInfo: expressionInfo{Usage: node.args[0].ResultUsage(), Pos: parser.Position{Character: 99}, Property: ExprProperties{RowDependent: true}}, scope: 1, frame: b.frameID(), origin: ids.ObjectRef{Address: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 999, SubID: 1}}}
		node.args = []BoundExpr{node.args[0], column}
		node.Property.RowDependent = true
		visits := 0
		walkBoundExpression(node, func(BoundExpr) { visits++ })
		if visits != 3 || firstQueryBoundFrameVariable(node, b.frameID()).Character != 99 {
			t.Fatal("lexical child/frame walk", visits)
		}
		grouped, err := b.rewriteGroup(node, []BoundExpr{column})
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := grouped.(advisoryExpression).args[1].(groupExpression); !ok {
			t.Fatal("group descendant not rewritten")
		}
		if _, ok := node.args[1].(columnExpression); !ok {
			t.Fatal("group rewrite mutated shared source")
		}
		if !b.sameGroupExpression(node, node) {
			t.Fatal("group structural identity")
		}
		other := node
		other.args = []BoundExpr{node.args[1], node.args[0]}
		if b.sameGroupExpression(node, other) {
			t.Fatal("group identity ignored argument order")
		}
		remapped, err := remapBoundExpression(node, func(ref ids.ObjectRef) ids.ObjectRef { ref.Address.ObjectID++; return ref }, map[int32]int{1: 7})
		if err != nil {
			t.Fatal(err)
		}
		mappedColumn := remapped.(advisoryExpression).args[1].(columnExpression)
		if mappedColumn.origin.Address.ObjectID != 1000 || mappedColumn.slot != 7 || node.args[1].(columnExpression).origin.Address.ObjectID != 999 {
			t.Fatal("view child identity/slot remap", mappedColumn)
		}
		types := formatUnitTypes()
		retained, changed, err := retagBoundTypes(New().CatalogRead(), node, func(d *sqltype.Descriptor) *sqltype.Descriptor { return types[d.ID()] })
		if err != nil || !changed {
			t.Fatal(retained, changed, err)
		}
		fresh := retained.(advisoryExpression)
		if fresh.ResultUsage().Type != types[sqltype.Bool] || fresh.args[1].ResultUsage().Type != types[sqltype.Int4] || fresh.mode != node.mode || fresh.action != node.action || node.ResultUsage().Type == fresh.ResultUsage().Type {
			t.Fatal("retained generation/compiled action")
		}
		aggregate := aggregateExpression{expressionInfo: expressionInfo{Usage: node.args[0].ResultUsage(), Property: ExprProperties{StageDependent: true}}, slot: 7}
		node.args = []BoundExpr{node.args[0], aggregate}
		if found := aggregateOccurrence(node); found == nil || found.slot != 7 {
			t.Fatal("aggregate descendant lost")
		}
	})
	t.Run("stored consumer policy and signature validation", func(t *testing.T) {
		s := sessionStart(t, e, nil)
		sessionSQL(t, s, "CREATE TABLE advisory_render_owner(v INT)")
		catalog := e.CatalogRead()
		ref := catalogRef(t, e, catalog, "advisory_render_owner")
		owner := catalog.MustRelation(ref.Address)
		b := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
		raw := statement(t, "SELECT pg_try_advisory_lock(1,2)").(*parser.Select).Targets[0]
		bound, err := b.bind(raw, expressionPolicy{context: projectionContext})
		if err != nil {
			t.Fatal(err)
		}
		node := bound.(advisoryExpression)
		text, err := (scalarRenderer{owner: owner}).sql(ctx, catalog, node)
		if err != nil || !strings.Contains(text, "pg_catalog.pg_try_advisory_lock(") {
			t.Fatal("stored CHECK/view symbolic rendering", text, err)
		}
		if _, err = defaultSQL(ctx, catalog, node); err == nil {
			t.Fatal("volatile default rendering widened")
		}
		if _, err = b.bindDefault(raw, ColumnDescriptor{Name: "v", Usage: node.ResultUsage()}); err == nil {
			t.Fatal("DEFAULT binding widened")
		}
		damaged := node
		damaged.action = advisoryUnlock
		if damaged.validStored() {
			t.Fatal("inconsistent compiled action admitted")
		}
		if _, err = (scalarRenderer{owner: owner}).sql(ctx, catalog, damaged); err == nil {
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
		bound, err := b.bind(rawStatement.(*parser.Select).Targets[0], expressionPolicy{context: projectionContext})
		if err != nil {
			_ = scope.Finish(err)
			op.Done()
			t.Fatal(err)
		}
		v, err := bound.Eval(&EvalContext{Context: op.Context(), statementScope: scope})
		finish := scope.Finish(err)
		op.Done()
		if err != nil || finish != nil || v.IsNull() || v.Type().ID() != sqltype.Void || advisoryUnitHold(s, lockResource{Kind: advisoryBigintResource, Low: 42}).Exclusive != 1 {
			t.Fatal("actual VOID grant", v, err, finish)
		}
		if err = e.locks.UnlockAdvisoryAll(s.advisory.ticket); err != nil {
			t.Fatal(err)
		}
	})
}

func TestAdvisorySessionCheckPreparationCache(t *testing.T) {
	ctx := context.Background()
	e := New()
	b := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
	bind := func(text string) BoundExpr {
		t.Helper()
		node, err := b.bind(statement(t, "SELECT "+text).(*parser.Select).Targets[0], expressionPolicy{context: projectionContext})
		if err != nil {
			t.Fatal(err)
		}
		return node
	}
	trueNode, falseNode := bind("true"), bind("false")
	trueValue, err := trueNode.Eval(&EvalContext{Context: ctx})
	if err != nil {
		t.Fatal(err)
	}
	falseValue, err := falseNode.Eval(&EvalContext{Context: ctx})
	if err != nil {
		t.Fatal(err)
	}
	column := columnExpression{expressionInfo: expressionInfo{Usage: trueNode.ResultUsage(), Property: ExprProperties{RowDependent: true}}, scope: 1, frame: 1}
	stored := bind("pg_try_advisory_lock(NULL::integer,CASE WHEN pg_try_advisory_lock(3201) THEN 2 ELSE 3 END)")
	var before strings.Builder
	if err := appendBoundTree(ctx, &before, stored); err != nil {
		t.Fatal(err)
	}
	d := &relationDescriptor{checkConstraints: []checkConstraint{{Name: "a_row", expression: column}, {Name: "b_null", expression: stored}}}
	oldRow := []sqltype.Value{falseValue}
	eval := &EvalContext{Context: ctx, Scope: 9, Frame: 7, Row: oldRow}
	for i := 0; i < 2; i++ {
		failed, err := validateCheckRow(d, []sqltype.Value{trueValue}, eval)
		if err != nil || failed != nil {
			t.Fatal("prepared constant NULL must not demand a session", failed, err)
		}
	}
	cached, ok := eval.preparedChecks[d]
	if !ok || len(cached) != 2 || !reflect.DeepEqual(cached[0], column) {
		t.Fatal("unchanged CHECK references or descriptor cache lost", cached, ok)
	}
	if _, ok := cached[1].(literalExpression); !ok {
		t.Fatal("constant-NULL executable CHECK was not detached", cached[1])
	}
	first := &cached[0]
	failed, err := validateCheckRow(d, []sqltype.Value{falseValue}, eval)
	if err != nil || failed != &d.checkConstraints[0] || &eval.preparedChecks[d][0] != first {
		t.Fatal("row changes must reuse preparation and return original constraint", failed, err)
	}
	if eval.Scope != 9 || eval.Frame != 7 || &eval.Row[0] != &oldRow[0] {
		t.Fatal("CHECK evaluator leaked its row frame")
	}
	var after strings.Builder
	if err := appendBoundTree(ctx, &after, d.checkConstraints[1].expression); err != nil || after.String() != before.String() {
		t.Fatal("execution preparation changed stored analyzed syntax", err, before.String(), after.String())
	}
	// Descriptor identity, not a relation name/address, bounds cache reuse;
	// fresh command contexts never inherit a previous command's preparation.
	other := *d
	if failed, err := validateCheckRow(&other, []sqltype.Value{trueValue}, eval); err != nil || failed != nil || &eval.preparedChecks[&other][0] == first {
		t.Fatal("distinct descriptor reused executable cache", failed, err)
	}
	fresh := &EvalContext{Context: ctx}
	if failed, err := validateCheckRow(d, []sqltype.Value{trueValue}, fresh); err != nil || failed != nil || &fresh.preparedChecks[d][0] == first {
		t.Fatal("new command inherited executable CHECK cache", failed, err)
	}
	plain := &relationDescriptor{checkConstraints: []checkConstraint{{Name: "plain", expression: trueNode}}}
	if failed, err := validateCheckRow(plain, nil, eval); err != nil || failed != nil {
		t.Fatal(failed, err)
	}
	if nodes, ok := eval.preparedChecks[plain]; !ok || nodes != nil {
		t.Fatal("unchanged CHECK preparation allocated an expression slice", nodes, ok)
	}
	emptyEval := &EvalContext{}
	if failed, err := validateCheckRow(&relationDescriptor{}, nil, emptyEval); err != nil || failed != nil || emptyEval.preparedChecks != nil {
		t.Fatal("no-CHECK path initialized preparation state", failed, err)
	}
}

func TestAdvisorySessionCheckPrepareAllBeforeEvaluation(t *testing.T) {
	ctx := context.Background()
	e := New()
	b := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
	var nodes [2]BoundExpr
	for i, text := range []string{"pg_try_advisory_lock(3205)", "pg_try_advisory_lock(NULL::integer,1/0)"} {
		var err error
		nodes[i], err = b.bind(statement(t, "SELECT "+text).(*parser.Select).Targets[0], expressionPolicy{context: projectionContext})
		if err != nil {
			t.Fatal("immutable CHECK child must remain analyzed until execution preparation", err)
		}
	}
	d := &relationDescriptor{checkConstraints: []checkConstraint{{Name: "a_effect", expression: nodes[0]}, {Name: "z_error", expression: nodes[1]}}}
	eval := &EvalContext{Context: ctx, Scope: 9, Frame: 7}
	failed, err := validateCheckRow(d, nil, eval)
	nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "22012", Message: "division by zero"})
	if failed != nil || eval.preparedChecks != nil || eval.Scope != 9 || eval.Frame != 7 {
		t.Fatal("failed preparation cached a partial set or leaked a frame", failed)
	}
	// If volatile evaluation had started, the first producer would require an
	// admitted session instead of reporting the later immutable child error.
	if !reflect.DeepEqual(d.checkConstraints[0].expression, nodes[0]) || !reflect.DeepEqual(d.checkConstraints[1].expression, nodes[1]) {
		t.Fatal("failed CHECK preparation mutated analyzed expressions")
	}
}
