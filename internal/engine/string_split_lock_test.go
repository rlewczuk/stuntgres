package engine

import (
	"context"
	"reflect"
	"strings"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestStringSplitCopiedFunction(t *testing.T) {
	b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
	text, bigint := b.typ(sqltype.Text), b.typ(sqltype.Int8)
	var evaluations []int
	dependent := plan.ColumnExpr{Info: expr.Info{Usage: text.Usage(), Property: expr.Properties{RowDependent: true}}, Scope: 1, Frame: 1, Slot: 0}
	independent := mathObservedArgument{Info: expr.Info{Usage: text.Usage()}, value: sqltype.String(text, "x,y"), order: &evaluations, index: 0}
	call := stringSplitMarker(t, b, 1, dependent, stringSplitLiteral(b, ","), stringSplitLiteral(b, "replacement"))
	peer := stringSplitMarker(t, b, 2, independent, stringSplitLiteral(b, ","))
	node := &plan.FunctionFromNode{Calls: []expr.Expr{call, peer}, Ordinality: true, Frame: 1, QuerySchema: plan.QuerySchema{{Usage: text.Usage()}, {Usage: text.Usage()}, {Usage: bigint.Usage()}}}
	ordinal, err := sqltype.Integer(bigint, 2)
	if err != nil {
		t.Fatal(err)
	}
	frozen := []sqltype.Value{sqltype.String(text, "old"), sqltype.String(text, "original"), ordinal}
	scope := &StatementScope{queryLocks: &queryLockContext{}}
	inputs := &querySource{Host: hostOf(scope)}
	width := exec.QueryLockRecheckWidth(node, inputs)
	if width != 3 {
		t.Fatal("wrong refreshed function frame scratch", width)
	}
	if physical := exec.QueryTreeOccurrences(node, inputs, nil); len(physical) != 0 {
		t.Fatal("computed function acquired physical identity", physical)
	}
	// As in PostgreSQL 18.6, a recheck keeps the function-scan tuple the row was
	// produced with and evaluates no call again.
	evaluation := EvalContext{EvalStatement: evalStatement{Context: context.Background(), Host: hostOf(scope)}, EvalFrame: evalFrame{Scope: 1, Frame: 1}}
	scratch := make([]sqltype.Value, width)
	offset := 0
	got, present, err := exec.RecheckLockedQuery(node, inputs, nil, &evaluation, scratch, &offset, frozen)
	if err != nil || !present || !reflect.DeepEqual(got, frozen) || len(evaluations) != 0 {
		t.Fatal("recheck changed the function-scan tuple", got, present, err, evaluations)
	}
	// Quals are requalified against that tuple.
	filteredCall := stringSplitMarker(t, b, 3, dependent, stringSplitLiteral(b, ","))
	filteredNode := &plan.FunctionFromNode{Calls: []expr.Expr{filteredCall}, Ordinality: true, Frame: 1, QuerySchema: plan.QuerySchema{{Usage: text.Usage()}, {Usage: bigint.Usage()}}}
	field := plan.ColumnExpr{Info: expr.Info{Usage: text.Usage(), Property: expr.Properties{RowDependent: true}}, Scope: 1, Frame: 1, Slot: 0}
	raw := statement(t, "SELECT 'x'='b'").(*parser.Select).Targets[0].(parser.OperatorExpr)
	predicate, err := b.bindComparisonOperands(raw, sqltype.CompareEqual, comparisonOperand{node: field}, comparisonOperand{node: stringSplitLiteral(b, "b")})
	if err != nil {
		t.Fatal(err)
	}
	filter := &plan.FilterNode{QuerySchema: filteredNode.QuerySchema, Child: filteredNode, Predicate: predicate}
	for _, tc := range []struct {
		field string
		match bool
	}{{"b", true}, {"a", false}} {
		tuple := []sqltype.Value{sqltype.String(text, tc.field), ordinal}
		offset := 0
		scratch := make([]sqltype.Value, exec.QueryLockRecheckWidth(filter, inputs))
		got, present, err := exec.RecheckLockedQuery(filter, inputs, nil, &evaluation, scratch, &offset, tuple)
		if err != nil || present != tc.match || tc.match && !reflect.DeepEqual(got, tuple) {
			t.Fatal("requalification", tc.field, got, present, err)
		}
	}
	set := &plan.SetProjectNode{Child: &plan.SingletonNode{}}
	if physical := exec.QueryTreeOccurrences(set, inputs, nil); len(physical) != 0 {
		t.Fatal(physical)
	}
	if width := exec.QueryLockRecheckWidth(set, inputs); width != 0 {
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
	function := &plan.FunctionFromNode{}
	for _, tc := range []struct {
		name   string
		node   plan.Node
		marked bool
	}{
		{"relation", &plan.SourceNode{}, true},
		{"derived_function", &plan.DerivedNode{Query: &plan.Query{Root: &plan.ProjectNode{Syntax: raw, Child: function}}}, true},
		{"function", function, false},
		{"source_free", &plan.SingletonNode{}, false},
		{"values", &plan.ValuesNode{}, false},
		{"function_expansion", &plan.SetProjectNode{Child: function}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			project := &plan.ProjectNode{Syntax: raw, Child: tc.node, HasTargetSet: true}
			err := queryRowLockLegality(project, lock.ForUpdate)
			if tc.marked != (err != nil) || err != nil && !strings.Contains(err.Error(), "set-returning functions in the target list") {
				t.Fatal(tc.name, err)
			}
		})
	}
}
