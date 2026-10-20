package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestQueryFromValuesCollationProvenance(t *testing.T) {
	b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
	text := b.typ(sqltype.Text)
	usage := text.Usage()
	operand := func(id uint32, position int) expr.Expr {
		u := usage
		u.Collation = sqltype.CollationRef{ID: id, Derivation: sqltype.Default}
		return plan.LiteralExpr{Info: b.info(u, parser.Position{Character: position}, expr.Properties{}), Value: sqltype.Null(text)}
	}
	info, err := b.collatedInfo(b.info(usage, parser.Position{Character: 40}, expr.Properties{}), operand(950, 59), operand(951, 64))
	if err != nil {
		t.Fatal(err)
	}
	base := plan.LiteralExpr{Info: info, Value: sqltype.Null(text)}
	assert := func(name string, expression expr.Expr) {
		t.Helper()
		left, right, position := expression.ImplicitCollationConflict()
		if !expression.CollationIndeterminate() || left.ID != 950 || right.ID != 951 || position.Character != 64 {
			t.Fatal(name, expression.CollationIndeterminate(), left, right, position)
		}
	}
	assert("common", base)
	coerced, err := b.coerceConditional(comparisonOperand{node: base}, b.typ(sqltype.VarChar).Usage(), "VALUES")
	if err != nil {
		t.Fatal(err)
	}
	assert("coercion", coerced)
	folded, _, err := simplifyConditional(&EvalContext{EvalStatement: evalStatement{Context: context.Background()}}, coerced, false)
	if err != nil {
		t.Fatal(err)
	}
	assert("folded coercion", folded)
	grouped, err := b.rewriteGroup(base, []expr.Expr{base})
	if err != nil {
		t.Fatal(err)
	}
	assert("group", grouped)
	retagged, _, err := retagBoundTypes(b.bindingCatalog(), base, func(old *sqltype.Descriptor) *sqltype.Descriptor { next := *old; return &next })
	if err != nil {
		t.Fatal(err)
	}
	assert("retained descriptor", retagged)
	projection := []expr.Expr{coerced}
	_, err = b.finishOrder(&plan.ValuesNode{QuerySchema: plan.QuerySchema{{Name: "n", Usage: coerced.ResultUsage()}}}, []plan.SortKey{{Expression: coerced, Cache: -1, Slot: -1}}, projection)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := projection[0].(plan.CachedOrderExpr); !ok {
		t.Fatal("ORDER did not cache equivalent output", projection[0])
	}
	assert("cache", projection[0])
	order := statement(t, "VALUES('a') ORDER BY 1 LIMIT 0").(*parser.Select).Values.Order
	for _, expression := range []expr.Expr{base, coerced, grouped, projection[0], retagged} {
		_, err := b.bindOrder(order, []expr.Expr{expression}, []string{"n"}, "ORDER BY")
		var diagnostic *sqlstate.Error
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P21", Message: `collation mismatch between implicit collations "C" and "POSIX"`, Hint: "You can choose the collation by applying the COLLATE clause to one or both expressions.", Position: "64"}
		if !errors.As(err, &diagnostic) || *diagnostic != want {
			t.Fatal(err, diagnostic, want)
		}
	}
	defaultC, err := b.collatedInfo(b.info(usage, parser.Position{}, expr.Properties{}), operand(100, 1), operand(950, 2))
	if err != nil || defaultC.CollationConflict || defaultC.Usage.Collation.ID != 950 {
		t.Fatal(defaultC, err)
	}
	explicit := operand(950, 80).(plan.LiteralExpr)
	explicit.CollationExplicit = parser.Position{Character: 80}
	overridden, err := b.collatedInfo(info, explicit)
	if err != nil || overridden.CollationConflict || overridden.CollationLeft.ID != 0 || overridden.CollationRight.ID != 0 || overridden.CollationConflictPos != (parser.Position{}) || overridden.Usage.Collation.ID != 950 {
		t.Fatal(overridden, err)
	}
	b.query.namespace = &queryNamespace{}
	if _, err := b.bindOrder(order, []expr.Expr{plan.LiteralExpr{Info: overridden, Value: sqltype.Null(text)}}, []string{"n"}, "ORDER BY"); err != nil {
		t.Fatal("explicit override did not restore ordinary ordering", err)
	}
}
