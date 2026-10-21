package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
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
	column := plan.ColumnExpr{Info: expr.Info{Usage: typ.Usage()}, Scope: 1, Frame: 4, Slot: 1, Virtual: probe, RowWidth: 2, QueryOccurrence: 1}
	// Local aliases 0/1 map to global physical ordinals 3/7 under locks.
	physical := &exec.Source{Occurrence: 3}
	state := &exec.State{Source: &exec.Source{Sources: []*exec.Source{{Input: physical}, {Occurrence: 7}}}, VirtualReads: true}
	outer := exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 4, Row: []sqltype.Value{sqltype.Null(typ), sqltype.Null(typ)}, Query: state, QueryOccurrences: []int{3, 7}, QueryProvenance: []int{exec.AbsentQueryIndex, 0}}}
	inner := exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 8, Outer: &outer, QueryOccurrences: []int{3, 7}, QueryProvenance: []int{0, exec.AbsentQueryIndex}}}
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
	old := plan.ColumnExpr{Info: expr.Info{Usage: typ.Usage()}, Origin: origin, Scope: 1, Frame: 1, Slot: 1, Virtual: plan.LiteralExpr{Info: expr.Info{Usage: typ.Usage()}, Value: seven}, RowWidth: 2, QueryOccurrence: 1}
	ordinary := *source
	ordinary.Constraints.Check = []catalog.CheckConstraint{{Name: "positive", Expression: old}}
	if err := stored.RefreshGenerationChecks(&ordinary, view); err != nil {
		t.Fatal(err)
	}
	column := ordinary.Constraints.Check[0].Expression.(plan.ColumnExpr)
	if column.Virtual != nil || column.QueryOccurrence != 0 || column.RowWidth != 0 {
		t.Fatal("ordinary destination retained source payload", column)
	}
	value, err := column.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1, Row: []sqltype.Value{one, negative}}})
	if n, access := value.Integer(); err != nil || access != nil || n != -9 {
		t.Fatal("ordinary CHECK did not read supplied value", value, err)
	}
	// The source map contains two attributes, but destination inputs start at 2.
	wide := *source
	wide.Columns = append(append([]catalog.Column(nil), source.Columns...), source.Columns...)
	wide.Attributes = []int32{3, 4, 1, 2}
	base := plan.ColumnExpr{Info: expr.Info{Usage: typ.Usage()}, Scope: 1, Frame: 1, Slot: 2}
	wide.ColumnMeta.Generations = map[int32]catalog.ColumnGeneration{2: {Kind: catalog.GenerationVirtual, Expression: base, Planned: base}}
	copied, err := stored.RemapScalar(old, nil, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, map[int32]int{1: 2, 2: 3}, nil)
	if err != nil {
		t.Fatal(err)
	}
	copiedColumn := copied.(plan.ColumnExpr)
	if copiedColumn.Virtual != nil || copiedColumn.QueryOccurrence != 0 || copiedColumn.RowWidth != 0 || copiedColumn.Slot != 3 {
		t.Fatal("owner copy retained source execution recipe", copiedColumn)
	}
	wide.Constraints.Check = []catalog.CheckConstraint{{Name: "positive", Expression: copied}}
	if err := stored.RefreshGenerationChecks(&wide, view); err != nil {
		t.Fatal(err)
	}
	column = wide.Constraints.Check[0].Expression.(plan.ColumnExpr)
	if column.Slot != 3 || column.RowStart != 0 || column.RowWidth != 4 || column.QueryOccurrence != 0 {
		t.Fatal("destination CHECK layout", column)
	}
	row := []sqltype.Value{seven, seven, one, sqltype.Null(typ)}
	value, err = column.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1, Row: row}})
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
	literal := plan.LiteralExpr{Info: expr.Info{Usage: typ.Usage()}, Value: one}
	d.ColumnMeta.Generations = map[int32]catalog.ColumnGeneration{2: {Kind: catalog.GenerationVirtual, Expression: literal, Planned: literal}}
	left := plan.ColumnExpr{Info: expr.Info{Usage: typ.Usage()}, Origin: origin, Scope: 1, Frame: 4, Slot: 1, QueryOccurrence: 1}.WithGeneration(d, 1, 0)
	right := left
	right.Slot, right.RowStart, right.QueryOccurrence = 3, 2, 2
	provider, err := sqltype.BindArithmetic(sqltype.ArithmeticAdd, typ, typ, typ)
	if err != nil {
		t.Fatal(err)
	}
	add := plan.ArithmeticExpr{Info: expr.Info{Usage: typ.Usage()}, Left: left, Right: literal, Provider: provider, Operator: sqltype.ArithmeticAdd}
	boolean := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil).Type(sqltype.Bool)
	test := plan.NullTestExpr{Info: expr.Info{Usage: boolean.Usage()}, Child: right}
	choice := plan.CaseExpr{Info: expr.Info{Usage: typ.Usage()}, Arms: []plan.CaseBranch{{Condition: test, Result: add}}, Otherwise: right}
	outside := left
	outside.Frame = 99
	syntax := &parser.Select{}
	join := &plan.JoinNode{Left: &plan.SourceNode{Ref: ref, Occurrence: 0}, Right: &plan.SourceNode{Ref: ref, Occurrence: 1}}
	project := &plan.ProjectNode{Frame: 4, Syntax: syntax, Child: join, Expressions: []expr.Expr{left, right, add, test, choice, outside}}
	definition := stored.NewView(stored.ViewParts{Queries: map[*parser.Select]*plan.ProjectNode{syntax: project}, Sources: []viewLeaf{{Ref: ref}, {Ref: ref}}})
	consumer := *d
	consumer.View = stored.ViewPayload(definition)
	w := &viewWrite{layers: []viewWriteLayer{{relation: &consumer}}}
	b := analyze.Binding{QuerySources: []exec.Leaf{{Ref: ref, Relation: d}}}
	mapped, err := w.graftQuery(&b, view, project, func(expr expr.Expr) (expr.Expr, bool, error) { return expr, false, nil })
	if err != nil {
		t.Fatal(err)
	}
	next := mapped.(*plan.ProjectNode)
	nextJoin := next.Child.(*plan.JoinNode)
	l, r := next.Expressions[0].(plan.ColumnExpr), next.Expressions[1].(plan.ColumnExpr)
	if len(b.QuerySources) != 3 || l.QueryOccurrence != 2 || r.QueryOccurrence != 3 || nextJoin.Left.(*plan.SourceNode).Occurrence != 1 || nextJoin.Right.(*plan.SourceNode).Occurrence != 2 {
		t.Fatal("grafted aliases lost occurrence mapping", l, r, nextJoin, len(b.QuerySources))
	}
	wrapped := next.Expressions[2].(plan.ArithmeticExpr)
	nullTest := next.Expressions[3].(plan.NullTestExpr)
	nextChoice := next.Expressions[4].(plan.CaseExpr)
	if wrapped.Left.(plan.ColumnExpr).QueryOccurrence != 2 || nullTest.Child.(plan.ColumnExpr).QueryOccurrence != 3 || nextChoice.Arms[0].Condition.(plan.NullTestExpr).Child.(plan.ColumnExpr).QueryOccurrence != 3 || next.Expressions[5].(plan.ColumnExpr).QueryOccurrence != 1 {
		t.Fatal("compound or outside lexical occurrence remap")
	}
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 4, Row: []sqltype.Value{sqltype.Null(typ), sqltype.Null(typ), sqltype.Null(typ), sqltype.Null(typ)}, Query: &exec.State{Source: &exec.Source{Sources: []*exec.Source{{Occurrence: 9}, {Occurrence: 4}, {Occurrence: 7}}}}, QueryOccurrences: []int{4, 7}, QueryProvenance: []int{exec.AbsentQueryIndex, 0}}}
	value, err := wrapped.Eval(&evaluation)
	if err != nil || !value.IsNull() {
		t.Fatal("absent compound graft recomputed recipe", value, err)
	}
	evaluation.QueryProvenance = []int{0, exec.AbsentQueryIndex}
	value, err = nextChoice.Eval(&evaluation)
	if n, access := value.Integer(); err != nil || access != nil || n != 2 {
		t.Fatal("present CASE graft evaluation", value, err)
	}
	innerLeft, innerRight := left, right
	innerLeft.Frame, innerRight.Frame = 8, 8
	innerAdd := add
	innerAdd.Left = innerLeft
	inner := &plan.ProjectNode{Frame: 8, Child: join, Expressions: []expr.Expr{innerAdd, innerRight}}
	outer := &plan.ProjectNode{Frame: 4, Syntax: syntax, Child: &plan.DerivedNode{Query: &plan.Query{Root: inner}}}
	derived, err := w.graftQuery(&b, view, outer, func(expr expr.Expr) (expr.Expr, bool, error) { return expr, false, nil })
	if err != nil {
		t.Fatal(err)
	}
	nextInner := derived.(*plan.ProjectNode).Child.(*plan.DerivedNode).Query.Root.(*plan.ProjectNode)
	innerColumn := nextInner.Expressions[0].(plan.ArithmeticExpr).Left.(plan.ColumnExpr)
	if innerColumn.QueryOccurrence != 2 || nextInner.Child.(*plan.JoinNode).Left.(*plan.SourceNode).Occurrence != 1 || innerAdd.Left.(plan.ColumnExpr).QueryOccurrence != 1 {
		t.Fatal("derived lexical frame lost mapping or mutated original")
	}
	evaluation.Frame = 8
	value, err = nextInner.Expressions[0].Eval(&evaluation)
	if n, access := value.Integer(); err != nil || access != nil || n != 2 {
		t.Fatal("derived present graft evaluation", value, err)
	}
	evaluation.QueryProvenance[0] = exec.AbsentQueryIndex
	value, err = nextInner.Expressions[0].Eval(&evaluation)
	if err != nil || !value.IsNull() {
		t.Fatal("derived absent graft evaluation", value, err)
	}
	if left.QueryOccurrence != 1 || right.QueryOccurrence != 2 || join.Left.(*plan.SourceNode).Occurrence != 0 {
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
		column := plan.ColumnExpr{Info: expr.Info{Usage: typ.Usage()}, Scope: 1, Frame: 1, Slot: 2, Virtual: probe, RowStart: 1, RowWidth: 2, QueryOccurrence: 2}
		left := &exec.Source{Anonymous: [][]sqltype.Value{{one}}, Occurrence: 0}
		right := &exec.Source{Anonymous: [][]sqltype.Value{}, Occurrence: 1}
		if present {
			right.Anonymous = [][]sqltype.Value{{sqltype.Null(typ), sqltype.Null(typ)}}
		}
		state := &exec.State{Source: &exec.Source{Sources: []*exec.Source{left, right}}, VirtualReads: true}
		schema := plan.QuerySchema{d.Columns[0], d.Columns[0], d.Columns[1], d.Columns[1]}
		join := &plan.JoinNode{QuerySchema: schema, Kind: parser.JoinLeft, Left: &plan.SourceNode{QuerySchema: schema[:1], Occurrence: 0}, Right: &plan.SourceNode{QuerySchema: schema[1:3], Occurrence: 1}, LeftWidth: 1, RightWidth: 2, Merges: []expr.Expr{column}}
		stage, err := exec.OpenJoinNode(context.Background(), state, join)
		if err != nil {
			t.Fatal(err)
		}
		if err := stage.Drain(context.Background()); err != nil || stage.Count() != 1 {
			t.Fatal("join production", err, stage.Count())
		}
		evaluation := state.Evaluation(context.Background())
		evaluation.BindQueryRow(stage.Source, stage.Index(0))
		value, err := column.Eval(&evaluation)
		if err != nil || value.IsNull() == present || stage.Source.Row(0)[3].IsNull() == present {
			t.Fatal("transient merge/saved projection identity", present, value, err, stage.Source.Row(0))
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
