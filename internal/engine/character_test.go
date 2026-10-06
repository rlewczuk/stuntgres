package engine

import (
	"context"
	"testing"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestCharacterRightColumnDependenceAndCastUsage(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE c(v CHAR(3))")
	run(t, e, "INSERT INTO c VALUES('a'),('b')")
	op, done, err := testOperation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	e.mu.Lock()
	auth, err := e.authorizationLocked(op)
	if err != nil {
		e.mu.Unlock()
		t.Fatal(err)
	}
	ast := statement(t, "SELECT CAST(v AS varchar(2)) FROM c WHERE CAST('a ' AS bpchar)=v")
	ref, err := e.resolveReference(auth, ast.(*parser.Select).Name)
	if err != nil {
		e.mu.Unlock()
		t.Fatal(err)
	}
	b, err := e.describe(op.ctx, ast, auth, ref, sqltype.InputEnvironment{}, op, nil)
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	project := b.query.Root.(*projectNode)
	filter := project.child.(*filterNode)
	properties := filter.predicate.Properties()
	if !properties.RowDependent || properties.ParameterDependent || properties.Volatility != Immutable {
		t.Fatal("right-column dependence was lost", properties)
	}
	if !project.expressions[0].Properties().RowDependent || project.expressions[0].ResultUsage().Typmod != 6 || project.expressions[0].ResultUsage().Type.ID() != sqltype.VarChar {
		t.Fatal(project.expressions[0].ResultUsage(), project.expressions[0].Properties())
	}
	bpchar, _ := e.CatalogRead().LookupType(sqltype.BPChar)
	for _, tc := range []struct {
		text string
		want Truth
	}{{"a  ", True}, {"b  ", False}} {
		value, err := filter.predicate.Eval(&EvalContext{Context: context.Background(), Scope: 1, Frame: 1, Row: []sqltype.Value{sqltype.String(bpchar, tc.text)}})
		if err != nil {
			t.Fatal(err)
		}
		got, err := TruthOf(value)
		if err != nil || got != tc.want {
			t.Fatal(tc, got, err)
		}
	}
}
