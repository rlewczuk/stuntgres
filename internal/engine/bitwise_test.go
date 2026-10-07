package engine

import (
	"context"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestBitwiseDefaultValidationAndRetag(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE d(i INTEGER DEFAULT (1 & 3),s SMALLINT DEFAULT (1::smallint << 15),n SMALLINT DEFAULT (~NULL::smallint))")
	view := e.CatalogRead()
	ref := roleTableRef(t, e, "d")
	old := view.state.descriptors[ref.Address].(*relationDescriptor)
	if err := (relationProvider{}).Validate(view, old); err != nil {
		t.Fatal(err)
	}
	root := old.defaults[1].expression.(bitwiseExpression)
	for _, mutate := range []func(bitwiseExpression) bitwiseExpression{
		func(n bitwiseExpression) bitwiseExpression { n.left = nil; return n },
		func(n bitwiseExpression) bitwiseExpression { n.right = nil; return n },
		func(n bitwiseExpression) bitwiseExpression { n.operator = sqltype.BitwiseOperator(255); return n },
		func(n bitwiseExpression) bitwiseExpression { n.operator = sqltype.BitwiseOr; return n },
		func(n bitwiseExpression) bitwiseExpression { n.provider = sqltype.Bitwise{}; return n },
		func(n bitwiseExpression) bitwiseExpression { n.right = old.defaults[2].expression; return n },
		func(n bitwiseExpression) bitwiseExpression {
			l := n.left.(literalExpression)
			l.properties.RowDependent = true
			n.left = l
			return n
		},
		func(n bitwiseExpression) bitwiseExpression { n.right = literalExpression{}; return n },
		func(n bitwiseExpression) bitwiseExpression {
			foreign := New()
			typ, _ := foreign.CatalogRead().LookupType(sqltype.Int4)
			value, _ := sqltype.Integer(typ, 1)
			n.left = literalExpression{expressionInfo: expressionInfo{usage: typ.Usage()}, value: value}
			return n
		},
	} {
		if _, err := defaultSQL(context.Background(), view, mutate(root)); err == nil {
			t.Fatal("invalid bitwise default accepted")
		}
	}
	shift := old.defaults[2].expression.(bitwiseExpression)
	shift.right = old.defaults[3].expression
	if _, err := defaultSQL(context.Background(), view, shift); err == nil {
		t.Fatal("SMALLINT bound shift count accepted")
	}
	ctx := &EvalContext{Context: context.Background()}
	ordinary, changed, err := preflightCoercions(ctx, root)
	if err != nil || changed {
		t.Fatal(ordinary, changed, err)
	}
	if _, ok := ordinary.(bitwiseExpression); !ok {
		t.Fatal("ordinary node folded")
	}
	folded, changed, err := preflightCoercionsWithPolicy(ctx, root, true)
	if err != nil || !changed {
		t.Fatal(folded, changed, err)
	}
	v, err := folded.Eval(ctx)
	n, _ := v.Integer()
	if err != nil || n != 1 {
		t.Fatal(v, err)
	}
	if _, ok := old.defaults[1].expression.(bitwiseExpression); !ok {
		t.Fatal("catalog mutated")
	}
	replacement := New()
	retag := func(d *sqltype.Descriptor) *sqltype.Descriptor {
		x, _ := replacement.CatalogRead().LookupType(d.ID())
		return x
	}
	for _, attribute := range []int32{1, 2, 3} {
		original := old.defaults[attribute].expression
		rebound, changed, err := retagDefaultTypes(original, retag)
		if err != nil || !changed || rebound.ResultUsage().Type == original.ResultUsage().Type {
			t.Fatal(rebound, changed, err)
		}
		a, err := original.Eval(ctx)
		if err != nil {
			t.Fatal(err)
		}
		b, err := rebound.Eval(ctx)
		if err != nil || a.IsNull() != b.IsNull() {
			t.Fatal(a, b, err)
		}
		av, _ := a.Integer()
		bv, _ := b.Integer()
		if av != bv {
			t.Fatal(av, bv)
		}
		if _, err := defaultSQL(context.Background(), replacement.CatalogRead(), rebound); err != nil {
			t.Fatal(err)
		}
	}
}

func TestBitwiseScopedCastDefaultSerializationAndRetag(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE d(w BIGINT DEFAULT CAST(32767::smallint << 1::smallint AS bigint),n INTEGER DEFAULT CAST(~NULL::smallint AS integer))")
	view := e.CatalogRead()
	ref := roleTableRef(t, e, "d")
	table := view.state.descriptors[ref.Address].(*relationDescriptor)
	replacement := New()
	retag := func(d *sqltype.Descriptor) *sqltype.Descriptor {
		typ, _ := replacement.CatalogRead().LookupType(d.ID())
		return typ
	}
	ctx := &EvalContext{Context: context.Background()}
	for _, attribute := range []int32{1, 2} {
		original := table.defaults[attribute].expression
		rebound, changed, err := retagDefaultTypes(original, retag)
		if err != nil || !changed {
			t.Fatal(rebound, changed, err)
		}
		for _, node := range []BoundExpr{original, rebound} {
			value, err := node.Eval(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if attribute == 2 {
				if !value.IsNull() || value.Type().ID() != sqltype.Int4 {
					t.Fatal("lost typed NULL", value)
				}
			} else if n, err := value.Integer(); err != nil || n != -2 || value.Type().ID() != sqltype.Int8 {
				t.Fatal("lost narrow truncation before widening", value)
			}
		}
		sql, err := defaultSQL(context.Background(), replacement.CatalogRead(), rebound)
		if err != nil {
			t.Fatal(err)
		}
		name, target := "w", "BIGINT"
		if attribute == 2 {
			name, target = "n", "INTEGER"
		}
		run(t, replacement, "CREATE TABLE "+name+"(v "+target+" DEFAULT "+sql+")")
		run(t, replacement, "INSERT INTO "+name+" DEFAULT VALUES")
		restoredRef := roleTableRef(t, replacement, name)
		restored := replacement.CatalogRead().state.descriptors[restoredRef.Address].(*relationDescriptor)
		value, err := restored.defaults[1].expression.Eval(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if attribute == 2 {
			if !value.IsNull() {
				t.Fatal("NULL changed on replay", value)
			}
		} else if n, err := value.Integer(); err != nil || n != -2 {
			t.Fatal("width changed on replay", value)
		}
	}
}
