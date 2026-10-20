package engine

import (
	"context"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqlstate"
	"testing"

	"stuntgres/internal/engine/functions"
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
	node, ok := bound.(functions.Format)
	if !ok || node.ResultUsage().Type.ID() != sqltype.Text || node.ResultUsage().Typmod != -1 || node.Properties().Volatility != expr.Stable || PlannedVolatility(node) != expr.Stable {
		t.Fatalf("format metadata: %#v", bound)
	}
	for _, rewrite := range []struct {
		name string
		run  func() (expr.Expr, bool, error)
	}{
		{"preflight", func() (expr.Expr, bool, error) {
			return preflightCoercions(&EvalContext{evalStatement: evalStatement{Context: ctx}}, node)
		}},
		{"conditional", func() (expr.Expr, bool, error) {
			return simplifyConditional(&EvalContext{evalStatement: evalStatement{Context: ctx}}, node, false)
		}},
	} {
		got, changed, err := rewrite.run()
		if err != nil || changed {
			t.Fatalf("%s consumed runtime format: %v/%v", rewrite.name, changed, err)
		}
		if _, ok := got.(functions.Format); !ok {
			t.Fatalf("lost format node: %T", got)
		}
	}
	if _, err := node.Eval(&EvalContext{evalStatement: evalStatement{Context: ctx}}); err == nil {
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
	renderer := newViewRenderer(&viewDefinition{literals: b.capture.literals, castUsages: b.capture.castUsages})
	renderer.frame = b.frameID()
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
	value, err := bound.Eval(&EvalContext{evalStatement: evalStatement{Context: ctx}})
	if err != nil {
		t.Fatal(stored, err)
	}
	text, err := value.Text()
	if err != nil || text != "b/a" {
		t.Fatal(text, err)
	}
	t.Run("BoundUnknownAnyReplay", func(t *testing.T) {
		for _, tc := range []struct{ sql, want string }{
			{`SELECT format('%I','foo')`, `format('%I'::text, 'foo')`},
			{`SELECT format('%I','foo'::text)`, `format('%I'::text, 'foo'::text)`},
		} {
			script, err := parser.Parse(tc.sql)
			if err != nil {
				t.Fatal(err)
			}
			binder := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
			node, err := binder.bind(script[0].(*parser.Select).Targets[0], expressionPolicy{context: projectionContext})
			if err != nil {
				t.Fatal(err)
			}
			stored, err := (scalarRenderer{}).sql(ctx, e.CatalogRead(), node)
			if err != nil {
				t.Fatal(err)
			}
			replay, err := parser.Parse("SELECT " + stored)
			if err != nil {
				t.Fatal(stored, err)
			}
			rebound, err := binder.bind(replay[0].(*parser.Select).Targets[0], expressionPolicy{context: projectionContext})
			if err != nil {
				t.Fatal(stored, err)
			}
			for _, current := range []expr.Expr{node, rebound} {
				rendered, err := (pgRenderer{view: e.CatalogRead()}).sql(ctx, current, pgRenderParent{})
				if err != nil || rendered != tc.want {
					t.Fatal("ANY provenance/replay deparse", rendered, tc.want, err)
				}
			}
		}
	})
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
		_, err = bound.Eval(&EvalContext{evalStatement: evalStatement{Context: ctx}})
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
	node := bound.(functions.Format)
	other := node
	other.Variadic = true
	if !b.sameGroupExpression(node, node) || b.sameGroupExpression(node, other) {
		t.Fatal("VARIADIC participates in grouping identity")
	}
	other = node
	other.Args = []expr.Expr{node.Args[1], node.Args[0]}
	if b.sameGroupExpression(node, other) {
		t.Fatal("ordered argument identity")
	}
	types := formatUnitTypes()
	retained, changed, err := retagBoundTypes(b.bindingCatalog(), node, func(d *sqltype.Descriptor) *sqltype.Descriptor { return types[d.ID()] })
	if err != nil || !changed {
		t.Fatal(changed, err)
	}
	fresh := retained.(functions.Format)
	if fresh.RawUnknown != node.RawUnknown || fresh.Int4 != types[sqltype.Int4] || fresh.ResultUsage().Type != types[sqltype.Text] || fresh.Args[0].ResultUsage().Type != types[sqltype.Text] || node.Int4 == fresh.Int4 || node.Args[0].ResultUsage().Type == fresh.Args[0].ResultUsage().Type {
		t.Fatal("retag must own a new immutable descriptor generation")
	}
	value, err := fresh.Eval(&EvalContext{evalStatement: evalStatement{Context: ctx}})
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
	node := bound.(functions.Format)
	types := formatUnitTypes()
	retained, changed, err := retagBoundTypes(b.bindingCatalog(), node, func(d *sqltype.Descriptor) *sqltype.Descriptor { return types[d.ID()] })
	if err != nil || !changed {
		t.Fatal(changed, err)
	}
	fresh := retained.(functions.Format)
	if fresh.Args[1].ResultUsage().Type.BaseType().Element() != types[sqltype.Name] || node.Args[1].ResultUsage().Type == fresh.Args[1].ResultUsage().Type || fresh.Args[1].ResultUsage().Type != types[sqltype.NameArray] {
		t.Fatal("VARIADIC descriptors not retained")
	}
	value, err := fresh.Eval(&EvalContext{evalStatement: evalStatement{Context: ctx}})
	if err != nil {
		t.Fatal(err)
	}
	text, err := value.Text()
	if err != nil || text != "b/a" {
		t.Fatal(text, err)
	}
	column := plan.ColumnExpr{Info: expr.Info{Usage: node.ResultUsage(), Pos: parser.Position{Character: 99}, Property: expr.Properties{RowDependent: true}}, Scope: 1, Frame: b.frameID()}
	node.Variadic = false
	node.Args = []expr.Expr{node.Args[0], column}
	node.Property.RowDependent = true
	if generationImmutable(node) || firstQueryBoundFrameVariable(node, b.frameID()).Character != 99 {
		t.Fatal("stable/variable descendants must remain visible")
	}
	rebound, err := b.rewriteGroup(node, []expr.Expr{column})
	if err != nil {
		t.Fatal(err)
	}
	grouped := rebound.(functions.Format)
	if _, ok := grouped.Args[1].(plan.GroupExpr); !ok {
		t.Fatalf("group descendant not rewritten: %T", grouped.Args[1])
	}
	if _, ok := node.Args[1].(plan.ColumnExpr); !ok {
		t.Fatal("group rewrite mutated shared children")
	}
	aggregate := plan.AggregateExpr{Info: expr.Info{Usage: node.ResultUsage(), Property: expr.Properties{StageDependent: true}}, Slot: 7}
	node.Args = []expr.Expr{node.Args[0], column, aggregate}
	if found := aggregateOccurrence(node); found == nil || found.Slot != 7 {
		t.Fatal("all arguments must participate in aggregate traversal")
	}
}

func TestFormatParameterDependence(t *testing.T) {
	ctx := context.Background()
	b := newTestExpressionBinder(t, ctx, New(), sqltype.InputEnvironment{}, nil)
	bound, err := b.bind(nullCountRaw("format", parser.ParameterExpr{Number: 1}, parser.ParameterExpr{Number: 1}), expressionPolicy{context: projectionContext})
	if err != nil || !bound.Properties().ParameterDependent || bound.Properties().Volatility != expr.Stable {
		t.Fatal(bound, err)
	}
}
