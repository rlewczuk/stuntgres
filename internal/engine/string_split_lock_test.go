package engine

import (
	"context"
	"reflect"
	"strings"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestStringSplitCopiedFunction(t *testing.T) {
	b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
	text, bigint := b.typ(sqltype.Text), b.typ(sqltype.Int8)
	var evaluations []int
	dependent := columnExpression{expressionInfo: expressionInfo{Usage: text.Usage(), Property: ExprProperties{RowDependent: true}}, scope: 1, frame: 1, slot: 0}
	independent := mathObservedArgument{expressionInfo: expressionInfo{Usage: text.Usage()}, value: sqltype.String(text, "x,y"), order: &evaluations, index: 0}
	call := stringSplitMarker(t, b, 1, dependent, stringSplitLiteral(b, ","), stringSplitLiteral(b, "replacement"))
	peer := stringSplitMarker(t, b, 2, independent, stringSplitLiteral(b, ","))
	node := &functionFromNode{calls: []BoundExpr{call, peer}, ordinality: true, frame: 1, querySchema: querySchema{{Usage: text.Usage()}, {Usage: text.Usage()}, {Usage: bigint.Usage()}}}
	ordinal, err := sqltype.Integer(bigint, 2)
	if err != nil {
		t.Fatal(err)
	}
	frozen := []sqltype.Value{sqltype.String(text, "old"), sqltype.String(text, "original"), ordinal}
	scope := &StatementScope{queryLocks: &queryLockContext{}}
	inputs := &querySource{scope: scope}
	width := queryLockRecheckWidth(node, inputs)
	if width != 3 {
		t.Fatal("wrong refreshed function frame scratch", width)
	}
	if physical := queryTreeOccurrences(node, inputs, nil); len(physical) != 0 {
		t.Fatal("computed function acquired physical identity", physical)
	}
	state := &queryState{source: inputs, frame: 1, invocation: &EvalContext{Scope: 1, Frame: 1, Row: []sqltype.Value{sqltype.String(text, "replacement")}}}
	var cache functionRecheckCache
	defer cache.close()
	evaluation := EvalContext{Context: context.Background(), Scope: 1, Frame: 1, statementScope: scope, query: state, functionRecheckCache: &cache}
	scratch := make([]sqltype.Value, width)
	for range 2 {
		offset := 0
		got, present, err := recheckLockedQuery(node, inputs, nil, &evaluation, scratch, &offset, frozen)
		if err != nil || !present || len(got) != 3 || !got[0].IsNull() {
			t.Fatal("refreshed NULL field lost", got, present, err)
		}
		field, _ := got[1].Text()
		ordinal, _ := got[2].Integer()
		if field != "x" || ordinal != 1 || !reflect.DeepEqual(evaluations, []int{0}) {
			t.Fatal("independent invocation failed rewind or ordinal reset", got, evaluations)
		}
	}
	// A recheck searches later fields rather than testing only the first field.
	filteredCall := stringSplitMarker(t, b, 3, dependent, stringSplitLiteral(b, ","))
	filteredNode := &functionFromNode{calls: []BoundExpr{filteredCall}, ordinality: true, frame: 1, querySchema: querySchema{{Usage: text.Usage()}, {Usage: bigint.Usage()}}}
	field := columnExpression{expressionInfo: expressionInfo{Usage: text.Usage(), Property: ExprProperties{RowDependent: true}}, scope: 1, frame: 1, slot: 0}
	raw := statement(t, "SELECT 'x'='b'").(*parser.Select).Targets[0].(parser.OperatorExpr)
	predicate, err := b.bindComparisonOperands(raw, sqltype.CompareEqual, comparisonOperand{node: field}, comparisonOperand{node: stringSplitLiteral(b, "b")})
	if err != nil {
		t.Fatal(err)
	}
	filter := &filterNode{querySchema: filteredNode.querySchema, child: filteredNode, predicate: predicate}
	state.invocation.Row[0] = sqltype.String(text, "x,b")
	offset := 0
	scratch = make([]sqltype.Value, queryLockRecheckWidth(filter, inputs))
	got, present, err := recheckLockedQuery(filter, inputs, nil, &evaluation, scratch, &offset, frozen)
	if err != nil || !present {
		t.Fatal(got, present, err)
	}
	value, _ := got[0].Text()
	position, _ := got[1].Integer()
	if value != "b" || position != 2 {
		t.Fatal("later qualifying replacement field not found", got)
	}
	set := &setProjectNode{child: &singletonNode{}}
	if physical := queryTreeOccurrences(set, inputs, nil); len(physical) != 0 {
		t.Fatal(physical)
	}
	if width := queryLockRecheckWidth(set, inputs); width != 0 {
		t.Fatal("source-free expansion needs physical scratch", width)
	}
}

func TestStringSplitRowMarks(t *testing.T) {
	for _, tc := range []struct {
		q       string
		invalid bool
	}{
		{"SELECT * FROM string_to_table('a,b',',') AS t(v) FOR UPDATE", false},
		{"SELECT * FROM string_to_table('a,b',',') AS t(v) FOR UPDATE OF t", true},
		{"SELECT * FROM string_to_table('a,b',',') FOR SHARE OF string_to_table", true},
	} {
		raw := statement(t, tc.q).(*parser.Select)
		marks, err := queryLockTargets(raw.From, raw.Locking)
		if len(marks) != 0 || tc.invalid != (err != nil) || err != nil && !strings.Contains(err.Error(), "cannot be applied to a function") {
			t.Fatal(tc.q, marks, err)
		}
	}
	raw := statement(t, "SELECT string_to_table('a,b',',')").(*parser.Select)
	function := &functionFromNode{}
	for _, tc := range []struct {
		name   string
		node   LogicalNode
		marked bool
	}{
		{"relation", &sourceNode{}, true},
		{"derived_function", &derivedNode{query: &BoundQuery{Root: &projectNode{syntax: raw, child: function}}}, true},
		{"function", function, false},
		{"source_free", &singletonNode{}, false},
		{"values", &valuesNode{}, false},
		{"function_expansion", &setProjectNode{child: function}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			project := &projectNode{syntax: raw, child: tc.node, hasTargetSet: true}
			err := queryRowLockLegality(project, rowUpdate)
			if tc.marked != (err != nil) || err != nil && !strings.Contains(err.Error(), "set-returning functions in the target list") {
				t.Fatal(tc.name, err)
			}
		})
	}
}
