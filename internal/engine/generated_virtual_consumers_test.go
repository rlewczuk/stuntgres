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
	for _, state := range []*queryState{nil, {}, {source: &querySource{occurrence: 4}}, {source: &querySource{sources: []*querySource{nil}}}} {
		c := EvalContext{evalStatement: evalStatement{Context: context.Background()}, evalFrame: evalFrame{Scope: 1, Frame: 1, Row: []sqltype.Value{sqltype.Null(typ)}, query: state}}
		if _, err := column.Eval(&c); err == nil || calls != 0 {
			t.Fatal("missing authority succeeded or demanded recipe", err, calls)
		}
	}
	c := EvalContext{evalStatement: evalStatement{Context: context.Background()}, evalFrame: evalFrame{Scope: 1, Frame: 1, Row: []sqltype.Value{sqltype.Null(typ)}, query: &queryState{source: &querySource{occurrence: 4}}, queryOccurrences: []int{4}, queryProvenance: []int{0}}}
	column.QueryOccurrence = 2
	if _, err := column.Eval(&c); err == nil || calls != 0 {
		t.Fatal("invalid local occurrence did not fail", err, calls)
	}
	column.QueryOccurrence = 1
	c.queryProvenance = nil
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
	if !boundHasVirtualReads(plan.CaseExpr{Info: expr.Info{Usage: typ.Usage()}, Otherwise: edge}) {
		t.Fatal("executable scalar query demand was not found")
	}
	edge.CTEEdge = true
	if boundHasVirtualReads(edge) || queryHasVirtualReads(&plan.ProjectNode{AnalysisExpressions: []expr.Expr{edge}}) {
		t.Fatal("unused WITH definition acquired runtime demand")
	}
	column.QueryOccurrence = 0
	if boundHasVirtualReads(column) {
		t.Fatal("owner recipe acquired query demand")
	}
}

func TestGeneratedVirtualWindowInputIdentityAdapter(t *testing.T) {
	_, _, d := generatedUnitDescriptor(t)
	typ := d.Columns[0].Usage.Type
	one, _ := sqltype.Integer(typ, 1)
	two, _ := sqltype.Integer(typ, 2)
	leaf := &querySource{anonymous: [][]sqltype.Value{{one}, {two}}, occurrence: 7}
	lineage := &queryLockLineage{stage: leaf, selection: []int{0, 1}}
	input := &querySource{anonymous: [][]sqltype.Value{{one}, {two}}, lineage: lineage}
	window := &querySource{input: input, window: true, windowCount: 2, windowIndices: []int{1, 0}, scratch: make([]sqltype.Value, 1)}
	if value, _ := window.row(0)[0].Integer(); value != 2 {
		t.Fatal("row adapter did not translate", value)
	}
	if got := appendQueryProvenance(nil, window, 0); !reflect.DeepEqual(got, []int{1}) {
		t.Fatal("provenance used a different input row", got)
	}
	versions := appendQueryVersions(nil, window, 0, nil)
	if len(versions) != 1 || !reflect.DeepEqual(versions[0].Values, leaf.row(1)) {
		t.Fatal("versions used a different input row", versions)
	}
	setQueryLineage(window, 0)
	if lineage.currentIndex != 1 {
		t.Fatal("lineage used a different input row", lineage.currentIndex)
	}
	setQueryLineage(window, absentQueryIndex)
	if lineage.currentIndex != absentQueryIndex || !reflect.DeepEqual(appendQueryProvenance(nil, window, absentQueryIndex), []int{absentQueryIndex}) {
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
		index := absentQueryIndex
		wantCalls := 0
		if present {
			index = 0
			wantCalls = 1
		}
		joined := &querySource{materialized: true, flatWidth: 2, flatCount: 1, anonymous: [][]sqltype.Value{{sqltype.Null(typ), sqltype.Null(typ)}}, occurrence: 4, occurrences: []int{4}, provenance: []int{index}}
		state := &queryState{source: joined, virtualReads: true}
		node := &plan.EvaluationNode{QuerySchema: d.Columns, Child: &plan.SourceNode{QuerySchema: d.Columns, Occurrence: 0}, Expressions: []expr.Expr{plan.CaseExpr{Info: expr.Info{Usage: typ.Usage()}, Otherwise: column}}}
		stage, err := openEvaluationNode(context.Background(), state, node)
		if err != nil {
			t.Fatal(err)
		}
		i, ok, err := stage.nextOccurrence(context.Background())
		if err != nil || !ok || stage.orderRow(i)[0].IsNull() == present || calls != wantCalls {
			t.Fatal("scheduled cache lost identity", present, ok, err, calls)
		}
		if !joined.anonymous[0][1].IsNull() || column.QueryOccurrence != 1 {
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
		calls, index, wantCalls := 0, absentQueryIndex, 0
		if present {
			index, wantCalls = 0, 1
		}
		input := &querySource{materialized: true, flatWidth: 2, flatCount: 1, anonymous: [][]sqltype.Value{{sqltype.Null(typ), sqltype.Null(typ)}}, occurrence: 4, occurrences: []int{4}, provenance: []int{index}}
		probe := generatedOccurrenceProbe{Info: expr.Info{Usage: typ.Usage()}, calls: &calls, value: one}
		column := plan.ColumnExpr{Info: expr.Info{Usage: typ.Usage()}, Scope: 1, Frame: 1, Slot: 1, RowWidth: 2, QueryOccurrence: 1, Virtual: probe}
		schema := plan.QuerySchema{d.Columns[1]}
		project := &plan.ProjectNode{QuerySchema: schema, Frame: 1, Child: &plan.SourceNode{QuerySchema: d.Columns, Occurrence: 0}, Expressions: []expr.Expr{column}}
		view := &querySource{materialized: true, flatWidth: 1, nestedRoot: project, nestedInputs: input}
		state := &queryState{source: view}
		stage, err := openSourceStage(context.Background(), state, view, schema)
		if err != nil {
			t.Fatal(err)
		}
		_, ok, err := stage.nextOccurrence(context.Background())
		if err != nil || !ok || view.flat[0].IsNull() == present || calls != wantCalls {
			t.Fatal("retained view projection lost inner identity", present, ok, err, calls)
		}
		if view.flat[0].Type() != typ || !input.anonymous[0][1].IsNull() {
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
		materialized: true, flatWidth: 2, flatCount: 3, occurrence: 4,
		anonymous: [][]sqltype.Value{
			{sqltype.Null(typ), sqltype.Null(typ)},
			{sqltype.Null(typ), sqltype.Null(typ)},
			{sqltype.Null(typ), sqltype.Null(typ)},
		},
		occurrences: []int{4}, provenance: []int{0, absentQueryIndex, 1},
	}
	state := &queryState{source: source, virtualReads: true}
	stage := indexedStage{
		source: source, schema: []catalog.Column{d.Columns[1]},
		expressions: []expr.Expr{column}, retain: true,
		permutation: []int{2, 0, 1},
		evaluation:  EvalContext{evalFrame: evalFrame{Scope: 1, Frame: 1, query: state}},
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
		stage.position = seek.position
		before := calls
		ok, err := stage.projectOrdinal(context.Background(), seek.ordinal, dst)
		wantCalls := before
		if seek.present {
			wantCalls++
		}
		if err != nil || !ok || dst[0].Type() != typ || dst[0].IsNull() == seek.present || calls != wantCalls {
			t.Fatal("selected ordinal lost virtual identity", seek, ok, err, dst, calls)
		}
		if stage.position != seek.position {
			t.Fatal("random projection advanced sequential frontier", stage.position, seek)
		}
	}
	stage.position = 3
	before := calls
	if ok, err := stage.projectOrdinal(context.Background(), 3, dst); err != nil || ok || calls != before {
		t.Fatal("EOF projected a nonexistent occurrence", ok, err, calls)
	}
	for _, row := range source.anonymous {
		if !row[0].IsNull() || !row[1].IsNull() {
			t.Fatal("generated projection mutated physical input", row)
		}
	}
	if err := stage.Close(); err != nil {
		t.Fatal(err)
	}
}
