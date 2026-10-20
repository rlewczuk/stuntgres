package engine

import (
	"context"
	"reflect"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

func TestQueryWindowOffsetNestedComparison(t *testing.T) {
	for _, fn := range []string{"lead", "lag"} {
		for _, tail := range []string{"", " FROM t member WHERE member.id=t.id"} {
			left := "(SELECT " + fn + "(0,1,t.id) OVER()" + tail + ")"
			different := "(SELECT " + fn + "(0,1,-t.id) OVER()" + tail + ")"
			_, query, source := queryCoreFixture(t, "SELECT "+left+","+different+","+left+" FROM t")
			project := query.Root.(*projectNode)
			a, b, equal := project.expressions[0], project.expressions[1], project.expressions[2]
			// Independently bound child frames may pair, but their window defaults
			// remain semantic inputs. The child window is the only differing node.
			if (*expressionBinder)(nil).sameSubqueryTarget(a, b) || (*expressionBinder)(nil).sameQueryTarget(a, b) {
				source.close()
				t.Fatal("nested window defaults treated as equivalent", fn, tail)
			}
			if !(*expressionBinder)(nil).sameSubqueryTarget(a, equal) || !(*expressionBinder)(nil).sameQueryTarget(a, equal) {
				source.close()
				t.Fatal("equal nested window queries lost local equivalence", fn, tail)
			}
			source.close()
		}
	}
}

func TestQueryWindowOffsetLowering(t *testing.T) {
	b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
	b.query.frame = 7
	marker := stringSplitMarker(t, b, 11, stringSplitLiteral(b, "a,b"), stringSplitLiteral(b, ","))
	peer := stringSplitMarker(t, b, 21, stringSplitLiteral(b, "x,y,z"), stringSplitLiteral(b, ","))
	markerArgs := append([]expr.Expr(nil), expr.Expr(marker).(expr.Composite).Children()...)
	peerArgs := append([]expr.Expr(nil), expr.Expr(peer).(expr.Composite).Children()...)
	argument := stringSplitLiteral(b, "value")
	offset, err := sqltype.Integer(b.typ(sqltype.Int4), 100)
	if err != nil {
		t.Fatal(err)
	}
	ordinal := literalExpression{Info: expr.Info{Usage: b.typ(sqltype.Int4).Usage()}, value: offset}
	tail := stringSplitLiteral(b, "tail")
	retainedCalls := []windowCall{
		{operation: windowLead, argument: argument, ordinal: ordinal, defaultValue: marker},
		{operation: windowLag, argument: argument, ordinal: ordinal, defaultValue: tail},
	}
	retainedSpecs := []boundWindowSpec{{partition: []expr.Expr{stringSplitLiteral(b, "partition")}, order: []sortKey{{expression: stringSplitLiteral(b, "order")}}}}
	retainedCompanion := []expr.Expr{marker, peer}
	calls := append([]windowCall(nil), retainedCalls...)
	specs := append([]boundWindowSpec(nil), retainedSpecs...)
	companion := append([]expr.Expr(nil), retainedCompanion...)
	// This mechanics test supplies a real pre-lowering marker, not public SQL
	// admitting an SRF inside a window call. The lowering contract must transform
	// every owned request and corresponding companion without changing its inputs.
	root, err := b.lowerStringWindowSpecs(&singletonNode{}, specs, calls, [][]expr.Expr{companion})
	if err != nil {
		t.Fatal(err)
	}
	selected, ok := calls[0].defaultValue.(columnExpression)
	if !ok {
		t.Fatalf("third child not written back after lowering: %T", calls[0].defaultValue)
	}
	shared, ok := companion[0].(columnExpression)
	if !ok || selected.slot != shared.slot || selected.frame != 7 || shared.frame != 7 {
		t.Fatal("default/companion lost owned stage datum", selected, companion[0])
	}
	independent, ok := companion[1].(columnExpression)
	if !ok || independent.slot == selected.slot || independent.frame != 7 {
		t.Fatal("independent companion occurrence collapsed", companion[1])
	}
	if !reflect.DeepEqual(calls[0].argument, argument) || !reflect.DeepEqual(calls[0].ordinal, ordinal) || !reflect.DeepEqual(calls[1], retainedCalls[1]) {
		t.Fatal("ordered default writeback displaced another child", calls)
	}
	if !reflect.DeepEqual(expr.Expr(marker).(expr.Composite).Children(), markerArgs) || !reflect.DeepEqual(expr.Expr(peer).(expr.Composite).Children(), peerArgs) || !reflect.DeepEqual(retainedCalls[0].defaultValue, marker) || !reflect.DeepEqual(retainedCompanion, []expr.Expr{marker, peer}) || !reflect.DeepEqual(retainedSpecs[0].partition, []expr.Expr{stringSplitLiteral(b, "partition")}) || !reflect.DeepEqual(retainedSpecs[0].order, []sortKey{{expression: stringSplitLiteral(b, "order")}}) {
		t.Fatal("lowering mutated retained input ownership")
	}
	node, ok := root.(*setProjectNode)
	if !ok {
		t.Fatalf("no executable marker stage: %T", root)
	}
	stage, err := openSetProjectNode(context.Background(), &queryState{source: &querySource{}, frame: 7}, node)
	if err != nil {
		t.Fatal(err)
	}
	defer stage.Close()
	evaluation := EvalContext{evalStatement: evalStatement{Context: context.Background()}, evalFrame: evalFrame{Scope: 1, Frame: 7}}
	var rows [][]any
	for {
		occurrence, present, err := stage.nextOccurrence(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if !present {
			break
		}
		evaluation.Row = stage.source.row(stage.index(occurrence))
		row := make([]any, 4)
		for i, expr := range []expr.Expr{calls[0].defaultValue, companion[0], companion[1], calls[1].defaultValue} {
			value, err := expr.Eval(&evaluation)
			if err != nil {
				t.Fatal(err)
			}
			if !value.IsNull() {
				row[i], err = value.Text()
				if err != nil {
					t.Fatal(err)
				}
			}
		}
		rows = append(rows, row)
	}
	if !reflect.DeepEqual(rows, [][]any{{"a", "a", "x", "tail"}, {"b", "b", "y", "tail"}, {nil, nil, "z", "tail"}}) {
		t.Fatal("lowered defaults/companions lost values or multiplicity", rows)
	}
	// The reusable analyzed markers still own their original arguments after the
	// executable stage has consumed them, rather than retaining execution buffers.
	if !reflect.DeepEqual(expr.Expr(marker).(expr.Composite).Children(), markerArgs) || !reflect.DeepEqual(expr.Expr(peer).(expr.Composite).Children(), peerArgs) {
		t.Fatal("execution mutated retained marker arguments")
	}
}
