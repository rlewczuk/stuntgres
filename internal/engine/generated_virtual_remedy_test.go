package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

type generatedOccurrenceProbe struct {
	expr.Info
	calls *int
	value sqltype.Value
	err   error
}

func (e generatedOccurrenceProbe) Eval(expr.Context) (sqltype.Value, error) {
	*e.calls++
	return e.value, e.err
}

func TestGeneratedVirtualOccurrenceAliasesAndOuterFrame(t *testing.T) {
	_, _, d := generatedUnitDescriptor(t)
	typ := d.Columns[0].Usage.Type
	one, _ := sqltype.Integer(typ, 1)
	calls := 0
	failure := errors.New("demanded recipe")
	probe := generatedOccurrenceProbe{Info: expr.Info{Usage: typ.Usage()}, calls: &calls, value: one, err: failure}
	column := columnExpression{Info: expr.Info{Usage: typ.Usage()}, Scope: 1, Frame: 4, Slot: 1, Virtual: probe, RowWidth: 2, QueryOccurrence: 1}
	// Local aliases 0/1 map to global physical ordinals 3/7 under locks.
	physical := &querySource{occurrence: 3}
	state := &queryState{source: &querySource{sources: []*querySource{{input: physical}, {occurrence: 7}}}, virtualReads: true}
	outer := EvalContext{evalStatement: evalStatement{Context: context.Background()}, evalFrame: evalFrame{Scope: 1, Frame: 4, Row: []sqltype.Value{sqltype.Null(typ), sqltype.Null(typ)}, query: state, queryOccurrences: []int{3, 7}, queryProvenance: []int{absentQueryIndex, 0}}}
	inner := EvalContext{evalStatement: evalStatement{Context: context.Background()}, evalFrame: evalFrame{Scope: 1, Frame: 8, Outer: &outer, queryOccurrences: []int{3, 7}, queryProvenance: []int{0, absentQueryIndex}}}
	value, err := column.Eval(&inner)
	if err != nil || !value.IsNull() || value.Type() != typ || calls != 0 {
		t.Fatal("absent outer alias demanded recipe", value, err, calls)
	}
	column.QueryOccurrence = 2
	if _, err := column.Eval(&inner); !errors.Is(err, failure) || calls != 1 {
		t.Fatal("real all-NULL alias skipped demand", err, calls)
	}
	probe.err = nil
	column.Virtual = probe
	value, err = column.Eval(&inner)
	if n, access := value.Integer(); err != nil || access != nil || n != 1 || calls != 2 {
		t.Fatal("present all-NULL value", value, err, calls)
	}
	column.QueryOccurrence = 0
	value, err = column.Eval(&inner)
	if err != nil || value.IsNull() || calls != 3 {
		t.Fatal("owner-row read inherited unrelated absence", value, err, calls)
	}
	column.Virtual = nil
	value, err = column.Eval(&inner)
	if err != nil || !value.IsNull() || calls != 3 {
		t.Fatal("ordinary read entered generation", value, err, calls)
	}
}

func TestGeneratedVirtualDestinationCheckAttachment(t *testing.T) {
	e, view, source := generatedUnitDescriptor(t)
	typ := source.Columns[0].Usage.Type
	one, _ := sqltype.Integer(typ, 1)
	seven, _ := sqltype.Integer(typ, 7)
	negative, _ := sqltype.Integer(typ, -9)
	origin := e.catalog.Domain.Ref(view.Epoch(), source.Address())
	origin.Address.SubID = 2
	old := columnExpression{Info: expr.Info{Usage: typ.Usage()}, Origin: origin, Scope: 1, Frame: 1, Slot: 1, Virtual: literalExpression{Info: expr.Info{Usage: typ.Usage()}, Value: seven}, RowWidth: 2, QueryOccurrence: 1}
	ordinary := *source
	ordinary.Constraints.Check = []catalog.CheckConstraint{{Name: "positive", Expression: old}}
	if err := refreshGenerationChecks(&ordinary, view); err != nil {
		t.Fatal(err)
	}
	column := ordinary.Constraints.Check[0].Expression.(columnExpression)
	if column.Virtual != nil || column.QueryOccurrence != 0 || column.RowWidth != 0 {
		t.Fatal("ordinary destination retained source payload", column)
	}
	value, err := column.Eval(&EvalContext{evalStatement: evalStatement{Context: context.Background()}, evalFrame: evalFrame{Scope: 1, Frame: 1, Row: []sqltype.Value{one, negative}}})
	if n, access := value.Integer(); err != nil || access != nil || n != -9 {
		t.Fatal("ordinary CHECK did not read supplied value", value, err)
	}
	// The source map contains two attributes, but destination inputs start at 2.
	wide := *source
	wide.Columns = append(append([]catalog.Column(nil), source.Columns...), source.Columns...)
	wide.Attributes = []int32{3, 4, 1, 2}
	base := columnExpression{Info: expr.Info{Usage: typ.Usage()}, Scope: 1, Frame: 1, Slot: 2}
	wide.ColumnMeta.Generations = map[int32]catalog.ColumnGeneration{2: {Kind: catalog.GenerationVirtual, Expression: base, Planned: base}}
	copied, err := remapScalarExpression(old, nil, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, map[int32]int{1: 2, 2: 3}, nil)
	if err != nil {
		t.Fatal(err)
	}
	copiedColumn := copied.(columnExpression)
	if copiedColumn.Virtual != nil || copiedColumn.QueryOccurrence != 0 || copiedColumn.RowWidth != 0 || copiedColumn.Slot != 3 {
		t.Fatal("owner copy retained source execution recipe", copiedColumn)
	}
	wide.Constraints.Check = []catalog.CheckConstraint{{Name: "positive", Expression: copied}}
	if err := refreshGenerationChecks(&wide, view); err != nil {
		t.Fatal(err)
	}
	column = wide.Constraints.Check[0].Expression.(columnExpression)
	if column.Slot != 3 || column.RowStart != 0 || column.RowWidth != 4 || column.QueryOccurrence != 0 {
		t.Fatal("destination CHECK layout", column)
	}
	row := []sqltype.Value{seven, seven, one, sqltype.Null(typ)}
	value, err = column.Eval(&EvalContext{evalStatement: evalStatement{Context: context.Background()}, evalFrame: evalFrame{Scope: 1, Frame: 1, Row: row}})
	if n, access := value.Integer(); err != nil || access != nil || n != 1 {
		t.Fatal("destination recipe/window not evaluated", value, err)
	}
	if old.RowWidth != 2 || old.Slot != 1 || old.QueryOccurrence != 1 {
		t.Fatal("source attachment mutated", old)
	}
}

func TestGeneratedVirtualGraftedAliasOccurrences(t *testing.T) {
	e, view, d := generatedUnitDescriptor(t)
	view = view.WithState(catalog.CloneState(view.State()))
	view.State().SetDescriptor(d.Address(), d)
	ref := e.catalog.Domain.Ref(view.Epoch(), d.Address())
	origin := ref
	origin.Address.SubID = 2
	typ := d.Columns[0].Usage.Type
	one, _ := sqltype.Integer(typ, 1)
	literal := literalExpression{Info: expr.Info{Usage: typ.Usage()}, Value: one}
	d.ColumnMeta.Generations = map[int32]catalog.ColumnGeneration{2: {Kind: catalog.GenerationVirtual, Expression: literal, Planned: literal}}
	left := columnExpression{Info: expr.Info{Usage: typ.Usage()}, Origin: origin, Scope: 1, Frame: 4, Slot: 1, QueryOccurrence: 1}.WithGeneration(d, 1, 0)
	right := left
	right.Slot, right.RowStart, right.QueryOccurrence = 3, 2, 2
	provider, err := sqltype.BindArithmetic(sqltype.ArithmeticAdd, typ, typ, typ)
	if err != nil {
		t.Fatal(err)
	}
	add := arithmeticExpression{Info: expr.Info{Usage: typ.Usage()}, Left: left, Right: literal, Provider: provider, Operator: sqltype.ArithmeticAdd}
	boolean := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil).typ(sqltype.Bool)
	test := nullTestExpression{Info: expr.Info{Usage: boolean.Usage()}, Child: right}
	choice := caseExpression{Info: expr.Info{Usage: typ.Usage()}, Arms: []caseBranch{{Condition: test, Result: add}}, Otherwise: right}
	outside := left
	outside.Frame = 99
	syntax := &parser.Select{}
	join := &joinNode{Left: &sourceNode{Ref: ref, Occurrence: 0}, Right: &sourceNode{Ref: ref, Occurrence: 1}}
	project := &projectNode{Frame: 4, Syntax: syntax, Child: join, Expressions: []expr.Expr{left, right, add, test, choice, outside}}
	definition := &viewDefinition{queries: map[*parser.Select]*projectNode{syntax: project}, sources: []viewLeaf{{ref: ref}, {ref: ref}}}
	consumer := *d
	consumer.View = viewPayloadOf(definition)
	w := &viewWrite{layers: []viewWriteLayer{{relation: &consumer}}}
	b := binding{querySources: []queryLeaf{{ref: ref, relation: d}}}
	mapped, err := w.graftQuery(&b, view, project, func(expr expr.Expr) (expr.Expr, bool, error) { return expr, false, nil })
	if err != nil {
		t.Fatal(err)
	}
	next := mapped.(*projectNode)
	nextJoin := next.Child.(*joinNode)
	l, r := next.Expressions[0].(columnExpression), next.Expressions[1].(columnExpression)
	if len(b.querySources) != 3 || l.QueryOccurrence != 2 || r.QueryOccurrence != 3 || nextJoin.Left.(*sourceNode).Occurrence != 1 || nextJoin.Right.(*sourceNode).Occurrence != 2 {
		t.Fatal("grafted aliases lost occurrence mapping", l, r, nextJoin, len(b.querySources))
	}
	wrapped := next.Expressions[2].(arithmeticExpression)
	nullTest := next.Expressions[3].(nullTestExpression)
	nextChoice := next.Expressions[4].(caseExpression)
	if wrapped.Left.(columnExpression).QueryOccurrence != 2 || nullTest.Child.(columnExpression).QueryOccurrence != 3 || nextChoice.Arms[0].Condition.(nullTestExpression).Child.(columnExpression).QueryOccurrence != 3 || next.Expressions[5].(columnExpression).QueryOccurrence != 1 {
		t.Fatal("compound or outside lexical occurrence remap")
	}
	evaluation := EvalContext{evalStatement: evalStatement{Context: context.Background()}, evalFrame: evalFrame{Scope: 1, Frame: 4, Row: []sqltype.Value{sqltype.Null(typ), sqltype.Null(typ), sqltype.Null(typ), sqltype.Null(typ)}, query: &queryState{source: &querySource{sources: []*querySource{{occurrence: 9}, {occurrence: 4}, {occurrence: 7}}}}, queryOccurrences: []int{4, 7}, queryProvenance: []int{absentQueryIndex, 0}}}
	value, err := wrapped.Eval(&evaluation)
	if err != nil || !value.IsNull() {
		t.Fatal("absent compound graft recomputed recipe", value, err)
	}
	evaluation.queryProvenance = []int{0, absentQueryIndex}
	value, err = nextChoice.Eval(&evaluation)
	if n, access := value.Integer(); err != nil || access != nil || n != 2 {
		t.Fatal("present CASE graft evaluation", value, err)
	}
	innerLeft, innerRight := left, right
	innerLeft.Frame, innerRight.Frame = 8, 8
	innerAdd := add
	innerAdd.Left = innerLeft
	inner := &projectNode{Frame: 8, Child: join, Expressions: []expr.Expr{innerAdd, innerRight}}
	outer := &projectNode{Frame: 4, Syntax: syntax, Child: &derivedNode{Query: &BoundQuery{Root: inner}}}
	derived, err := w.graftQuery(&b, view, outer, func(expr expr.Expr) (expr.Expr, bool, error) { return expr, false, nil })
	if err != nil {
		t.Fatal(err)
	}
	nextInner := derived.(*projectNode).Child.(*derivedNode).Query.Root.(*projectNode)
	innerColumn := nextInner.Expressions[0].(arithmeticExpression).Left.(columnExpression)
	if innerColumn.QueryOccurrence != 2 || nextInner.Child.(*joinNode).Left.(*sourceNode).Occurrence != 1 || innerAdd.Left.(columnExpression).QueryOccurrence != 1 {
		t.Fatal("derived lexical frame lost mapping or mutated original")
	}
	evaluation.Frame = 8
	value, err = nextInner.Expressions[0].Eval(&evaluation)
	if n, access := value.Integer(); err != nil || access != nil || n != 2 {
		t.Fatal("derived present graft evaluation", value, err)
	}
	evaluation.queryProvenance[0] = absentQueryIndex
	value, err = nextInner.Expressions[0].Eval(&evaluation)
	if err != nil || !value.IsNull() {
		t.Fatal("derived absent graft evaluation", value, err)
	}
	if left.QueryOccurrence != 1 || right.QueryOccurrence != 2 || join.Left.(*sourceNode).Occurrence != 0 {
		t.Fatal("grafting mutated source graph")
	}
}

func TestGeneratedVirtualJoinIdentityBeforeMerge(t *testing.T) {
	_, _, d := generatedUnitDescriptor(t)
	typ := d.Columns[0].Usage.Type
	one, _ := sqltype.Integer(typ, 1)
	for _, present := range []bool{false, true} {
		calls := 0
		probe := generatedOccurrenceProbe{Info: expr.Info{Usage: typ.Usage()}, calls: &calls, value: one}
		column := columnExpression{Info: expr.Info{Usage: typ.Usage()}, Scope: 1, Frame: 1, Slot: 2, Virtual: probe, RowStart: 1, RowWidth: 2, QueryOccurrence: 2}
		left := &querySource{anonymous: [][]sqltype.Value{{one}}, occurrence: 0}
		right := &querySource{anonymous: [][]sqltype.Value{}, occurrence: 1}
		if present {
			right.anonymous = [][]sqltype.Value{{sqltype.Null(typ), sqltype.Null(typ)}}
		}
		state := &queryState{source: &querySource{sources: []*querySource{left, right}}, virtualReads: true}
		schema := querySchema{d.Columns[0], d.Columns[0], d.Columns[1], d.Columns[1]}
		join := &joinNode{querySchema: schema, Kind: parser.JoinLeft, Left: &sourceNode{querySchema: schema[:1], Occurrence: 0}, Right: &sourceNode{querySchema: schema[1:3], Occurrence: 1}, LeftWidth: 1, RightWidth: 2, Merges: []expr.Expr{column}}
		stage, err := openJoinNode(context.Background(), state, join)
		if err != nil {
			t.Fatal(err)
		}
		if err := stage.drain(context.Background()); err != nil || stage.count() != 1 {
			t.Fatal("join production", err, stage.count())
		}
		evaluation := state.evaluation(context.Background())
		evaluation.bindQueryRow(stage.source, stage.index(0))
		value, err := column.Eval(&evaluation)
		if err != nil || value.IsNull() == present || stage.source.row(0)[3].IsNull() == present {
			t.Fatal("transient merge/saved projection identity", present, value, err, stage.source.row(0))
		}
		wantCalls := 0
		if present {
			wantCalls = 2 // Once for the merge and once for the demanded read.
		}
		if calls != wantCalls {
			t.Fatal("recipe demand count", present, calls, wantCalls)
		}
		if err := stage.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
