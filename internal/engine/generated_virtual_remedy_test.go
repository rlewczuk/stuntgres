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
	column := columnExpression{Info: expr.Info{Usage: typ.Usage()}, scope: 1, frame: 4, slot: 1, virtual: probe, rowWidth: 2, queryOccurrence: 1}
	// Local aliases 0/1 map to global physical ordinals 3/7 under locks.
	physical := &querySource{occurrence: 3}
	state := &queryState{source: &querySource{sources: []*querySource{{input: physical}, {occurrence: 7}}}, virtualReads: true}
	outer := EvalContext{Context: context.Background(), Scope: 1, Frame: 4, Row: []sqltype.Value{sqltype.Null(typ), sqltype.Null(typ)}, query: state, queryOccurrences: []int{3, 7}, queryProvenance: []int{absentQueryIndex, 0}}
	inner := EvalContext{Context: context.Background(), Scope: 1, Frame: 8, Outer: &outer, queryOccurrences: []int{3, 7}, queryProvenance: []int{0, absentQueryIndex}}
	value, err := column.Eval(&inner)
	if err != nil || !value.IsNull() || value.Type() != typ || calls != 0 {
		t.Fatal("absent outer alias demanded recipe", value, err, calls)
	}
	column.queryOccurrence = 2
	if _, err := column.Eval(&inner); !errors.Is(err, failure) || calls != 1 {
		t.Fatal("real all-NULL alias skipped demand", err, calls)
	}
	probe.err = nil
	column.virtual = probe
	value, err = column.Eval(&inner)
	if n, access := value.Integer(); err != nil || access != nil || n != 1 || calls != 2 {
		t.Fatal("present all-NULL value", value, err, calls)
	}
	column.queryOccurrence = 0
	value, err = column.Eval(&inner)
	if err != nil || value.IsNull() || calls != 3 {
		t.Fatal("owner-row read inherited unrelated absence", value, err, calls)
	}
	column.virtual = nil
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
	old := columnExpression{Info: expr.Info{Usage: typ.Usage()}, origin: origin, scope: 1, frame: 1, slot: 1, virtual: literalExpression{Info: expr.Info{Usage: typ.Usage()}, value: seven}, rowWidth: 2, queryOccurrence: 1}
	ordinary := *source
	ordinary.CheckConstraints = []checkConstraint{{Name: "positive", Expression: old}}
	if err := refreshGenerationChecks(&ordinary, view); err != nil {
		t.Fatal(err)
	}
	column := ordinary.CheckConstraints[0].Expression.(columnExpression)
	if column.virtual != nil || column.queryOccurrence != 0 || column.rowWidth != 0 {
		t.Fatal("ordinary destination retained source payload", column)
	}
	value, err := column.Eval(&EvalContext{Context: context.Background(), Scope: 1, Frame: 1, Row: []sqltype.Value{one, negative}})
	if n, access := value.Integer(); err != nil || access != nil || n != -9 {
		t.Fatal("ordinary CHECK did not read supplied value", value, err)
	}
	// The source map contains two attributes, but destination inputs start at 2.
	wide := *source
	wide.Columns = append(append([]ColumnDescriptor(nil), source.Columns...), source.Columns...)
	wide.Attributes = []int32{3, 4, 1, 2}
	base := columnExpression{Info: expr.Info{Usage: typ.Usage()}, scope: 1, frame: 1, slot: 2}
	wide.Generations = map[int32]columnGeneration{2: {Kind: generationVirtual, Expression: base, Planned: base}}
	copied, err := remapScalarExpression(old, nil, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, map[int32]int{1: 2, 2: 3}, nil)
	if err != nil {
		t.Fatal(err)
	}
	copiedColumn := copied.(columnExpression)
	if copiedColumn.virtual != nil || copiedColumn.queryOccurrence != 0 || copiedColumn.rowWidth != 0 || copiedColumn.slot != 3 {
		t.Fatal("owner copy retained source execution recipe", copiedColumn)
	}
	wide.CheckConstraints = []checkConstraint{{Name: "positive", Expression: copied}}
	if err := refreshGenerationChecks(&wide, view); err != nil {
		t.Fatal(err)
	}
	column = wide.CheckConstraints[0].Expression.(columnExpression)
	if column.slot != 3 || column.rowStart != 0 || column.rowWidth != 4 || column.queryOccurrence != 0 {
		t.Fatal("destination CHECK layout", column)
	}
	row := []sqltype.Value{seven, seven, one, sqltype.Null(typ)}
	value, err = column.Eval(&EvalContext{Context: context.Background(), Scope: 1, Frame: 1, Row: row})
	if n, access := value.Integer(); err != nil || access != nil || n != 1 {
		t.Fatal("destination recipe/window not evaluated", value, err)
	}
	if old.rowWidth != 2 || old.slot != 1 || old.queryOccurrence != 1 {
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
	literal := literalExpression{Info: expr.Info{Usage: typ.Usage()}, value: one}
	d.Generations = map[int32]columnGeneration{2: {Kind: generationVirtual, Expression: literal, Planned: literal}}
	left := columnExpression{Info: expr.Info{Usage: typ.Usage()}, origin: origin, scope: 1, frame: 4, slot: 1, queryOccurrence: 1}.withGeneration(d, 1, 0)
	right := left
	right.slot, right.rowStart, right.queryOccurrence = 3, 2, 2
	provider, err := sqltype.BindArithmetic(sqltype.ArithmeticAdd, typ, typ, typ)
	if err != nil {
		t.Fatal(err)
	}
	add := arithmeticExpression{Info: expr.Info{Usage: typ.Usage()}, left: left, right: literal, provider: provider, operator: sqltype.ArithmeticAdd}
	boolean := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil).typ(sqltype.Bool)
	test := nullTestExpression{Info: expr.Info{Usage: boolean.Usage()}, child: right}
	choice := caseExpression{Info: expr.Info{Usage: typ.Usage()}, arms: []caseBranch{{condition: test, result: add}}, otherwise: right}
	outside := left
	outside.frame = 99
	syntax := &parser.Select{}
	join := &joinNode{left: &sourceNode{ref: ref, occurrence: 0}, right: &sourceNode{ref: ref, occurrence: 1}}
	project := &projectNode{frame: 4, syntax: syntax, child: join, expressions: []expr.Expr{left, right, add, test, choice, outside}}
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
	nextJoin := next.child.(*joinNode)
	l, r := next.expressions[0].(columnExpression), next.expressions[1].(columnExpression)
	if len(b.querySources) != 3 || l.queryOccurrence != 2 || r.queryOccurrence != 3 || nextJoin.left.(*sourceNode).occurrence != 1 || nextJoin.right.(*sourceNode).occurrence != 2 {
		t.Fatal("grafted aliases lost occurrence mapping", l, r, nextJoin, len(b.querySources))
	}
	wrapped := next.expressions[2].(arithmeticExpression)
	nullTest := next.expressions[3].(nullTestExpression)
	nextChoice := next.expressions[4].(caseExpression)
	if wrapped.left.(columnExpression).queryOccurrence != 2 || nullTest.child.(columnExpression).queryOccurrence != 3 || nextChoice.arms[0].condition.(nullTestExpression).child.(columnExpression).queryOccurrence != 3 || next.expressions[5].(columnExpression).queryOccurrence != 1 {
		t.Fatal("compound or outside lexical occurrence remap")
	}
	evaluation := EvalContext{Context: context.Background(), Scope: 1, Frame: 4, Row: []sqltype.Value{sqltype.Null(typ), sqltype.Null(typ), sqltype.Null(typ), sqltype.Null(typ)}, query: &queryState{source: &querySource{sources: []*querySource{{occurrence: 9}, {occurrence: 4}, {occurrence: 7}}}}, queryOccurrences: []int{4, 7}, queryProvenance: []int{absentQueryIndex, 0}}
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
	innerLeft.frame, innerRight.frame = 8, 8
	innerAdd := add
	innerAdd.left = innerLeft
	inner := &projectNode{frame: 8, child: join, expressions: []expr.Expr{innerAdd, innerRight}}
	outer := &projectNode{frame: 4, syntax: syntax, child: &derivedNode{query: &BoundQuery{Root: inner}}}
	derived, err := w.graftQuery(&b, view, outer, func(expr expr.Expr) (expr.Expr, bool, error) { return expr, false, nil })
	if err != nil {
		t.Fatal(err)
	}
	nextInner := derived.(*projectNode).child.(*derivedNode).query.Root.(*projectNode)
	innerColumn := nextInner.expressions[0].(arithmeticExpression).left.(columnExpression)
	if innerColumn.queryOccurrence != 2 || nextInner.child.(*joinNode).left.(*sourceNode).occurrence != 1 || innerAdd.left.(columnExpression).queryOccurrence != 1 {
		t.Fatal("derived lexical frame lost mapping or mutated original")
	}
	evaluation.Frame = 8
	value, err = nextInner.expressions[0].Eval(&evaluation)
	if n, access := value.Integer(); err != nil || access != nil || n != 2 {
		t.Fatal("derived present graft evaluation", value, err)
	}
	evaluation.queryProvenance[0] = absentQueryIndex
	value, err = nextInner.expressions[0].Eval(&evaluation)
	if err != nil || !value.IsNull() {
		t.Fatal("derived absent graft evaluation", value, err)
	}
	if left.queryOccurrence != 1 || right.queryOccurrence != 2 || join.left.(*sourceNode).occurrence != 0 {
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
		column := columnExpression{Info: expr.Info{Usage: typ.Usage()}, scope: 1, frame: 1, slot: 2, virtual: probe, rowStart: 1, rowWidth: 2, queryOccurrence: 2}
		left := &querySource{anonymous: [][]sqltype.Value{{one}}, occurrence: 0}
		right := &querySource{anonymous: [][]sqltype.Value{}, occurrence: 1}
		if present {
			right.anonymous = [][]sqltype.Value{{sqltype.Null(typ), sqltype.Null(typ)}}
		}
		state := &queryState{source: &querySource{sources: []*querySource{left, right}}, virtualReads: true}
		schema := querySchema{d.Columns[0], d.Columns[0], d.Columns[1], d.Columns[1]}
		join := &joinNode{querySchema: schema, kind: parser.JoinLeft, left: &sourceNode{querySchema: schema[:1], occurrence: 0}, right: &sourceNode{querySchema: schema[1:3], occurrence: 1}, leftWidth: 1, rightWidth: 2, merges: []expr.Expr{column}}
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
