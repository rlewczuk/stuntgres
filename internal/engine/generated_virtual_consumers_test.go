package engine

import (
	"context"
	"reflect"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestGeneratedVirtualMissingIdentity(t *testing.T) {
	_, _, d := generatedUnitDescriptor(t)
	typ := d.Columns[0].Usage.Type
	one, _ := sqltype.Integer(typ, 1)
	calls := 0
	column := plan.ColumnExpr{Info: expr.Info{Usage: typ.Usage()}, Scope: 1, Frame: 1, Slot: 0, RowWidth: 1, QueryOccurrence: 1, Virtual: generatedOccurrenceProbe{Info: expr.Info{Usage: typ.Usage()}, calls: &calls, value: one}}
	for _, state := range []*queryState{nil, {}, {Source: &querySource{Occurrence: 4}}, {Source: &querySource{Sources: []*querySource{nil}}}} {
		c := EvalContext{evalStatement: evalStatement{Context: context.Background()}, evalFrame: evalFrame{Scope: 1, Frame: 1, Row: []sqltype.Value{sqltype.Null(typ)}, Query: state}}
		if _, err := column.Eval(&c); err == nil || calls != 0 {
			t.Fatal("missing authority succeeded or demanded recipe", err, calls)
		}
	}
	c := EvalContext{evalStatement: evalStatement{Context: context.Background()}, evalFrame: evalFrame{Scope: 1, Frame: 1, Row: []sqltype.Value{sqltype.Null(typ)}, Query: &queryState{Source: &querySource{Occurrence: 4}}, QueryOccurrences: []int{4}, QueryProvenance: []int{0}}}
	column.QueryOccurrence = 2
	if _, err := column.Eval(&c); err == nil || calls != 0 {
		t.Fatal("invalid local occurrence did not fail", err, calls)
	}
	column.QueryOccurrence = 1
	c.QueryProvenance = nil
	if _, err := column.Eval(&c); err == nil || calls != 0 {
		t.Fatal("inconsistent identity width did not fail", err, calls)
	}
}

func TestGeneratedVirtualExecutableNestedDemand(t *testing.T) {
	_, _, d := generatedUnitDescriptor(t)
	typ := d.Columns[0].Usage.Type
	column := plan.ColumnExpr{Info: expr.Info{Usage: typ.Usage()}, Virtual: plan.LiteralExpr{Info: expr.Info{Usage: typ.Usage()}, Value: sqltype.Null(typ)}, QueryOccurrence: 1}
	query := &plan.Query{Root: &plan.ProjectNode{Expressions: []expr.Expr{column}}}
	edge := plan.SubqueryExpr{Info: expr.Info{Usage: typ.Usage()}, QueryPlan: query}
	if !BoundHasVirtualReads(plan.CaseExpr{Info: expr.Info{Usage: typ.Usage()}, Otherwise: edge}) {
		t.Fatal("executable scalar query demand was not found")
	}
	edge.CTEEdge = true
	if BoundHasVirtualReads(edge) || QueryHasVirtualReads(&plan.ProjectNode{AnalysisExpressions: []expr.Expr{edge}}) {
		t.Fatal("unused WITH definition acquired runtime demand")
	}
	column.QueryOccurrence = 0
	if BoundHasVirtualReads(column) {
		t.Fatal("owner recipe acquired query demand")
	}
}

func TestGeneratedVirtualWindowInputIdentityAdapter(t *testing.T) {
	_, _, d := generatedUnitDescriptor(t)
	typ := d.Columns[0].Usage.Type
	one, _ := sqltype.Integer(typ, 1)
	two, _ := sqltype.Integer(typ, 2)
	leaf := &querySource{Anonymous: [][]sqltype.Value{{one}, {two}}, Occurrence: 7}
	lineage := &queryLockLineage{Stage: leaf, Selection: []int{0, 1}}
	input := &querySource{Anonymous: [][]sqltype.Value{{one}, {two}}, Lineage: lineage}
	window := &querySource{Input: input, Window: true, WindowCount: 2, WindowIndices: []int{1, 0}, Scratch: make([]sqltype.Value, 1)}
	if value, _ := window.Row(0)[0].Integer(); value != 2 {
		t.Fatal("row adapter did not translate", value)
	}
	if got := AppendQueryProvenance(nil, window, 0); !reflect.DeepEqual(got, []int{1}) {
		t.Fatal("provenance used a different input row", got)
	}
	versions := AppendQueryVersions(nil, window, 0, nil)
	if len(versions) != 1 || !reflect.DeepEqual(versions[0].Values, leaf.Row(1)) {
		t.Fatal("versions used a different input row", versions)
	}
	SetQueryLineage(window, 0)
	if lineage.CurrentIndex != 1 {
		t.Fatal("lineage used a different input row", lineage.CurrentIndex)
	}
	SetQueryLineage(window, AbsentQueryIndex)
	if lineage.CurrentIndex != AbsentQueryIndex || !reflect.DeepEqual(AppendQueryProvenance(nil, window, AbsentQueryIndex), []int{AbsentQueryIndex}) {
		t.Fatal("adapter lost absence")
	}
}

func TestGeneratedVirtualScheduledProjectionIdentity(t *testing.T) {
	_, _, d := generatedUnitDescriptor(t)
	typ := d.Columns[0].Usage.Type
	one, _ := sqltype.Integer(typ, 1)
	for _, present := range []bool{false, true} {
		calls := 0
		probe := generatedOccurrenceProbe{Info: expr.Info{Usage: typ.Usage()}, calls: &calls, value: one}
		column := plan.ColumnExpr{Info: expr.Info{Usage: typ.Usage()}, Scope: 1, Frame: 1, Slot: 1, Virtual: probe, RowWidth: 2, QueryOccurrence: 1}
		index := AbsentQueryIndex
		wantCalls := 0
		if present {
			index = 0
			wantCalls = 1
		}
		joined := &querySource{Materialized: true, FlatWidth: 2, FlatCount: 1, Anonymous: [][]sqltype.Value{{sqltype.Null(typ), sqltype.Null(typ)}}, Occurrence: 4, Occurrences: []int{4}, Provenance: []int{index}}
		state := &queryState{Source: joined, VirtualReads: true}
		node := &plan.EvaluationNode{QuerySchema: d.Columns, Child: &plan.SourceNode{QuerySchema: d.Columns, Occurrence: 0}, Expressions: []expr.Expr{plan.CaseExpr{Info: expr.Info{Usage: typ.Usage()}, Otherwise: column}}}
		stage, err := OpenEvaluationNode(context.Background(), state, node)
		if err != nil {
			t.Fatal(err)
		}
		i, ok, err := stage.NextOccurrence(context.Background())
		if err != nil || !ok || stage.OrderRow(i)[0].IsNull() == present || calls != wantCalls {
			t.Fatal("scheduled cache lost identity", present, ok, err, calls)
		}
		if !joined.Anonymous[0][1].IsNull() || column.QueryOccurrence != 1 {
			t.Fatal("scheduled evaluation mutated physical input")
		}
		if err := stage.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestGeneratedVirtualRetainedViewProjectionIdentity(t *testing.T) {
	_, _, d := generatedUnitDescriptor(t)
	typ := d.Columns[0].Usage.Type
	one, _ := sqltype.Integer(typ, 1)
	for _, present := range []bool{false, true} {
		calls, index, wantCalls := 0, AbsentQueryIndex, 0
		if present {
			index, wantCalls = 0, 1
		}
		input := &querySource{Materialized: true, FlatWidth: 2, FlatCount: 1, Anonymous: [][]sqltype.Value{{sqltype.Null(typ), sqltype.Null(typ)}}, Occurrence: 4, Occurrences: []int{4}, Provenance: []int{index}}
		probe := generatedOccurrenceProbe{Info: expr.Info{Usage: typ.Usage()}, calls: &calls, value: one}
		column := plan.ColumnExpr{Info: expr.Info{Usage: typ.Usage()}, Scope: 1, Frame: 1, Slot: 1, RowWidth: 2, QueryOccurrence: 1, Virtual: probe}
		schema := plan.QuerySchema{d.Columns[1]}
		project := &plan.ProjectNode{QuerySchema: schema, Frame: 1, Child: &plan.SourceNode{QuerySchema: d.Columns, Occurrence: 0}, Expressions: []expr.Expr{column}}
		view := &querySource{Materialized: true, FlatWidth: 1, NestedRoot: project, NestedInputs: input}
		state := &queryState{Source: view}
		stage, err := OpenSourceStage(context.Background(), state, view, schema)
		if err != nil {
			t.Fatal(err)
		}
		_, ok, err := stage.NextOccurrence(context.Background())
		if err != nil || !ok || view.Flat[0].IsNull() == present || calls != wantCalls {
			t.Fatal("retained view projection lost inner identity", present, ok, err, calls)
		}
		if view.Flat[0].Type() != typ || !input.Anonymous[0][1].IsNull() {
			t.Fatal("view materialization lost type or mutated physical input")
		}
		if err := stage.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestGeneratedVirtualSelectedOrdinalIdentity(t *testing.T) {
	_, _, d := generatedUnitDescriptor(t)
	typ := d.Columns[0].Usage.Type
	one, _ := sqltype.Integer(typ, 1)
	calls := 0
	probe := generatedOccurrenceProbe{Info: expr.Info{Usage: typ.Usage()}, calls: &calls, value: one}
	column := plan.ColumnExpr{Info: expr.Info{Usage: typ.Usage()}, Scope: 1, Frame: 1, Slot: 1, Virtual: probe, RowWidth: 2, QueryOccurrence: 1}
	source := &querySource{
		Materialized: true, FlatWidth: 2, FlatCount: 3, Occurrence: 4,
		Anonymous: [][]sqltype.Value{
			{sqltype.Null(typ), sqltype.Null(typ)},
			{sqltype.Null(typ), sqltype.Null(typ)},
			{sqltype.Null(typ), sqltype.Null(typ)},
		},
		Occurrences: []int{4}, Provenance: []int{0, AbsentQueryIndex, 1},
	}
	state := &queryState{Source: source, VirtualReads: true}
	stage := indexedStage{
		Source: source, Columns: []catalog.Column{d.Columns[1]},
		Expressions: []expr.Expr{column}, Retain: true,
		Permutation: []int{2, 0, 1},
		Evaluation:  EvalContext{evalFrame: evalFrame{Scope: 1, Frame: 1, Query: state}},
	}
	dst := make([]sqltype.Value, 1)
	for _, seek := range []struct {
		position, ordinal int
		present           bool
	}{
		{2, 0, true},  // frontier absent, selected occurrence present
		{0, 2, false}, // frontier present, selected occurrence absent
		{3, 1, true},  // frontier at EOF, selected occurrence present
		{3, 2, false}, // frontier at EOF, selected occurrence absent
	} {
		stage.Position = seek.position
		before := calls
		ok, err := stage.ProjectOrdinal(context.Background(), seek.ordinal, dst)
		wantCalls := before
		if seek.present {
			wantCalls++
		}
		if err != nil || !ok || dst[0].Type() != typ || dst[0].IsNull() == seek.present || calls != wantCalls {
			t.Fatal("selected ordinal lost virtual identity", seek, ok, err, dst, calls)
		}
		if stage.Position != seek.position {
			t.Fatal("random projection advanced sequential frontier", stage.Position, seek)
		}
	}
	stage.Position = 3
	before := calls
	if ok, err := stage.ProjectOrdinal(context.Background(), 3, dst); err != nil || ok || calls != before {
		t.Fatal("EOF projected a nonexistent occurrence", ok, err, calls)
	}
	for _, row := range source.Anonymous {
		if !row[0].IsNull() || !row[1].IsNull() {
			t.Fatal("generated projection mutated physical input", row)
		}
	}
	if err := stage.Close(); err != nil {
		t.Fatal(err)
	}
}
