package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

func TestBitwiseDefaultValidationAndRetag(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE TABLE d(i INTEGER DEFAULT (1 & 3),s SMALLINT DEFAULT (1::smallint << 15),n SMALLINT DEFAULT (~NULL::smallint))")
	view := e.CatalogRead()
	ref := enginetest.RoleTableRef(t, e, "d")
	old := view.MustRelation(ref.Address)
	if err := (core.RelationProvider).Validate(view, old); err != nil {
		t.Fatal(err)
	}
	root := old.ColumnMeta.Defaults[1].Expression.(plan.BitwiseExpr)
	for _, mutate := range []func(plan.BitwiseExpr) plan.BitwiseExpr{
		func(n plan.BitwiseExpr) plan.BitwiseExpr { n.Left = nil; return n },
		func(n plan.BitwiseExpr) plan.BitwiseExpr { n.Right = nil; return n },
		func(n plan.BitwiseExpr) plan.BitwiseExpr { n.Operator = sqltype.BitwiseOperator(255); return n },
		func(n plan.BitwiseExpr) plan.BitwiseExpr { n.Operator = sqltype.BitwiseOr; return n },
		func(n plan.BitwiseExpr) plan.BitwiseExpr { n.Provider = sqltype.Bitwise{}; return n },
		func(n plan.BitwiseExpr) plan.BitwiseExpr { n.Right = old.ColumnMeta.Defaults[2].Expression; return n },
		func(n plan.BitwiseExpr) plan.BitwiseExpr {
			l := n.Left.(plan.LiteralExpr)
			l.Property.RowDependent = true
			n.Left = l
			return n
		},
		func(n plan.BitwiseExpr) plan.BitwiseExpr { n.Right = plan.LiteralExpr{}; return n },
		func(n plan.BitwiseExpr) plan.BitwiseExpr {
			foreign := engine.New()
			typ, _ := foreign.CatalogRead().LookupType(sqltype.Int4)
			value, _ := sqltype.Integer(typ, 1)
			n.Left = plan.LiteralExpr{Info: expr.Info{Usage: typ.Usage()}, Value: value}
			return n
		},
	} {
		if _, err := stored.DefaultSQL(context.Background(), view, mutate(root)); err == nil {
			t.Fatal("invalid bitwise default accepted")
		}
	}
	shift := old.ColumnMeta.Defaults[2].Expression.(plan.BitwiseExpr)
	shift.Right = old.ColumnMeta.Defaults[3].Expression
	if _, err := stored.DefaultSQL(context.Background(), view, shift); err == nil {
		t.Fatal("SMALLINT bound shift count accepted")
	}
	ctx := &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}}
	ordinary, changed, err := exec.PreflightCoercions(ctx, root)
	if err != nil || changed {
		t.Fatal(ordinary, changed, err)
	}
	if _, ok := ordinary.(plan.BitwiseExpr); !ok {
		t.Fatal("ordinary node folded")
	}
	folded, changed, err := exec.PreflightCoercionsWithPolicy(ctx, root, true)
	if err != nil || !changed {
		t.Fatal(folded, changed, err)
	}
	v, err := folded.Eval(ctx)
	n, _ := v.Integer()
	if err != nil || n != 1 {
		t.Fatal(v, err)
	}
	if _, ok := old.ColumnMeta.Defaults[1].Expression.(plan.BitwiseExpr); !ok {
		t.Fatal("catalog mutated")
	}
	replacement := engine.New()
	retag := func(d *sqltype.Descriptor) *sqltype.Descriptor {
		x, _ := replacement.CatalogRead().LookupType(d.ID())
		return x
	}
	mapper := stored.ViewQueryRemapper{Catalog: replacement.CatalogRead(), Type: func(d *sqltype.Descriptor) *sqltype.Descriptor { return d }, Procedure: replacementProcedurePolicyFixture(e, replacement)}
	for _, attribute := range []int32{1, 2, 3} {
		original := old.ColumnMeta.Defaults[attribute].Expression
		rebound, changed, err := stored.RetagTypes(replacement.CatalogRead(), original, retag)
		if err != nil || !changed || rebound.ResultUsage().Type == original.ResultUsage().Type {
			t.Fatal(rebound, changed, err)
		}
		query, err := mapper.Query(&plan.ValuesNode{Rows: [][]expr.Expr{{rebound}}})
		if err != nil {
			t.Fatal(err)
		}
		rebound = query.(*plan.ValuesNode).Rows[0][0]
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
		if _, err := stored.DefaultSQL(context.Background(), replacement.CatalogRead(), rebound); err != nil {
			t.Fatal(err)
		}
	}
}

func TestBitwiseScopedCastDefaultSerializationAndRetag(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE TABLE d(w BIGINT DEFAULT CAST(32767::smallint << 1::smallint AS bigint),n INTEGER DEFAULT CAST(~NULL::smallint AS integer))")
	view := e.CatalogRead()
	ref := enginetest.RoleTableRef(t, e, "d")
	table := view.MustRelation(ref.Address)
	replacement := engine.New()
	retag := func(d *sqltype.Descriptor) *sqltype.Descriptor {
		typ, _ := replacement.CatalogRead().LookupType(d.ID())
		return typ
	}
	mapper := stored.ViewQueryRemapper{Catalog: replacement.CatalogRead(), Type: func(d *sqltype.Descriptor) *sqltype.Descriptor { return d }, Procedure: replacementProcedurePolicyFixture(e, replacement)}
	ctx := &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}}
	for _, attribute := range []int32{1, 2} {
		original := table.ColumnMeta.Defaults[attribute].Expression
		rebound, changed, err := stored.RetagTypes(replacement.CatalogRead(), original, retag)
		if err != nil || !changed {
			t.Fatal(rebound, changed, err)
		}
		query, err := mapper.Query(&plan.ValuesNode{Rows: [][]expr.Expr{{rebound}}})
		if err != nil {
			t.Fatal(err)
		}
		rebound = query.(*plan.ValuesNode).Rows[0][0]
		for _, node := range []expr.Expr{original, rebound} {
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
		sql, err := stored.DefaultSQL(context.Background(), replacement.CatalogRead(), rebound)
		if err != nil {
			t.Fatal(err)
		}
		name, target := "w", "BIGINT"
		if attribute == 2 {
			name, target = "n", "INTEGER"
		}
		enginetest.Run(t, replacement, "CREATE TABLE "+name+"(v "+target+" DEFAULT "+sql+")")
		enginetest.Run(t, replacement, "INSERT INTO "+name+" DEFAULT VALUES")
		restoredRef := enginetest.RoleTableRef(t, replacement, name)
		restored := replacement.CatalogRead().MustRelation(restoredRef.Address)
		value, err := restored.ColumnMeta.Defaults[1].Expression.Eval(ctx)
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

func TestBitwiseConstantNullDefaultRemainsAnalyzed(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE TABLE null_default(v INTEGER DEFAULT (NULL::integer & 1))")
	view := e.CatalogRead()
	ref := enginetest.RoleTableRef(t, e, "null_default")
	table := view.MustRelation(ref.Address)
	original := table.ColumnMeta.Defaults[1].Expression
	before, err := stored.DefaultSQL(context.Background(), view, original)
	if err != nil {
		t.Fatal(err)
	}
	ctx := &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}}
	planned, changed, err := exec.PreflightCoercionsWithPolicy(ctx, original, true)
	if err != nil || !changed {
		t.Fatal(planned, changed, err)
	}
	if value, err := planned.Eval(ctx); err != nil || !value.IsNull() || value.Type() != original.ResultUsage().Type {
		t.Fatal(value, err)
	}
	if _, ok := table.ColumnMeta.Defaults[1].Expression.(plan.BitwiseExpr); !ok {
		t.Fatal("stored default replaced")
	}
	after, err := stored.DefaultSQL(context.Background(), view, table.ColumnMeta.Defaults[1].Expression)
	if err != nil || before != after {
		t.Fatal(before, after, err)
	}
	if err := (core.RelationProvider).Validate(view, table); err != nil {
		t.Fatal(err)
	}
}
