package engine

import (
	"context"
	"stuntgres/internal/sqlstate"
	"testing"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func formatUnitLiteral(text string) parser.Expr {
	return parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: text}}
}

func TestFormatBoundMetadataAndRuntimeOwnership(t *testing.T) {
	ctx := context.Background()
	b := newTestExpressionBinder(t, ctx, New(), sqltype.InputEnvironment{}, nil)
	raw := nullCountRaw("format", formatUnitLiteral("%d"))
	bound, err := b.bind(raw, expressionPolicy{context: projectionContext})
	if err != nil {
		t.Fatal(err)
	}
	node, ok := bound.(formatExpression)
	if !ok || node.ResultUsage().Type.ID() != sqltype.Text || node.ResultUsage().Typmod != -1 || node.Properties().Volatility != Stable || plannedVolatility(node) != Stable {
		t.Fatalf("format metadata: %#v", bound)
	}
	for _, rewrite := range []struct {
		name string
		run  func() (BoundExpr, bool, error)
	}{
		{"preflight", func() (BoundExpr, bool, error) { return preflightCoercions(&EvalContext{Context: ctx}, node) }},
		{"conditional", func() (BoundExpr, bool, error) { return simplifyConditional(&EvalContext{Context: ctx}, node, false) }},
	} {
		got, changed, err := rewrite.run()
		if err != nil || changed {
			t.Fatalf("%s consumed runtime format: %v/%v", rewrite.name, changed, err)
		}
		if _, ok := got.(formatExpression); !ok {
			t.Fatalf("lost format node: %T", got)
		}
	}
	if _, err := node.Eval(&EvalContext{Context: ctx}); err == nil {
		t.Fatal("runtime must reject the unsupported integer format verb")
	}
	query, err := b.bindSelect(&parser.Select{Targets: []parser.Expr{nullCountRaw("format", formatUnitLiteral("%s"), formatUnitLiteral("x"))}}, false)
	if err != nil {
		t.Fatal(err)
	}
	columns := query.Root.Schema()
	if len(columns) != 1 || columns[0].Name != "format" || columns[0].Usage.Type.ID() != 25 || columns[0].Usage.Typmod != -1 || columns[0].Usage.Collation.ID != 100 || columns[0].OriginTable != 0 || columns[0].OriginAttribute != 0 || columns[0].NullabilityKnown {
		t.Fatalf("computed TEXT has no physical origin or invented nullable metadata: %#v", columns)
	}
}

func TestFormatExactDerivedCollationMetadata(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		oid  uint32
	}{{"C", 950}, {"pg_unicode_fast", 6411}} {
		b := newTestExpressionBinder(t, ctx, New(), sqltype.InputEnvironment{}, nil)
		operand := parser.CollateExpr{Child: formatUnitLiteral("É"), Name: parser.ObjectName{Parts: []parser.Identifier{{Text: tc.name}}}}
		query, err := b.bindSelect(&parser.Select{Targets: []parser.Expr{nullCountRaw("format", formatUnitLiteral("%s"), operand)}}, false)
		if err != nil {
			t.Fatal(tc.name, err)
		}
		columns := query.Root.Schema()
		if len(columns) != 1 || columns[0].Usage.Type.ID() != 25 || columns[0].Usage.Typmod != -1 || columns[0].Usage.Collation.ID != tc.oid || columns[0].OriginTable != 0 || columns[0].OriginAttribute != 0 {
			t.Fatal(tc.name, columns)
		}
	}
}

func TestFormatViewVariadicSerializationReplay(t *testing.T) {
	ctx := context.Background()
	e := New()
	script, err := parser.Parse("SELECT format('%2$s/%1$s',VARIADIC '{a,b}'::name[])")
	if err != nil {
		t.Fatal(err)
	}
	raw := script[0].(*parser.Select).Targets[0]
	b := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
	original, err := b.bind(raw, expressionPolicy{context: projectionContext})
	if err != nil {
		t.Fatal(err)
	}
	if err := b.captureViewLiterals(raw, original, viewTargetLiteral, 0); err != nil {
		t.Fatal(err)
	}
	renderer := newViewRenderer(&viewDefinition{literals: b.viewLiterals, domainCasts: b.viewDomainCasts})
	stored, err := renderer.expressionSQL(ctx, e.CatalogRead(), raw, viewLiteralPath{clause: viewTargetLiteral})
	if err != nil {
		t.Fatal(err)
	}
	replay, err := parser.Parse("SELECT " + stored)
	if err != nil {
		t.Fatal(stored, err)
	}
	call := replay[0].(*parser.Select).Targets[0].(parser.FunctionExpr)
	if !call.Variadic {
		t.Fatalf("lost raw VARIADIC in %s", stored)
	}
	bound, err := b.bind(call, expressionPolicy{context: projectionContext})
	if err != nil {
		t.Fatal(err)
	}
	value, err := bound.Eval(&EvalContext{Context: ctx})
	if err != nil {
		t.Fatal(stored, err)
	}
	text, err := value.Text()
	if err != nil || text != "b/a" {
		t.Fatal(text, err)
	}
}

func TestFormatNullAndUnusedDoNotHideRealChildErrors(t *testing.T) {
	ctx := context.Background()
	for _, nullPattern := range []bool{false, true} {
		b := newTestExpressionBinder(t, ctx, New(), sqltype.InputEnvironment{}, nil)
		pattern := formatUnitLiteral("constant")
		if nullPattern {
			pattern = parser.LiteralExpr{Value: parser.Value{Kind: parser.NullValue}}
		}
		divide := parser.OperatorExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "/"}}}, Args: []parser.Expr{
			parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: 1}},
			parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: 0}},
		}}
		bound, err := b.bind(nullCountRaw("format", pattern, divide), expressionPolicy{context: projectionContext})
		if err != nil {
			t.Fatal(err)
		}
		_, err = bound.Eval(&EvalContext{Context: ctx})
		nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "22012", Message: "division by zero"})
	}
}

func TestFormatNodeIdentityAndRetainedGeneration(t *testing.T) {
	ctx := context.Background()
	b := newTestExpressionBinder(t, ctx, New(), sqltype.InputEnvironment{}, nil)
	bound, err := b.bind(nullCountRaw("format", formatUnitLiteral("%s"), formatUnitLiteral("x")), expressionPolicy{context: projectionContext})
	if err != nil {
		t.Fatal(err)
	}
	node := bound.(formatExpression)
	other := node
	other.variadic = true
	if !b.sameGroupExpression(node, node) || b.sameGroupExpression(node, other) {
		t.Fatal("VARIADIC participates in grouping identity")
	}
	other = node
	other.args = []BoundExpr{node.args[1], node.args[0]}
	if b.sameGroupExpression(node, other) {
		t.Fatal("ordered argument identity")
	}
	types := formatUnitTypes()
	retained, changed, err := retagBoundTypes(node, func(d *sqltype.Descriptor) *sqltype.Descriptor { return types[d.ID()] })
	if err != nil || !changed {
		t.Fatal(changed, err)
	}
	fresh := retained.(formatExpression)
	if fresh.int4 != types[sqltype.Int4] || fresh.ResultUsage().Type != types[sqltype.Text] || fresh.args[0].ResultUsage().Type != types[sqltype.Text] || node.int4 == fresh.int4 || node.args[0].ResultUsage().Type == fresh.args[0].ResultUsage().Type {
		t.Fatal("retag must own a new immutable descriptor generation")
	}
	value, err := fresh.Eval(&EvalContext{Context: ctx})
	if err != nil {
		t.Fatal(err)
	}
	text, err := value.Text()
	if err != nil || text != "x" {
		t.Fatal(text, err)
	}
}

func TestFormatVariadicRetentionAndSharedChildWalks(t *testing.T) {
	ctx := context.Background()
	b := newTestExpressionBinder(t, ctx, New(), sqltype.InputEnvironment{}, nil)
	raw := nullCountRaw("format", formatUnitLiteral("%2$s/%1$s"), parser.CastExpr{Child: formatUnitLiteral("{a,b}"), TypeName: parser.TypeName{Name: "name", ArrayBounds: []int32{0}}})
	raw.Variadic = true
	bound, err := b.bind(raw, expressionPolicy{context: projectionContext})
	if err != nil {
		t.Fatal(err)
	}
	node := bound.(formatExpression)
	types := formatUnitTypes()
	retained, changed, err := retagBoundTypes(node, func(d *sqltype.Descriptor) *sqltype.Descriptor { return types[d.ID()] })
	if err != nil || !changed {
		t.Fatal(changed, err)
	}
	fresh := retained.(formatExpression)
	if fresh.args[1].ResultUsage().Type.BaseType().Element() != types[sqltype.Name] || node.args[1].ResultUsage().Type == fresh.args[1].ResultUsage().Type || fresh.args[1].ResultUsage().Type != types[sqltype.NameArray] {
		t.Fatal("VARIADIC descriptors not retained")
	}
	value, err := fresh.Eval(&EvalContext{Context: ctx})
	if err != nil {
		t.Fatal(err)
	}
	text, err := value.Text()
	if err != nil || text != "b/a" {
		t.Fatal(text, err)
	}
	column := columnExpression{expressionInfo: expressionInfo{usage: node.ResultUsage(), pos: parser.Position{Character: 99}, properties: ExprProperties{RowDependent: true}}}
	node.variadic = false
	node.args = []BoundExpr{node.args[0], column}
	node.properties.RowDependent = true
	if generationImmutable(node) || firstQueryBoundVariable(node).Character != 99 {
		t.Fatal("stable/variable descendants must remain visible")
	}
	rebound, err := b.rewriteGroup(node, []BoundExpr{column})
	if err != nil {
		t.Fatal(err)
	}
	grouped := rebound.(formatExpression)
	if _, ok := grouped.args[1].(groupExpression); !ok {
		t.Fatalf("group descendant not rewritten: %T", grouped.args[1])
	}
	if _, ok := node.args[1].(columnExpression); !ok {
		t.Fatal("group rewrite mutated shared children")
	}
	aggregate := aggregateExpression{expressionInfo: expressionInfo{usage: node.ResultUsage(), properties: ExprProperties{StageDependent: true}}, slot: 7}
	node.args = []BoundExpr{node.args[0], column, aggregate}
	if found := aggregateOccurrence(node); found == nil || found.slot != 7 {
		t.Fatal("all arguments must participate in aggregate traversal")
	}
}

func TestFormatParameterDependence(t *testing.T) {
	ctx := context.Background()
	b := newTestExpressionBinder(t, ctx, New(), sqltype.InputEnvironment{}, nil)
	bound, err := b.bind(nullCountRaw("format", parser.ParameterExpr{Number: 1}, parser.ParameterExpr{Number: 1}), expressionPolicy{context: projectionContext})
	if err != nil || !bound.Properties().ParameterDependent || bound.Properties().Volatility != Stable {
		t.Fatal(bound, err)
	}
}
