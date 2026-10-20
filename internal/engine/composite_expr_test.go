package engine

import (
	"context"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func compositeExprUnitLiteral(value sqltype.Value) expr.Expr {
	return literalExpression{Info: expr.Info{Usage: value.Type().Usage()}, value: value}
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
	row := rowExpression{Info: expr.Info{Usage: shape.Usage()}, fields: []expr.Expr{compositeExprUnitLiteral(compositeExprUnitInteger(t, integer, 7)), compositeExprUnitLiteral(sqltype.Null(integer))}}
	c := &EvalContext{evalStatement: evalStatement{Context: context.Background()}, evalFrame: evalFrame{Scope: 1, Frame: 1}}
	value, err := row.Eval(c)
	if err != nil || value.IsNull() || !value.CompositeField(1).IsNull() {
		t.Fatal("present partially NULL constructor", err)
	}
	selected := fieldExpression{Info: expr.Info{Usage: integer.Usage()}, base: row, attribute: 1, index: 0}
	value, err = selected.Eval(c)
	number, nerr := value.Integer()
	if err != nil || nerr != nil || number != 7 {
		t.Fatal(number, err, nerr)
	}
	// A selected constructor child does not evaluate an unused invalid child.
	row.fields = []expr.Expr{row.fields[0], literalExpression{}}
	selected.base = row
	if _, err := selected.Eval(c); err != nil {
		t.Fatal("selected field evaluated sibling", err)
	}
	if _, err := row.Eval(c); err == nil {
		t.Fatal("full constructor omitted sibling")
	}
	row.fields = []expr.Expr{compositeExprUnitLiteral(sqltype.Null(integer)), compositeExprUnitLiteral(sqltype.Null(integer))}
	row.wholeNames = []string{"f1", "f2"}
	row.presenceSlot = 1
	row.presenceFrame = 1
	c.presenceSlots = []int{1}
	c.presence = []bool{true}
	if value, err := row.Eval(c); err != nil || value.IsNull() {
		t.Fatal("all-NULL emitted tuple became absent", err)
	}
	c.presence[0] = false
	if value, err := row.Eval(c); err != nil || !value.IsNull() {
		t.Fatal("null-extended tuple became present", err)
	}
	selected.base = row
	if value, err := selected.Eval(c); err != nil || !value.IsNull() {
		t.Fatal("direct field ignored occurrence absence", err)
	}
	outer := &EvalContext{evalStatement: evalStatement{Context: context.Background()}, evalFrame: evalFrame{Scope: 1, Frame: 2, Outer: c}}
	if value, err := row.Eval(outer); err != nil || !value.IsNull() {
		t.Fatal("whole row lost outer frame presence", err)
	}
	c.presenceSlots = nil
	if _, err := row.Eval(c); err == nil {
		t.Fatal("missing demanded occurrence was treated as present")
	}
	empty, err := sqltype.RecordShape(nil)
	if err != nil {
		t.Fatal(err)
	}
	zero := rowExpression{Info: expr.Info{Usage: empty.Usage()}, wholeNames: []string{}, presenceSlot: 1, presenceFrame: 1}
	c.presenceSlots = []int{1}
	c.presence = []bool{true}
	if value, err := zero.Eval(c); err != nil || value.IsNull() || value.CompositeLen() != 0 {
		t.Fatal("present zero-width occurrence", err)
	}
	c.presence[0] = false
	if value, err := zero.Eval(c); err != nil || !value.IsNull() {
		t.Fatal("absent zero-width occurrence", err)
	}
	source := &querySource{}
	establishSourcePresence(source, 0)
	if source.presenceCount != 0 || source.presenceSlots != nil || source.presenceConstant != nil {
		t.Fatal("undemanded source allocated presence")
	}
	t.Run("discarded immutable preflight", func(t *testing.T) {
		node := preflightCast(t, "SELECT (ROW(7,1/0)).f1")
		if _, _, err := preflightCoercions(&EvalContext{evalStatement: evalStatement{Context: context.Background()}}, node); err == nil {
			t.Fatal("discarded immutable sibling omitted from execution preflight")
		} else {
			state(t, err, "22012")
		}
	})
	t.Run("transparent VALUES occurrence", func(t *testing.T) {
		values := &valuesNode{}
		b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
		q := queryQualifier{nullExtended: true, sourceRoot: &projectNode{child: values}}
		slot, err := b.wholeRowPresence(&q, 1)
		if err != nil || slot != 1 || values.presenceSlot != slot {
			t.Fatal("presence did not belong to emitted VALUES occurrence", slot, err)
		}
	})
	t.Run("scoped operand reuse preserves policy", func(t *testing.T) {
		b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
		raw := parser.RowExpr{Fields: []parser.Expr{parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: 7}}}, Pos: parser.Position{Offset: 10, Character: 11}, Explicit: true}
		cached, err := b.bind(raw, expressionPolicy{context: projectionContext})
		if err != nil {
			t.Fatal(err)
		}
		b.clause.functionProjection = functionProjectionScope{argumentPos: raw.Pos, argumentKind: projectionArgumentKind(raw), operand: cached}
		node, err := b.bind(raw, expressionPolicy{context: projectionContext})
		if err != nil || node.ResultUsage().Type != cached.ResultUsage().Type {
			t.Fatal("operand was rebound", node, err)
		}
		node, err = b.bind(raw, expressionPolicy{context: functionArgumentContext, expected: b.typ(sqltype.Bool).Usage(), mode: sqltype.Implicit})
		if err == nil || node == nil || node.ResultUsage().Type != cached.ResultUsage().Type {
			t.Fatal("reuse bypassed coercion policy", node, err)
		}
	})
}

func TestCompositeExprAssignmentPaths(t *testing.T) {
	e := New()
	b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	integer := b.typ(sqltype.Int4)
	pair, err := sqltype.Composite(50000, "pair", "pair", 50001, []sqltype.CompositeMember{{1, "i", integer.Usage()}, {7, "j", integer.Usage()}})
	if err != nil {
		t.Fatal(err)
	}
	outer, err := sqltype.Composite(50002, "outer_pair", "outer_pair", 50003, []sqltype.CompositeMember{{3, "p", pair.Usage()}, {8, "n", integer.Usage()}})
	if err != nil {
		t.Fatal(err)
	}
	field := func(name string) parser.TargetStep { return parser.TargetStep{Field: &parser.Identifier{Text: name}} }
	target, err := b.bindAssignmentTarget(catalog.Column{Name: "v", Usage: outer.Usage()}, []parser.TargetStep{field("p"), field("i")}, parser.Position{})
	if err != nil {
		t.Fatal(err)
	}
	if target.destination.Type != integer || target.attribute != 3 || target.next.attribute != 1 {
		t.Fatal("wrong nested leaf")
	}
	original := sqltype.Null(outer)
	c := &EvalContext{evalStatement: evalStatement{Context: context.Background()}, evalFrame: evalFrame{Scope: 1, Frame: 1, Row: []sqltype.Value{compositeExprUnitInteger(t, integer, 9)}}}
	rhs := columnExpression{Info: expr.Info{Usage: integer.Usage()}, scope: 1, frame: 1, slot: 0}
	first, err := target.applyExpression(c, original, rhs)
	if err != nil {
		t.Fatal(err)
	}
	if !original.IsNull() || first.CompositeField(0).CompositeField(0).IsNull() || !first.CompositeField(0).CompositeField(1).IsNull() || !first.CompositeField(1).IsNull() {
		t.Fatal("nested NULL initialization or original ownership")
	}
	secondTarget, err := b.bindAssignmentTarget(catalog.Column{Name: "v", Usage: outer.Usage()}, []parser.TargetStep{field("n")}, parser.Position{})
	if err != nil {
		t.Fatal(err)
	}
	second, err := secondTarget.applyExpression(c, first, rhs)
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
	if usage := targetUsage(outer.Usage(), []parser.TargetStep{field("p"), field("j")}); usage.Type != integer {
		t.Fatal("leaf metadata", usage)
	}
	if _, err := b.bindAssignmentTarget(catalog.Column{Name: "v", Usage: outer.Usage()}, []parser.TargetStep{field("missing")}, parser.Position{}); err == nil {
		t.Fatal("absent assignment attribute admitted")
	}
}

func TestCompositeExprVisitorsAndRetag(t *testing.T) {
	e := New()
	b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	raw := parser.RowExpr{Fields: []parser.Expr{arrayUnitInteger(7), parser.RowExpr{Fields: []parser.Expr{arrayUnitInteger(8)}}}}
	bound, err := b.bindRow(raw, sqltype.Usage{}, sqltype.Explicit, parser.Position{})
	if err != nil {
		t.Fatal(err)
	}
	row := bound.(rowExpression)
	row.expansions = map[int]compositeExpansionBinding{0: {base: row.fields[1], fields: []expr.Expr{row.fields[1]}}}
	selected, err := b.selectCompositeField(row, "f2", parser.Position{})
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	walkBoundExpression(selected, func(expr.Expr) { count++ })
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
	mappedRow := remapped.(fieldExpression).base.(rowExpression)
	if mappedRow.Usage.Type == row.Usage.Type || mappedRow.fields[1].ResultUsage().Type == row.fields[1].ResultUsage().Type || mappedRow.expansions[0].base.ResultUsage().Type == row.expansions[0].base.ResultUsage().Type {
		t.Fatal("runtime or retained analysis shape borrowed old owner")
	}
	if err := validateGenerationPlan(view, nil, remapped); err != nil {
		t.Fatal("remapped closure", err)
	}
	value, err := remapped.Eval(&EvalContext{evalStatement: evalStatement{Context: context.Background()}})
	if err != nil {
		t.Fatal(err)
	}
	number, err := value.CompositeField(0).Integer()
	if err != nil || number != 8 {
		t.Fatal(number, err)
	}
	broken := mappedRow
	broken.fields = nil
	if err := validateGenerationPlan(view, nil, broken); err == nil {
		t.Fatal("malformed row width admitted")
	}
	badField := remapped.(fieldExpression)
	badField.attribute = 99
	if err := validateGenerationPlan(view, nil, badField); err == nil {
		t.Fatal("retired field plan admitted")
	}
	empty, err := sqltype.RecordShape(nil)
	if err != nil {
		t.Fatal(err)
	}
	left := rowExpression{Info: expr.Info{Usage: empty.Usage(), Property: expr.Properties{RowDependent: true}}, wholeNames: []string{}, wholeName: "a", presenceFrame: 1}
	right := left
	right.wholeName = "b"
	if b.sameGroupExpression(left, right) {
		t.Fatal("zero-width source occurrences conflated")
	}
	zero := compositeOccurrence{kind: compositeExpansion, base: empty.Usage()}
	if err := zero.validate(view); err != nil {
		t.Fatal("zero-width expansion lost authority", err)
	}
	zero.kind = 0
	if err := zero.validate(view); err == nil {
		t.Fatal("missing capture kind admitted")
	}
	whole := compositeOccurrence{kind: compositeWholeRow, base: mappedRow.Usage, source: wholeRowSource{namespace: 1, frame: 1}}
	if err := (&viewDefinition{}).validateCompositeSource(whole); err == nil {
		t.Fatal("missing actual namespace source admitted")
	}
	if err := validateGenerationPlan(view, nil, selected); err == nil {
		t.Fatal("foreign record owner admitted")
	}
}
