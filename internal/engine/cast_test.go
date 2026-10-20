package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func castTarget(t *testing.T, q string) parser.Expr {
	t.Helper()
	return statement(t, q).(*parser.Select).Targets[0]
}

func TestCastExpressionOccurrenceAndDiagnosticOwnership(t *testing.T) {
	e := New()
	b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	first, err := b.bind(castTarget(t, "SELECT $1::int4 FROM t"), expressionPolicy{context: projectionContext})
	if err != nil {
		t.Fatal(err)
	}
	later, err := b.bind(castTarget(t, "SELECT $1::text FROM t"), expressionPolicy{context: projectionContext})
	if err != nil {
		t.Fatal(err)
	}
	info, err := b.finish()
	if err != nil || info.Count != 1 || info.Types[0].Type.ID() != sqltype.Int4 || first.ResultUsage().Type.ID() != sqltype.Int4 || later.ResultUsage().Type.ID() != sqltype.Text || !later.Properties().ParameterDependent {
		t.Fatal(info, first, later, err)
	}
	value, _ := sqltype.Integer(b.typ(sqltype.Int4), 2)
	got, err := later.Eval(&EvalContext{evalStatement: evalStatement{Context: context.Background(), Parameters: []sqltype.Value{value}}})
	text, accessErr := got.Text()
	if err != nil || accessErr != nil || text != "2" {
		t.Fatal(got, err, accessErr)
	}
	earlierBinder := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	earlier, err := earlierBinder.bind(castTarget(t, "SELECT $1 FROM t"), expressionPolicy{context: projectionContext})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := earlierBinder.bind(castTarget(t, "SELECT $1::int4 FROM t"), expressionPolicy{context: projectionContext}); err != nil {
		t.Fatal(err)
	}
	_, err = earlierBinder.finalizeProjection(earlier)
	state(t, err, "42P08")
	for _, tc := range []struct {
		q, code, position string
		evaluation        bool
	}{
		{"SELECT CAST('oops' AS int4) FROM t", "22P02", "13", false},
		{"SELECT CAST('oops'::text AS int4) FROM t", "22P02", "", true},
		{"SELECT 2147483648::int4 FROM t", "22003", "", true},
		{"SELECT int4($1) FROM t", "42725", "8", false},
		{"SELECT CAST(NULL::int8 AS bool) FROM t", "42846", "8", false},
		{"SELECT bool(NULL::int8) FROM t", "42883", "8", false},
	} {
		binder := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
		expr, err := binder.bind(castTarget(t, tc.q), expressionPolicy{context: projectionContext})
		if tc.evaluation {
			if err != nil {
				t.Fatal(tc.q, err)
			}
			_, err = expr.Eval(&EvalContext{evalStatement: evalStatement{Context: context.Background()}})
		}
		var d *sqlstate.Error
		if !errors.As(err, &d) || d.Code != tc.code || d.Position != tc.position {
			t.Fatal(tc.q, err, d)
		}
	}
}

func TestCastExpressionReductionOriginsAndDependencies(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE t(i INT NOT NULL,n BIGINT)")
	op, done, err := testOperation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	e.mu.Lock()
	defer e.mu.Unlock()
	auth, err := e.authorizationLocked(op)
	if err != nil {
		t.Fatal(err)
	}
	ast := statement(t, "SELECT i,i::int4,int4(i),CAST(i AS bigint),CAST(int8(i) AS text) FROM t")
	ref, err := e.resolveReference(auth, ast.(*parser.Select).From.(*parser.TableReference).Name)
	if err != nil {
		t.Fatal(err)
	}
	bound, err := e.describe(op.ctx, ast, auth, ref, sqltype.InputEnvironment{}, op, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	project := bound.query.Root.(*plan.ProjectNode)
	original := project.Expressions[0].(plan.ColumnExpr)
	for _, index := range []int{1, 2} {
		reduced, ok := project.Expressions[index].(plan.ColumnExpr)
		if !ok || reduced.Origin != original.Origin || reduced.Slot != original.Slot || project.Schema()[index].Nullable {
			t.Fatal(index, project.Expressions[index], project.Schema()[index])
		}
	}
	for _, index := range []int{3, 4} {
		conversion, ok := project.Expressions[index].(plan.CoercionExpr)
		if !ok || !conversion.Properties().RowDependent || conversion.Properties().ParameterDependent {
			t.Fatal(index, project.Expressions[index])
		}
	}
	for i, name := range []string{"i", "i", "int4", "i", "int8"} {
		if project.Schema()[i].Name != name {
			t.Fatal(i, project.Schema()[i])
		}
	}
	foundColumn, foundText := false, false
	for _, dep := range bound.dependencies {
		if dep.Ref == original.Origin {
			foundColumn = true
		}
		if dep.Ref.Address.ClassID == ids.TypeClassID && dep.Ref.Address.ObjectID == ids.OID(sqltype.Text) {
			foundText = true
		}
	}
	if !foundColumn || !foundText {
		t.Fatal(bound.dependencies)
	}
}

func TestCastRestoreRecursiveParameterGuard(t *testing.T) {
	raw := castTarget(t, "SELECT CAST($1::text AS int4) FROM t")
	err := restoreScalar(raw, false)
	var d *sqlstate.Error
	if !errors.As(err, &d) || d.Code != "42P02" || d.Position != "13" {
		t.Fatal(err)
	}
	if err := restoreScalar(castTarget(t, "SELECT CAST('42'::text AS int4) FROM t"), false); err != nil {
		t.Fatal(err)
	}
}
