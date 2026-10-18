package engine

import (
	"context"
	"reflect"
	"strings"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestStringSplitCopiedFunction(t *testing.T) {
	b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
	text, bigint := b.typ(sqltype.Text), b.typ(sqltype.Int8)
	var evaluations []int
	dependent := columnExpression{Info: expr.Info{Usage: text.Usage(), Property: expr.Properties{RowDependent: true}}, scope: 1, frame: 1, slot: 0}
	independent := mathObservedArgument{Info: expr.Info{Usage: text.Usage()}, value: sqltype.String(text, "x,y"), order: &evaluations, index: 0}
	call := stringSplitMarker(t, b, 1, dependent, stringSplitLiteral(b, ","), stringSplitLiteral(b, "replacement"))
	peer := stringSplitMarker(t, b, 2, independent, stringSplitLiteral(b, ","))
	node := &functionFromNode{calls: []expr.Expr{call, peer}, ordinality: true, frame: 1, querySchema: querySchema{{Usage: text.Usage()}, {Usage: text.Usage()}, {Usage: bigint.Usage()}}}
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
	// As in PostgreSQL 18.6, a recheck keeps the function-scan tuple the row was
	// produced with and evaluates no call again.
	evaluation := EvalContext{Context: context.Background(), Scope: 1, Frame: 1, statementScope: scope}
	scratch := make([]sqltype.Value, width)
	offset := 0
	got, present, err := recheckLockedQuery(node, inputs, nil, &evaluation, scratch, &offset, frozen)
	if err != nil || !present || !reflect.DeepEqual(got, frozen) || len(evaluations) != 0 {
		t.Fatal("recheck changed the function-scan tuple", got, present, err, evaluations)
	}
	// Quals are requalified against that tuple.
	filteredCall := stringSplitMarker(t, b, 3, dependent, stringSplitLiteral(b, ","))
	filteredNode := &functionFromNode{calls: []expr.Expr{filteredCall}, ordinality: true, frame: 1, querySchema: querySchema{{Usage: text.Usage()}, {Usage: bigint.Usage()}}}
	field := columnExpression{Info: expr.Info{Usage: text.Usage(), Property: expr.Properties{RowDependent: true}}, scope: 1, frame: 1, slot: 0}
	raw := statement(t, "SELECT 'x'='b'").(*parser.Select).Targets[0].(parser.OperatorExpr)
	predicate, err := b.bindComparisonOperands(raw, sqltype.CompareEqual, comparisonOperand{node: field}, comparisonOperand{node: stringSplitLiteral(b, "b")})
	if err != nil {
		t.Fatal(err)
	}
	filter := &filterNode{querySchema: filteredNode.querySchema, child: filteredNode, predicate: predicate}
	for _, tc := range []struct {
		field string
		match bool
	}{{"b", true}, {"a", false}} {
		tuple := []sqltype.Value{sqltype.String(text, tc.field), ordinal}
		offset := 0
		scratch := make([]sqltype.Value, queryLockRecheckWidth(filter, inputs))
		got, present, err := recheckLockedQuery(filter, inputs, nil, &evaluation, scratch, &offset, tuple)
		if err != nil || present != tc.match || tc.match && !reflect.DeepEqual(got, tuple) {
			t.Fatal("requalification", tc.field, got, present, err)
		}
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
			err := queryRowLockLegality(project, lock.ForUpdate)
			if tc.marked != (err != nil) || err != nil && !strings.Contains(err.Error(), "set-returning functions in the target list") {
				t.Fatal(tc.name, err)
			}
		})
	}
}
