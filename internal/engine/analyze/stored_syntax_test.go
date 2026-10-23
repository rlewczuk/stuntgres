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

func TestStoredProviderSyntaxGeneratedOccurrences(t *testing.T) {
	b := &Binder{}
	call := parser.FunctionExpr{
		Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "unnest"}}},
		Pos:  parser.Position{Offset: 7, Character: 8},
		Args: []parser.Expr{
			parser.ColumnExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "a"}}}, Pos: parser.Position{Offset: 14, Character: 15}},
			parser.ColumnExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "b"}}}, Pos: parser.Position{Offset: 17, Character: 18}},
		},
	}
	from := &parser.FunctionTable{Calls: []parser.FunctionExpr{call}, Ordinality: true}
	unary := parser.NormalizedFunctionCalls(from)
	if len(unary) != 2 || b.storedExpressionSyntaxKey(unary[0]) == b.storedExpressionSyntaxKey(unary[1]) {
		t.Fatal("generated unary syntax occurrences collide")
	}
	b.Stored.Syntax = make(map[StoredExpressionSyntaxKey]parser.Expr)
	for _, raw := range unary {
		frozen := raw
		frozen.StoredProviderRoutine, frozen.StoredProviderSymbol = true, "unnest"
		b.Stored.Syntax[b.storedExpressionSyntaxKey(raw)] = frozen
	}
	normalized := b.normalizeInListFrom(from).(*parser.FunctionTable)
	if len(normalized.Calls) != 2 || !normalized.Ordinality || len(from.Calls) != 1 || len(from.Calls[0].Args) != 2 {
		t.Fatal("FROM normalization lost occurrence boundaries or changed source")
	}
	for i, raw := range normalized.Calls {
		if !raw.StoredProviderRoutine || raw.StoredProviderSymbol != "unnest" || len(raw.Args) != 1 || raw.Args[0].SourcePosition() != call.Args[i].SourcePosition() {
			t.Fatal("provider marker lost its own unary operand", i)
		}
	}
	b.Query.Frame = 2
	other := b.normalizeInListFrom(from).(*parser.FunctionTable)
	for _, raw := range other.Calls {
		if raw.StoredProviderRoutine {
			t.Fatal("provider marker crossed query frames")
		}
	}
}

func TestStoredProviderSyntaxSubstitutedRoot(t *testing.T) {
	b := &Binder{}
	input := parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: 1, Pos: parser.Position{Offset: 12, Character: 13}}}
	raw := parser.CastExpr{Child: input, Syntax: parser.PostfixCast, Pos: parser.Position{Offset: 14, Character: 15}}
	call := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "cast_provider"}}}, Args: []parser.Expr{input}, Pos: raw.Pos}
	marked := call
	marked.StoredProviderRoutine, marked.StoredProviderSymbol = true, "cast_provider"
	b.Stored.Syntax = map[StoredExpressionSyntaxKey]parser.Expr{
		b.storedExpressionSyntaxKey(raw):  call,
		b.storedExpressionSyntaxKey(call): marked,
	}
	got, ok := b.NormalizeStoredExpression(raw).(parser.FunctionExpr)
	if !ok || !got.StoredProviderRoutine || got.StoredProviderSymbol != "cast_provider" || len(got.Args) != 1 {
		t.Fatal("substituted root did not receive its checked provider annotation")
	}
	if len(b.normalizingSyntax) != 0 || call.StoredProviderRoutine {
		t.Fatal("normalization leaked its guard or mutated shared source")
	}
}

func TestStoredProviderSyntaxOwnedAggregateOccurrence(t *testing.T) {
	b := &Binder{}
	input := parser.ColumnExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "x"}}}, Pos: parser.Position{Offset: 19, Character: 20}}
	source := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "owned_sum"}}}, Args: []parser.Expr{input}, Pos: parser.Position{Offset: 9, Character: 10}}
	selected := parser.AggregateExpr{Name: "owned_sum", Args: []parser.Expr{parser.ParameterExpr{Number: 1}}, Pos: source.Pos}
	frozen := selected
	frozen.Args, frozen.StoredAggregate = source.Args, true
	frozen.Schema = &parser.Identifier{Text: "public"}
	if b.storedExpressionSyntaxKey(source) != b.storedExpressionSyntaxKey(frozen) {
		t.Fatal("selected aggregate lost its written call occurrence")
	}
	if b.storedExpressionSyntaxKey(selected) == b.storedExpressionSyntaxKey(frozen) {
		t.Fatal("binder-local slots alias the written syntax key")
	}
	b.Stored.Syntax = map[StoredExpressionSyntaxKey]parser.Expr{b.storedExpressionSyntaxKey(frozen): frozen}
	got, ok := b.NormalizeStoredExpression(source).(parser.AggregateExpr)
	if !ok || !got.StoredAggregate || got.StoredProviderRoutine || got.Schema == nil || got.Schema.Text != "public" || len(got.Args) != 1 || got.Args[0].SourcePosition() != input.Pos {
		t.Fatal("owned aggregate normalized through ordinary provider syntax", got)
	}
	if _, ok := source.Args[0].(parser.ColumnExpr); !ok || len(b.normalizingSyntax) != 0 {
		t.Fatal("normalization changed written operands or retained its guard")
	}
	zero := parser.FunctionExpr{Name: source.Name, Star: true, Pos: source.Pos}
	zeroAggregate := parser.AggregateExpr{Name: selected.Name, Star: true, Pos: source.Pos}
	if b.storedExpressionSyntaxKey(zero) != b.storedExpressionSyntaxKey(zeroAggregate) {
		t.Fatal("zero-input aggregate changed its call occurrence")
	}
}

func TestStoredProviderSyntaxFunctionCastOccurrence(t *testing.T) {
	b := &Binder{}
	input := parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: 1, Pos: parser.Position{Offset: 19, Character: 20}}}
	raw := parser.CastExpr{Child: input, Syntax: parser.FunctionCast, Callee: "int4", TypeName: parser.TypeName{Name: "int4", Schema: &parser.Identifier{Text: "public"}}, Pos: parser.Position{Offset: 7, Character: 8}}
	frozen := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "public"}, {Text: "int4"}}}, Args: []parser.Expr{input}, Pos: raw.Pos, StoredSQLRoutine: true}
	if b.storedExpressionSyntaxKey(raw) != b.storedExpressionSyntaxKey(frozen) {
		t.Fatal("legacy function syntax lost the selected SQL call occurrence")
	}
	if got := b.NormalizeStoredExpression(raw); got.(parser.CastExpr).Syntax != parser.FunctionCast {
		t.Fatal("unannotated typename shortcut became a routine")
	}
	b.Stored.Syntax = map[StoredExpressionSyntaxKey]parser.Expr{b.storedExpressionSyntaxKey(frozen): frozen}
	got, ok := b.NormalizeStoredExpression(raw).(parser.FunctionExpr)
	if !ok || !got.StoredSQLRoutine || got.StoredProviderRoutine || len(got.Args) != 1 || got.Args[0].SourcePosition() != input.SourcePosition() {
		t.Fatal("selected SQL call was reconstructed as a builtin cast", got)
	}
	outer := parser.CastExpr{Child: raw, Syntax: parser.PostfixCast, TypeName: parser.TypeName{Name: "text"}, Pos: parser.Position{Offset: 21, Character: 22}}
	if b.storedExpressionSyntaxKey(outer) == b.storedExpressionSyntaxKey(raw) {
		t.Fatal("enclosing postfix cast aliases the function call")
	}
	normalized := b.NormalizeStoredExpression(outer).(parser.CastExpr)
	if child, ok := normalized.Child.(parser.FunctionExpr); !ok || !child.StoredSQLRoutine || normalized.Syntax != parser.PostfixCast {
		t.Fatal("function lowering erased the enclosing cast", normalized)
	}
	b.Query.Frame = 2
	if _, ok := b.NormalizeStoredExpression(raw).(parser.CastExpr); !ok || len(b.normalizingSyntax) != 0 {
		t.Fatal("call annotation crossed query frames or retained its guard")
	}
}
