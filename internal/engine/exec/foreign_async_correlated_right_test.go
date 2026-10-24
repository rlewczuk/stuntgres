package exec

import (
	"context"
	"database/sql/driver"
	"fmt"
	"reflect"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// The existing executor supports CorrelatedRight plans independently of SQL's
// FROM syntax. Exercise that actual reopen path without adding parser scope.
type invocationUnitThreshold struct{ plan.LiteralExpr }

func (p invocationUnitThreshold) Eval(c expr.Context) (sqltype.Value, error) {
	frame := c.(*Context)
	id, err := frame.Row[1].Integer()
	if err != nil {
		return sqltype.Value{}, err
	}
	threshold, err := frame.Parameters[0].Integer()
	if err != nil {
		return sqltype.Value{}, err
	}
	return sqltype.Boolean(p.Value.Type(), id >= threshold), nil
}

func TestFDWAsyncInvocationCorrelatedRight(t *testing.T) {
	for _, duplicate := range []bool{false, true} {
		t.Run(fmt.Sprintf("duplicates_%v", duplicate), func(t *testing.T) {
			f := newInvocationUnitFixture(t, true)
			// Prefix, seq and id are distinct values; current RHS parameters must retain
			// all three, not mistake a lexical outer frame for the current left row.
			for _, p := range f.providers {
				p.correlationWidth = 3
				p.correlationSlot = 2
			}
			leftColumns := []catalog.Column{{Name: "seq", Usage: f.integer.Usage()}, {Name: "outer_id", Usage: f.integer.Usage()}}
			var leftRows [][2]int64
			if duplicate {
				leftRows = [][2]int64{{1, 1}, {2, 1}}
			} else {
				for i := range 24 {
					leftRows = append(leftRows, [2]int64{int64(i + 1), []int64{3, 4, 9}[i%3]})
				}
			}
			values := make([][]expr.Expr, len(leftRows))
			for i, row := range leftRows {
				for _, n := range row {
					value, err := sqltype.Integer(f.integer, n)
					if err != nil {
						t.Fatal(err)
					}
					values[i] = append(values[i], plan.LiteralExpr{Info: expr.Info{Usage: f.integer.Usage()}, Value: value})
				}
			}
			boolean := asyncUnitType(t, sqltype.Bool)
			predicate := invocationUnitThreshold{LiteralExpr: plan.LiteralExpr{Info: expr.Info{Usage: boolean.Usage(), Property: expr.Properties{RowDependent: true, ParameterDependent: true}}, Value: sqltype.Boolean(boolean, true)}}
			left := &plan.FilterNode{QuerySchema: leftColumns, Child: &plan.ValuesNode{QuerySchema: leftColumns, Rows: values}, Predicate: predicate}
			rightQuery := *f.root
			rightQuery.RuleInvocation = true
			right := &plan.DerivedNode{QuerySchema: f.columns, Query: &plan.Query{Root: &rightQuery}, ScanFrame: 3}
			schema := append(append([]catalog.Column{}, leftColumns...), f.columns...)
			node := &plan.JoinNode{QuerySchema: schema, Kind: parser.JoinInner, Left: left, Right: right, LeftWidth: 2, RightWidth: 1, CorrelatedRight: true, CorrelationPrefix: 1, Frame: 1}
			output := []catalog.Column{leftColumns[0], f.columns[0]}
			outputID := f.column(1).(plan.ColumnExpr)
			outputID.Slot = 2
			root := &plan.ProjectNode{QuerySchema: output, Frame: 1, Child: node, Expressions: []expr.Expr{f.column(1), outputID}}
			bindings := []int64{3, 3, 4, 3}
			if duplicate {
				bindings = []int64{1, 1, 1}
			}
			for _, threshold := range bindings {
				bound, err := sqltype.Integer(f.integer, threshold)
				if err != nil {
					t.Fatal(err)
				}
				prefixValue, err := sqltype.Integer(f.integer, 100+threshold)
				if err != nil {
					t.Fatal(err)
				}
				f.state.Parameters = []sqltype.Value{bound}
				prefix := f.state.Evaluation(context.Background())
				prefix.Frame = 1
				prefix.Row = []sqltype.Value{prefixValue}
				f.state.Invocation = &prefix
				for _, p := range f.providers {
					p.history = nil
				}
				// The identical immutable JoinNode and captured sources are reused across
				// bindings, as with a held plan; only the current operation values change.
				joined, err := OpenQueryProjectStage(context.Background(), &f.state, root)
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(joined.Columns, output) {
					t.Fatal("projected result metadata changed", joined.Columns)
				}
				got := map[[2]int64]int{}
				row := make([]sqltype.Value, 2)
				for {
					present, err := joined.Next(context.Background(), row)
					if err != nil {
						_ = joined.Close()
						t.Fatal(err)
					}
					if !present {
						break
					}
					seq, err := row[0].Integer()
					if err != nil {
						t.Fatal(err)
					}
					id, err := row[1].Integer()
					if err != nil {
						t.Fatal(err)
					}
					got[[2]int64{seq, id}]++
					f.checkReusable(t)
				}
				if err := joined.Close(); err != nil {
					t.Fatal(err)
				}
				want := map[[2]int64]int{}
				var expectedParameters []catalog.ForeignScanParameters
				for _, row := range leftRows {
					seq, id := row[0], row[1]
					if id < threshold {
						continue
					}
					expectedParameters = append(expectedParameters, catalog.ForeignScanParameters{Values: []driver.Value{threshold}, Correlation: []driver.Value{100 + threshold, seq, id}})
					if duplicate {
						want[[2]int64{seq, id}] = 2
					} else if id != 9 {
						want[[2]int64{seq, id}] = 1
					}
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("binding %d joined bag got %v want %v", threshold, got, want)
				}
				f.checkReusable(t)
				invocationUnitNoOptionalResources(t, f)
				for _, p := range f.providers {
					if !reflect.DeepEqual(p.history, expectedParameters) {
						t.Fatalf("binding %d stale exported RHS values/current prefix: got %+v want %+v", threshold, p.history, expectedParameters)
					}
					if p.ends != 0 {
						t.Fatal("RHS reader Ended a shared captured scan")
					}
				}
			}
			if err := f.owner.Finish(context.Background()); err != nil {
				t.Fatal(err)
			}
			for _, p := range f.providers {
				if p.liveScans != 0 || p.liveRequests != 0 || p.ends != 1 {
					t.Fatal("sole owner did not retire the fixed RHS frontier", p.liveScans, p.liveRequests, p.ends)
				}
			}
		})
	}
}
