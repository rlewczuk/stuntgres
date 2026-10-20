package engine

import (
	"context"
	"reflect"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func updateRowTestBinding(t *testing.T, q string) (*Engine, binding) {
	t.Helper()
	e := New()
	run(t, e, "CREATE TABLE t(id INT,a INT,b INT,c INT,d INT)")
	op, done, err := testOperation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	ast := statement(t, q)
	e.mu.Lock()
	defer e.mu.Unlock()
	auth, err := e.authorizationLocked(op)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := e.resolveReference(auth, ast.(*parser.Update).Name)
	if err != nil {
		t.Fatal(err)
	}
	bound, err := describe(op.Context(), e.analysisHost(op), ast, auth, ref, sqltype.InputEnvironment{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	return e, bound
}

func TestUpdateRowBinding(t *testing.T) {
	_, b := updateRowTestBinding(t, "UPDATE t SET id=id+1,(a,b)=(SELECT b,a),(c,d)=ROW(a,b)")
	u := b.update
	if !reflect.DeepEqual(b.targets, []int{0, 1, 2, 3, 4}) || len(u.assignments) != 5 || len(u.origins) != 5 || len(u.groups) != 1 || u.groups[0] != (updateAssignmentGroup{start: 1, width: 2}) || u.outputWidth != 2 {
		t.Fatal("normalization order", b.targets, u)
	}
	source, ok := u.assignments[1].(plan.SubqueryExpr)
	if !ok || source.UpdateWidth != 2 || len(source.QueryPlan.Root.Schema()) != 2 {
		t.Fatal("missing shared source", u.assignments[1])
	}
	output, ok := u.assignments[2].(plan.UpdateOutputExpr)
	if !ok || output.Index != 1 || output.Group != 1 || output.ResultUsage() != source.QueryPlan.Root.Schema()[1].Usage || output.SourcePosition() != u.origins[2] {
		t.Fatal("output provenance", output)
	}
	_, single := updateRowTestBinding(t, "UPDATE t SET (a)=(SELECT b)")
	if single.update.assignments[0].(plan.SubqueryExpr).UpdateWidth != 1 || len(single.update.groups) != 1 {
		t.Fatal("singleton group lost assignment identity")
	}
	for _, q := range []string{"UPDATE t SET a=1,b=2", "UPDATE t SET (a,b)=(SELECT 1,2)"} {
		t.Run("destination_diagnostic_input/"+q, func(t *testing.T) {
			e := New()
			owner := sessionStart(t, e, nil)
			sessionSQL(t, owner, "CREATE DOMAIN positive AS INT CHECK(VALUE>0)")
			sessionSQL(t, owner, "CREATE TABLE t(a positive,b INT)")
			op, scope, b, _, _ := updateFromBoundSources(t, e, owner, context.Background(), q)
			defer op.Done()
			defer scope.Finish(context.Canceled)
			if b.resolvers.typeDiagnosticName == nil || b.resolvers.typeDiagnosticName(b.relation.Columns[0].Usage.Type.ID()) != "positive" {
				t.Fatal("assignment lost captured destination diagnostic naming")
			}
		})
	}
}

func TestUpdateRowVisitors(t *testing.T) {
	e, b := updateRowTestBinding(t, "UPDATE t SET (a,b)=(SELECT a+$1::int,b)")
	u := b.update
	queries, outputs := 0, 0
	for _, recipe := range u.assignments {
		walkBoundExpression(recipe, func(node expr.Expr) {
			switch node.(type) {
			case plan.SubqueryExpr:
				queries++
			case plan.UpdateOutputExpr:
				outputs++
			}
		})
	}
	if queries != 1 || outputs != 1 {
		t.Fatal("source duplicated in lexical recipes", queries, outputs)
	}
	queryMaps := 0
	mapQuery := func(root plan.Node) (plan.Node, error) { queryMaps++; return root, nil }
	for _, recipe := range u.assignments {
		_, _, err := retagBoundTypesWithQuery(e.CatalogRead(), recipe, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d }, mapQuery)
		if err != nil {
			t.Fatal(err)
		}
	}
	if queryMaps != 1 {
		t.Fatal("query graph mapping count", queryMaps)
	}
	original := u.assignments[0].(plan.SubqueryExpr)
	mapped, _, err := mapBoundExpression(e.CatalogRead(), original, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d }, nil, nil, func(node expr.Expr) (expr.Expr, bool, error) { return node, false, nil })
	if err != nil || mapped.(plan.SubqueryExpr).UpdateWidth != 2 || original.QueryPlan != u.assignments[0].(plan.SubqueryExpr).QueryPlan {
		t.Fatal("lost marker or mutated template", err)
	}
	ctx := EvalContext{evalStatement: evalStatement{Context: context.Background()}}
	prepared, err := preflightUpdateWithPlanning(&ctx, u, false)
	if err != nil || prepared != u {
		t.Fatal("unchanged preflight replaced immutable source", prepared, err)
	}
}

func TestUpdateRowRuleRestriction(t *testing.T) {
	e, b := updateRowTestBinding(t, "UPDATE t SET (a,b)=(SELECT b,a)")
	for _, recipe := range b.update.assignments {
		wrapped := plan.CollationExpr{Info: expr.InfoOf(recipe), Child: recipe}
		state(t, validateRuleNewUpdateOutput(wrapped), "0A000")
	}
	_, one := updateRowTestBinding(t, "UPDATE t SET (a)=(SELECT b)")
	state(t, validateRuleNewUpdateOutput(one.update.assignments[0]), "0A000")
	_, constructor := updateRowTestBinding(t, "UPDATE t SET (a,b)=ROW(b,a)")
	for _, recipe := range constructor.update.assignments {
		if err := validateRuleNewUpdateOutput(recipe); err != nil {
			t.Fatal(err)
		}
	}
	// A nested query's output markers are not current-level lexical recipes.
	_, query, _ := queryJoinsFixture(t, "SELECT k FROM a WHERE k=2")
	project := query.Root.(*plan.ProjectNode)
	subqueryTestTarget(t, project, b.update.assignments[1])
	outer := subqueryTestExpression(t, e, query, parser.SubqueryScalar)
	if err := validateRuleNewUpdateOutput(outer); err != nil {
		t.Fatal("restriction crossed scalar SubLink boundary", err)
	}
}
