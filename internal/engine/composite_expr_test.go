package engine

import (
	"context"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func compositeExprUnitLiteral(value sqltype.Value) expr.Expr {
	return plan.LiteralExpr{Info: expr.Info{Usage: value.Type().Usage()}, Value: value}
}
func compositeExprUnitInteger(t *testing.T, typ *sqltype.Descriptor, n int64) sqltype.Value {
	t.Helper()
	value, err := sqltype.Integer(typ, n)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestCompositeExprFieldAndNull(t *testing.T) {
	e := New()
	view := e.CatalogRead()
	integer, _ := view.LookupType(sqltype.Int4)
	shape, err := sqltype.RecordShape([]sqltype.CompositeMember{{1, "f1", integer.Usage()}, {7, "f2", integer.Usage()}})
	if err != nil {
		t.Fatal(err)
	}
	row := plan.RowExpr{Info: expr.Info{Usage: shape.Usage()}, Fields: []expr.Expr{compositeExprUnitLiteral(compositeExprUnitInteger(t, integer, 7)), compositeExprUnitLiteral(sqltype.Null(integer))}}
	c := &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
	value, err := row.Eval(c)
	if err != nil || value.IsNull() || !value.CompositeField(1).IsNull() {
		t.Fatal("present partially NULL constructor", err)
	}
	selected := plan.FieldExpr{Info: expr.Info{Usage: integer.Usage()}, Base: row, Attribute: 1, Index: 0}
	value, err = selected.Eval(c)
	number, nerr := value.Integer()
	if err != nil || nerr != nil || number != 7 {
		t.Fatal(number, err, nerr)
	}
	// A selected constructor child does not evaluate an unused invalid child.
	row.Fields = []expr.Expr{row.Fields[0], plan.LiteralExpr{}}
	selected.Base = row
	if _, err := selected.Eval(c); err != nil {
		t.Fatal("selected field evaluated sibling", err)
	}
	if _, err := row.Eval(c); err == nil {
		t.Fatal("full constructor omitted sibling")
	}
	row.Fields = []expr.Expr{compositeExprUnitLiteral(sqltype.Null(integer)), compositeExprUnitLiteral(sqltype.Null(integer))}
	row.WholeNames = []string{"f1", "f2"}
	row.PresenceSlot = 1
	row.PresenceFrame = 1
	c.PresenceSlots = []int{1}
	c.Presence = []bool{true}
	if value, err := row.Eval(c); err != nil || value.IsNull() {
		t.Fatal("all-NULL emitted tuple became absent", err)
	}
	c.Presence[0] = false
	if value, err := row.Eval(c); err != nil || !value.IsNull() {
		t.Fatal("null-extended tuple became present", err)
	}
	selected.Base = row
	if value, err := selected.Eval(c); err != nil || !value.IsNull() {
		t.Fatal("direct field ignored occurrence absence", err)
	}
	outer := &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 2, Outer: c}}
	if value, err := row.Eval(outer); err != nil || !value.IsNull() {
		t.Fatal("whole row lost outer frame presence", err)
	}
	c.PresenceSlots = nil
	if _, err := row.Eval(c); err == nil {
		t.Fatal("missing demanded occurrence was treated as present")
	}
	empty, err := sqltype.RecordShape(nil)
	if err != nil {
		t.Fatal(err)
	}
	zero := plan.RowExpr{Info: expr.Info{Usage: empty.Usage()}, WholeNames: []string{}, PresenceSlot: 1, PresenceFrame: 1}
	c.PresenceSlots = []int{1}
	c.Presence = []bool{true}
	if value, err := zero.Eval(c); err != nil || value.IsNull() || value.CompositeLen() != 0 {
		t.Fatal("present zero-width occurrence", err)
	}
	c.Presence[0] = false
	if value, err := zero.Eval(c); err != nil || !value.IsNull() {
		t.Fatal("absent zero-width occurrence", err)
	}
	source := &exec.Source{}
	exec.EstablishSourcePresence(source, 0)
	if source.PresenceCount != 0 || source.PresenceSlots != nil || source.PresenceConstant != nil {
		t.Fatal("undemanded source allocated presence")
	}
	t.Run("discarded immutable preflight", func(t *testing.T) {
		node := preflightCast(t, "SELECT (ROW(7,1/0)).f1")
		if _, _, err := preflightCoercions(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}}, node); err == nil {
			t.Fatal("discarded immutable sibling omitted from execution preflight")
		} else {
			state(t, err, "22012")
		}
	})
	t.Run("transparent VALUES occurrence", func(t *testing.T) {
		values := &plan.ValuesNode{}
		b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
		q := queryQualifier{NullExtended: true, SourceRoot: &plan.ProjectNode{Child: values}}
		slot, err := b.WholeRowPresence(&q, 1)
		if err != nil || slot != 1 || values.PresenceSlot != slot {
			t.Fatal("presence did not belong to emitted VALUES occurrence", slot, err)
		}
	})
	t.Run("scoped operand reuse preserves policy", func(t *testing.T) {
		b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
		raw := parser.RowExpr{Fields: []parser.Expr{parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: 7}}}, Pos: parser.Position{Offset: 10, Character: 11}, Explicit: true}
		cached, err := b.Bind(raw, expressionPolicy{Context: analyze.ProjectionContext})
		if err != nil {
			t.Fatal(err)
		}
		b.Clause.FunctionProjection = functionProjectionScope{ArgumentPos: raw.Pos, ArgumentKind: analyze.ProjectionArgumentKind(raw), Operand: cached}
		node, err := b.Bind(raw, expressionPolicy{Context: analyze.ProjectionContext})
		if err != nil || node.ResultUsage().Type != cached.ResultUsage().Type {
			t.Fatal("operand was rebound", node, err)
		}
		node, err = b.Bind(raw, expressionPolicy{Context: analyze.FunctionArgumentContext, Expected: b.Type(sqltype.Bool).Usage(), Mode: sqltype.Implicit})
		if err == nil || node == nil || node.ResultUsage().Type != cached.ResultUsage().Type {
			t.Fatal("reuse bypassed coercion policy", node, err)
		}
	})
}

func TestCompositeExprAssignmentPaths(t *testing.T) {
	e := New()
	b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	integer := b.Type(sqltype.Int4)
	pair, err := sqltype.Composite(50000, "pair", "pair", 50001, []sqltype.CompositeMember{{1, "i", integer.Usage()}, {7, "j", integer.Usage()}})
	if err != nil {
		t.Fatal(err)
	}
	outer, err := sqltype.Composite(50002, "outer_pair", "outer_pair", 50003, []sqltype.CompositeMember{{3, "p", pair.Usage()}, {8, "n", integer.Usage()}})
	if err != nil {
		t.Fatal(err)
	}
	field := func(name string) parser.TargetStep { return parser.TargetStep{Field: &parser.Identifier{Text: name}} }
	target, err := b.BindAssignmentTarget(catalog.Column{Name: "v", Usage: outer.Usage()}, []parser.TargetStep{field("p"), field("i")}, parser.Position{})
	if err != nil {
		t.Fatal(err)
	}
	if target.Destination.Type != integer || target.Attribute != 3 || target.Next.Attribute != 1 {
		t.Fatal("wrong nested leaf")
	}
	original := sqltype.Null(outer)
	c := &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1, Row: []sqltype.Value{compositeExprUnitInteger(t, integer, 9)}}}
	rhs := plan.ColumnExpr{Info: expr.Info{Usage: integer.Usage()}, Scope: 1, Frame: 1, Slot: 0}
	first, err := target.ApplyExpression(c, original, rhs)
	if err != nil {
		t.Fatal(err)
	}
	if !original.IsNull() || first.CompositeField(0).CompositeField(0).IsNull() || !first.CompositeField(0).CompositeField(1).IsNull() || !first.CompositeField(1).IsNull() {
		t.Fatal("nested NULL initialization or original ownership")
	}
	secondTarget, err := b.BindAssignmentTarget(catalog.Column{Name: "v", Usage: outer.Usage()}, []parser.TargetStep{field("n")}, parser.Position{})
	if err != nil {
		t.Fatal(err)
	}
	second, err := secondTarget.ApplyExpression(c, first, rhs)
	if err != nil {
		t.Fatal(err)
	}
	if !first.CompositeField(1).IsNull() {
		t.Fatal("private composition mutated prior value")
	}
	for _, value := range []sqltype.Value{second.CompositeField(0).CompositeField(0), second.CompositeField(1)} {
		number, err := value.Integer()
		if err != nil || number != 9 {
			t.Fatal("RHS did not use original execution frame", number, err)
		}
	}
	if usage := analyze.TargetUsage(outer.Usage(), []parser.TargetStep{field("p"), field("j")}); usage.Type != integer {
		t.Fatal("leaf metadata", usage)
	}
	if _, err := b.BindAssignmentTarget(catalog.Column{Name: "v", Usage: outer.Usage()}, []parser.TargetStep{field("missing")}, parser.Position{}); err == nil {
		t.Fatal("absent assignment attribute admitted")
	}
}

func TestCompositeExprVisitorsAndRetag(t *testing.T) {
	e := New()
	b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	raw := parser.RowExpr{Fields: []parser.Expr{arrayUnitInteger(7), parser.RowExpr{Fields: []parser.Expr{arrayUnitInteger(8)}}}}
	bound, err := b.BindRow(raw, sqltype.Usage{}, sqltype.Explicit, parser.Position{})
	if err != nil {
		t.Fatal(err)
	}
	row := bound.(plan.RowExpr)
	row.Expansions = map[int]plan.CompositeExpansionBinding{0: {Base: row.Fields[1], Fields: []expr.Expr{row.Fields[1]}}}
	selected, err := b.SelectCompositeField(row, "f2", parser.Position{})
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	plan.WalkExpression(selected, func(expr.Expr) { count++ })
	if count != 5 {
		t.Fatal("lexical children omitted or analysis-only duplicate visited", count)
	}
	if err := validateGenerationPlan(e.CatalogRead(), nil, selected); err != nil {
		t.Fatal("owned anonymous tree", err)
	}
	if defaultReplayDeferrable(catalog.ColumnDefault{Expression: selected}) {
		t.Fatal("immutable constructor acquired deferred sequence replay")
	}
	other := New()
	view := other.CatalogRead()
	remapped, changed, err := retagBoundTypes(view, selected, func(old *sqltype.Descriptor) *sqltype.Descriptor {
		if old == nil {
			return nil
		}
		next, _ := view.LookupType(old.ID())
		return next
	})
	if err != nil || !changed {
		t.Fatal("record closure reownership", changed, err)
	}
	mappedRow := remapped.(plan.FieldExpr).Base.(plan.RowExpr)
	if mappedRow.Usage.Type == row.Usage.Type || mappedRow.Fields[1].ResultUsage().Type == row.Fields[1].ResultUsage().Type || mappedRow.Expansions[0].Base.ResultUsage().Type == row.Expansions[0].Base.ResultUsage().Type {
		t.Fatal("runtime or retained analysis shape borrowed old owner")
	}
	if err := validateGenerationPlan(view, nil, remapped); err != nil {
		t.Fatal("remapped closure", err)
	}
	value, err := remapped.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}})
	if err != nil {
		t.Fatal(err)
	}
	number, err := value.CompositeField(0).Integer()
	if err != nil || number != 8 {
		t.Fatal(number, err)
	}
	broken := mappedRow
	broken.Fields = nil
	if err := validateGenerationPlan(view, nil, broken); err == nil {
		t.Fatal("malformed row width admitted")
	}
	badField := remapped.(plan.FieldExpr)
	badField.Attribute = 99
	if err := validateGenerationPlan(view, nil, badField); err == nil {
		t.Fatal("retired field plan admitted")
	}
	empty, err := sqltype.RecordShape(nil)
	if err != nil {
		t.Fatal(err)
	}
	left := plan.RowExpr{Info: expr.Info{Usage: empty.Usage(), Property: expr.Properties{RowDependent: true}}, WholeNames: []string{}, WholeName: "a", PresenceFrame: 1}
	right := left
	right.WholeName = "b"
	if b.SameGroupExpression(left, right) {
		t.Fatal("zero-width source occurrences conflated")
	}
	zero := compositeOccurrence{Kind: analyze.CompositeExpansion, Base: empty.Usage()}
	if err := validateCompositeOccurrence(zero, view); err != nil {
		t.Fatal("zero-width expansion lost authority", err)
	}
	zero.Kind = 0
	if err := validateCompositeOccurrence(zero, view); err == nil {
		t.Fatal("missing capture kind admitted")
	}
	whole := compositeOccurrence{Kind: analyze.CompositeWholeRow, Base: mappedRow.Usage, Source: plan.WholeRowSource{Namespace: 1, Frame: 1}}
	if err := (&viewDefinition{}).validateCompositeSource(whole); err == nil {
		t.Fatal("missing actual namespace source admitted")
	}
	if err := validateGenerationPlan(view, nil, selected); err == nil {
		t.Fatal("foreign record owner admitted")
	}
}
