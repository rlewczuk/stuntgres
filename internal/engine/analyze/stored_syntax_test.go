package analyze

import (
	"testing"

	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
)

func TestCreateAggregateStoredSyntaxCastOwnership(t *testing.T) {
	source := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "aggregate_source"}}}, Star: true, Pos: parser.Position{Offset: 7, Character: 8}}
	inner := parser.CastExpr{Child: source, TypeName: parser.TypeName{Name: "text"}, Syntax: parser.PostfixCast, Pos: parser.Position{Offset: 20, Character: 21}}
	outer := parser.CastExpr{Child: inner, TypeName: parser.TypeName{Name: "varchar"}, Syntax: parser.PostfixCast, Pos: parser.Position{Offset: 31, Character: 32}}
	if source.SourcePosition() != inner.SourcePosition() || source.SourcePosition() != outer.SourcePosition() {
		t.Fatal("fixture does not exercise shared diagnostic source positions")
	}
	t.Run("frozen_child_preserves_postfix_ancestors", func(t *testing.T) {
		b := &Binder{}
		frozen := parser.AggregateExpr{Name: "aggregate_source", Schema: &parser.Identifier{Text: "public"}, Star: true, StoredAggregate: true, Pos: source.Pos}
		b.Stored.Syntax = map[StoredExpressionSyntaxKey]parser.Expr{b.storedExpressionSyntaxKey(source): frozen}
		normalized, ok := b.NormalizeStoredExpression(outer).(parser.CastExpr)
		if !ok || normalized.Syntax != parser.PostfixCast || normalized.Pos != outer.Pos || normalized.TypeName.Name != "varchar" {
			t.Fatal("outer explicit cast was replaced by child lowering", normalized)
		}
		middle, ok := normalized.Child.(parser.CastExpr)
		if !ok || middle.Syntax != parser.PostfixCast || middle.Pos != inner.Pos || middle.TypeName.Name != "text" {
			t.Fatal("inner explicit cast was replaced by child lowering", normalized.Child)
		}
		leaf, ok := middle.Child.(parser.AggregateExpr)
		if !ok || !leaf.StoredAggregate || leaf.Name != frozen.Name || !leaf.Star || leaf.Schema == nil || leaf.Schema.Text != "public" {
			t.Fatal("selected child recipe was not normalized", middle.Child)
		}
		if _, ok := inner.Child.(parser.FunctionExpr); !ok {
			t.Fatal("shared original source syntax was mutated")
		}
		b.Query.Frame = 2
		other := b.NormalizeStoredExpression(outer).(parser.CastExpr).Child.(parser.CastExpr)
		if _, ok := other.Child.(parser.FunctionExpr); !ok {
			t.Fatal("source lowering leaked into another lexical frame")
		}
	})
	t.Run("constructor_cast_erasure_owns_cast_token", func(t *testing.T) {
		b := &Binder{}
		row := parser.RowExpr{}
		if _, err := b.normalizeQuantifiedConstructor(inner, plan.RowExpr{Raw: &row}); err != nil {
			t.Fatal(err)
		}
		if b.storedExpressionSyntaxKey(inner) == b.storedExpressionSyntaxKey(source) {
			t.Fatal("cast erasure aliases the child source occurrence")
		}
		normalized, ok := b.NormalizeStoredExpression(outer).(parser.CastExpr)
		if !ok || normalized.Pos != outer.Pos || normalized.TypeName.Name != "varchar" {
			t.Fatal("deliberate inner lowering erased the outer cast", normalized)
		}
		if _, ok := normalized.Child.(parser.RowExpr); !ok {
			t.Fatal("existing constructor cast erasure was lost", normalized.Child)
		}
		if _, ok := inner.Child.(parser.FunctionExpr); !ok {
			t.Fatal("constructor normalization mutated shared original syntax")
		}
	})
}
