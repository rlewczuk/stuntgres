package catalogrt

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestCatalogSourceRejectsInputProof(t *testing.T) {
	var boolean *sqltype.Descriptor
	for _, typ := range sqltype.Builtins() {
		if typ.ID() == sqltype.Bool {
			boolean = typ
		}
	}
	if boolean == nil {
		t.Fatal("missing builtin Boolean")
	}
	domain := &ids.Domain{}
	ref := domain.Ref(1, ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 12248})
	leaves := []exec.Leaf{{Ref: ref, Table: &parser.TableReference{}}}
	source := &plan.SourceNode{Ref: ref}
	literal := func(value sqltype.Value) expr.Expr {
		return plan.LiteralExpr{Info: expr.Info{Usage: boolean.Usage()}, Value: value}
	}
	root := func(predicate expr.Expr) *plan.ProjectNode {
		return &plan.ProjectNode{Frame: 1, Child: &plan.FilterNode{Child: source, Predicate: predicate}}
	}
	falseExpr := literal(sqltype.Boolean(boolean, false))
	for _, tc := range []struct {
		name string
		predicate expr.Expr
		rejects bool
	}{
		{"false", falseExpr, true},
		{"null", literal(sqltype.Null(boolean)), true},
		{"true", literal(sqltype.Boolean(boolean, true)), false},
		{"row", plan.ColumnExpr{}, false},
		{"stage", &plan.WindowExpr{}, false},
		{"subquery", plan.SubqueryExpr{}, false},
		{"arbitrary", plan.LogicalExpr{Op: parser.LogicalAnd, Left: falseExpr, Right: falseExpr}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := KeepQuery{Root: root(tc.predicate), Leaves: leaves}
			got, err := q.RejectsInput(t.Context(), sqltype.InputEnvironment{}, sqltype.OutputEnvironment{}, leaves, 0, plan.RowMark{}, nil)
			if err != nil || got != tc.rejects {
				t.Fatal("safe physical proof only", got, err, tc.rejects)
			}
		})
	}
	parameter := plan.ParameterExpr{Info: expr.Info{Usage: boolean.Usage()}, Number: 1}
	q := KeepQuery{Root: root(parameter), Leaves: leaves}
	for _, value := range []sqltype.Value{sqltype.Boolean(boolean, false), sqltype.Boolean(boolean, true), sqltype.Null(boolean)} {
		q.Parameters = []sqltype.Value{value}
		want := value.IsNull()
		if !want {
			truth, err := value.Boolean()
			if err != nil {
				t.Fatal(err)
			}
			want = !truth
		}
		if got, err := q.RejectsInput(t.Context(), sqltype.InputEnvironment{}, sqltype.OutputEnvironment{}, leaves, 0, plan.RowMark{}, nil); err != nil || got != want {
			t.Fatal("current imported parameter, not cached rejection", got, err, want)
		}
	}
	q.Parameters = nil
	if got, err := q.RejectsInput(t.Context(), sqltype.InputEnvironment{}, sqltype.OutputEnvironment{}, leaves, 0, plan.RowMark{}, nil); got || err == nil {
		t.Fatal("missing imported parameter error suppressed", got, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if got, err := q.RejectsInput(ctx, sqltype.InputEnvironment{}, sqltype.OutputEnvironment{}, leaves, 0, plan.RowMark{}, nil); got || !errors.Is(err, context.Canceled) {
		t.Fatal("original context error suppressed", got, err)
	}

	for _, tc := range []struct {
		name string
		change func(*KeepQuery, *plan.ProjectNode, *[]exec.Leaf, *plan.RowMark, **exec.LockContext)
	}{
		{"other occurrence", func(_ *KeepQuery, _ *plan.ProjectNode, in *[]exec.Leaf, _ *plan.RowMark, _ **exec.LockContext) { (*in)[0].Table = &parser.TableReference{} }},
		{"other ref", func(_ *KeepQuery, _ *plan.ProjectNode, in *[]exec.Leaf, _ *plan.RowMark, _ **exec.LockContext) { (*in)[0].Ref.Epoch++ }},
		{"multiple sources", func(_ *KeepQuery, _ *plan.ProjectNode, in *[]exec.Leaf, _ *plan.RowMark, _ **exec.LockContext) { *in = append(*in, (*in)[0]) }},
		{"row mark", func(_ *KeepQuery, _ *plan.ProjectNode, _ *[]exec.Leaf, mark *plan.RowMark, _ **exec.LockContext) { mark.Mode = 1 }},
		{"lock context", func(_ *KeepQuery, _ *plan.ProjectNode, _ *[]exec.Leaf, _ *plan.RowMark, locks **exec.LockContext) { *locks = &exec.LockContext{} }},
		{"different frame", func(_ *KeepQuery, p *plan.ProjectNode, _ *[]exec.Leaf, _ *plan.RowMark, _ **exec.LockContext) { p.Frame++ }},
		{"cte boundary", func(_ *KeepQuery, p *plan.ProjectNode, _ *[]exec.Leaf, _ *plan.RowMark, _ **exec.LockContext) { p.CTEOwner = true }},
		{"rule boundary", func(_ *KeepQuery, p *plan.ProjectNode, _ *[]exec.Leaf, _ *plan.RowMark, _ **exec.LockContext) { p.RuleInvocation = true }},
		{"nested input", func(_ *KeepQuery, p *plan.ProjectNode, _ *[]exec.Leaf, _ *plan.RowMark, _ **exec.LockContext) { p.Child.(*plan.FilterNode).Child = &plan.ProjectNode{Child: source} }},
		{"sample", func(_ *KeepQuery, p *plan.ProjectNode, _ *[]exec.Leaf, _ *plan.RowMark, _ **exec.LockContext) { sampled := *source; sampled.Sample = &plan.TableSample{}; p.Child.(*plan.FilterNode).Child = &sampled }},
		{"source occurrence", func(_ *KeepQuery, p *plan.ProjectNode, _ *[]exec.Leaf, _ *plan.RowMark, _ **exec.LockContext) { copied := *source; copied.Occurrence++; p.Child.(*plan.FilterNode).Child = &copied }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := root(falseExpr)
			q := KeepQuery{Root: p, Leaves: leaves}
			in := append([]exec.Leaf(nil), leaves...)
			var mark plan.RowMark
			var locks *exec.LockContext
			tc.change(&q, p, &in, &mark, &locks)
			if got, err := q.RejectsInput(t.Context(), sqltype.InputEnvironment{}, sqltype.OutputEnvironment{}, in, 0, mark, locks); got || err != nil {
				t.Fatal("unsupported topology must demand ordinary capture", got, err)
			}
		})
	}
}
