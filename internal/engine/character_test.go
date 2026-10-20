package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
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
	ref, err := e.resolveReference(auth, ast.(*parser.Select).From.(*parser.TableReference).Name)
	if err != nil {
		e.mu.Unlock()
		t.Fatal(err)
	}
	b, err := describe(op.ctx, e.analysisHost(op), ast, auth, ref, sqltype.InputEnvironment{}, nil, nil)
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	project := b.Query.Root.(*plan.ProjectNode)
	filter := project.Child.(*plan.FilterNode)
	properties := filter.Predicate.Properties()
	if !properties.RowDependent || properties.ParameterDependent || properties.Volatility != expr.Immutable {
		t.Fatal("right-column dependence was lost", properties)
	}
	if !project.Expressions[0].Properties().RowDependent || project.Expressions[0].ResultUsage().Typmod != 6 || project.Expressions[0].ResultUsage().Type.ID() != sqltype.VarChar {
		t.Fatal(project.Expressions[0].ResultUsage(), project.Expressions[0].Properties())
	}
	bpchar, _ := e.CatalogRead().LookupType(sqltype.BPChar)
	for _, tc := range []struct {
		text string
		want plan.Truth
	}{{"a  ", plan.True}, {"b  ", plan.False}} {
		value, err := filter.Predicate.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1, Row: []sqltype.Value{sqltype.String(bpchar, tc.text)}}})
		if err != nil {
			t.Fatal(err)
		}
		got, err := plan.TruthOf(value)
		if err != nil || got != tc.want {
			t.Fatal(tc, got, err)
		}
	}
}

func TestCharacterCastPreservesKnownParameterUsage(t *testing.T) {
	e := New()
	b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	source := b.Type(sqltype.BPChar).Usage()
	b.Params.Parameters[1] = source
	raw := statement(t, "SELECT CAST($1 AS text) FROM seed").(*parser.Select).Targets[0]
	node, err := b.Bind(raw, expressionPolicy{Context: ProjectionContext})
	if err != nil {
		t.Fatal(err)
	}
	cast, ok := node.(plan.CoercionExpr)
	if !ok || cast.Child.ResultUsage() != source || cast.ResultUsage().Type.ID() != sqltype.Text || b.Params.Parameters[1] != source {
		t.Fatal("cast retargeted known parameter", node, b.Params.Parameters[1])
	}
	if !cast.Properties().ParameterDependent || cast.Properties().RowDependent || cast.Properties().Volatility != expr.Immutable {
		t.Fatal("cast properties", cast.Properties())
	}
	for _, id := range []sqltype.ID{sqltype.BPChar, sqltype.Text} {
		found := false
		for _, dependency := range b.Dependencies {
			if dependency.Ref.Address.ClassID == ids.TypeClassID && dependency.Ref.Address.ObjectID == ids.OID(id) {
				found = true
			}
		}
		if !found {
			t.Fatal("missing cast type dependency", id, b.Dependencies)
		}
	}
}

func TestCharacterCastIdentityColumnProof(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE proof(c CHAR(3) NOT NULL,v VARCHAR(3),t TEXT NOT NULL,u BPCHAR,n CHAR(3))")
	op, done, err := testOperation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	base, err := e.Describe(op, statement(t, "SELECT * FROM proof"))
	if err != nil {
		t.Fatal(err)
	}
	q := "SELECT c::char(3),c::char(4),v::varchar(3),v::varchar(2),CAST(t AS text),u::bpchar,n::char(3),c::char(3)::char(3),c::char(4)::char(4) FROM proof"
	description, err := e.Describe(op, statement(t, q))
	if err != nil {
		t.Fatal(err)
	}
	// Positive entries copy a physical descriptor exactly, including origin,
	// NOT NULL and typmod. Zero entries are genuine conversions with no proof.
	origins := []int{1, 0, 2, 0, 3, 4, 5, 1, 0}
	if len(description.Results) != len(origins) {
		t.Fatal(description.Results)
	}
	for i, origin := range origins {
		got := description.Results[i]
		if origin > 0 {
			if got != base.Results[origin-1] {
				t.Fatal("identity lost column proof", i, got, base.Results[origin-1])
			}
		} else if got.NullabilityKnown || got.Nullable || got.OriginTable != 0 || got.OriginAttribute != 0 {
			t.Fatal("conversion fabricated column proof", i, got)
		}
	}
}
