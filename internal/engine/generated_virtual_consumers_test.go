package engine

import (
	"context"
	"reflect"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestGeneratedVirtualMissingIdentity(t *testing.T) {
	_, _, d := generatedUnitDescriptor(t)
	typ := d.columns[0].Usage.Type
	one, _ := sqltype.Integer(typ, 1)
	calls := 0
	column := columnExpression{expressionInfo: expressionInfo{usage: typ.Usage()}, scope: 1, frame: 1, slot: 0, rowWidth: 1, queryOccurrence: 1, virtual: generatedOccurrenceProbe{expressionInfo: expressionInfo{usage: typ.Usage()}, calls: &calls, value: one}}
	for _, state := range []*queryState{nil, {}, {source: &querySource{occurrence: 4}}, {source: &querySource{sources: []*querySource{nil}}}} {
		c := EvalContext{Context: context.Background(), Scope: 1, Frame: 1, Row: []sqltype.Value{sqltype.Null(typ)}, query: state}
		if _, err := column.Eval(&c); err == nil || calls != 0 {
			t.Fatal("missing authority succeeded or demanded recipe", err, calls)
		}
	}
	c := EvalContext{Context: context.Background(), Scope: 1, Frame: 1, Row: []sqltype.Value{sqltype.Null(typ)}, query: &queryState{source: &querySource{occurrence: 4}}, queryOccurrences: []int{4}, queryProvenance: []int{0}}
	column.queryOccurrence = 2
	if _, err := column.Eval(&c); err == nil || calls != 0 {
		t.Fatal("invalid local occurrence did not fail", err, calls)
	}
	column.queryOccurrence = 1
	c.queryProvenance = nil
	if _, err := column.Eval(&c); err == nil || calls != 0 {
		t.Fatal("inconsistent identity width did not fail", err, calls)
	}
}

func TestGeneratedVirtualExecutableNestedDemand(t *testing.T) {
	_, _, d := generatedUnitDescriptor(t)
	typ := d.columns[0].Usage.Type
	column := columnExpression{expressionInfo: expressionInfo{usage: typ.Usage()}, virtual: literalExpression{expressionInfo: expressionInfo{usage: typ.Usage()}, value: sqltype.Null(typ)}, queryOccurrence: 1}
	query := &BoundQuery{Root: &projectNode{expressions: []BoundExpr{column}}}
	edge := subqueryExpression{expressionInfo: expressionInfo{usage: typ.Usage()}, queryPlan: query}
	if !boundHasVirtualReads(caseExpression{expressionInfo: expressionInfo{usage: typ.Usage()}, otherwise: edge}) {
		t.Fatal("executable scalar query demand was not found")
	}
	edge.cteDefinition = true
	if boundHasVirtualReads(edge) || queryHasVirtualReads(&projectNode{analysisExpressions: []BoundExpr{edge}}) {
		t.Fatal("unused WITH definition acquired runtime demand")
	}
	column.queryOccurrence = 0
	if boundHasVirtualReads(column) {
		t.Fatal("owner recipe acquired query demand")
	}
}

func TestGeneratedVirtualWindowInputIdentityAdapter(t *testing.T) {
	_, _, d := generatedUnitDescriptor(t)
	typ := d.columns[0].Usage.Type
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
	if len(versions) != 1 || !reflect.DeepEqual(versions[0].values, leaf.row(1)) {
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
	typ := d.columns[0].Usage.Type
	one, _ := sqltype.Integer(typ, 1)
	for _, present := range []bool{false, true} {
		calls := 0
		probe := generatedOccurrenceProbe{expressionInfo: expressionInfo{usage: typ.Usage()}, calls: &calls, value: one}
		column := columnExpression{expressionInfo: expressionInfo{usage: typ.Usage()}, scope: 1, frame: 1, slot: 1, virtual: probe, rowWidth: 2, queryOccurrence: 1}
		index := absentQueryIndex
		wantCalls := 0
		if present {
			index = 0
			wantCalls = 1
		}
		joined := &querySource{materialized: true, flatWidth: 2, flatCount: 1, anonymous: [][]sqltype.Value{{sqltype.Null(typ), sqltype.Null(typ)}}, occurrence: 4, occurrences: []int{4}, provenance: []int{index}}
		state := &queryState{source: joined, virtualReads: true}
		node := &evaluationNode{querySchema: d.columns, child: &sourceNode{querySchema: d.columns, occurrence: 0}, expressions: []BoundExpr{caseExpression{expressionInfo: expressionInfo{usage: typ.Usage()}, otherwise: column}}}
		stage, err := openEvaluationNode(context.Background(), state, node)
		if err != nil {
			t.Fatal(err)
		}
		i, ok, err := stage.nextOccurrence(context.Background())
		if err != nil || !ok || stage.orderRow(i)[0].IsNull() == present || calls != wantCalls {
			t.Fatal("scheduled cache lost identity", present, ok, err, calls)
		}
		if !joined.anonymous[0][1].IsNull() || column.queryOccurrence != 1 {
			t.Fatal("scheduled evaluation mutated physical input")
		}
		if err := stage.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestGeneratedVirtualRetainedViewProjectionIdentity(t *testing.T) {
	_, _, d := generatedUnitDescriptor(t)
	typ := d.columns[0].Usage.Type
	one, _ := sqltype.Integer(typ, 1)
	for _, present := range []bool{false, true} {
		calls, index, wantCalls := 0, absentQueryIndex, 0
		if present {
			index, wantCalls = 0, 1
		}
		input := &querySource{materialized: true, flatWidth: 2, flatCount: 1, anonymous: [][]sqltype.Value{{sqltype.Null(typ), sqltype.Null(typ)}}, occurrence: 4, occurrences: []int{4}, provenance: []int{index}}
		probe := generatedOccurrenceProbe{expressionInfo: expressionInfo{usage: typ.Usage()}, calls: &calls, value: one}
		column := columnExpression{expressionInfo: expressionInfo{usage: typ.Usage()}, scope: 1, frame: 1, slot: 1, rowWidth: 2, queryOccurrence: 1, virtual: probe}
		schema := querySchema{d.columns[1]}
		project := &projectNode{querySchema: schema, frame: 1, child: &sourceNode{querySchema: d.columns, occurrence: 0}, expressions: []BoundExpr{column}}
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
