package enginetests_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func TestQueryRecursiveCTELimitZeroAndEmptyAnchor(t *testing.T) {
	owner, state, node := recursiveUnitFixture(t, true, []int64{1}, 3)
	defer owner.Close()
	int8, _ := engine.New().CatalogRead().LookupType(sqltype.Int8)
	zero, err := sqltype.Integer(int8, 0)
	if err != nil {
		t.Fatal(err)
	}
	limit := &plan.LimitNode{QuerySchema: node.Schema(), Child: node, Count: plan.LiteralExpr{Info: expr.Info{Usage: int8.Usage()}, Value: zero}}
	stage, err := exec.OpenLimitNode(context.Background(), state, limit)
	if err != nil {
		t.Fatal(err)
	}
	if stage.Count() != 0 || owner.Producers != nil || len(stage.Columns) != 1 {
		t.Fatal("LIMIT0 opened recursion or lost schema")
	}
	if err := stage.Close(); err != nil {
		t.Fatal(err)
	}
	emptyOwner, emptyState, emptyNode := recursiveUnitFixture(t, true, nil, 3)
	defer emptyOwner.Close()
	empty, err := exec.OpenCTEScan(context.Background(), emptyState, emptyNode)
	if err != nil {
		t.Fatal(err)
	}
	if _, present, err := empty.NextOccurrence(context.Background()); present || err != nil {
		t.Fatal(present, err)
	}
	runtime := emptyOwner.Producers[emptyNode.CTE].Recursion
	if runtime.Phase != 2 || runtime.Source.FlatCount != 0 || runtime.Opened {
		t.Fatal("empty anchor opened a recursive generation")
	}
}

func TestQueryRecursiveCTEPrimaryErrorAndRequiredClose(t *testing.T) {
	for _, primaryFailure := range []bool{false, true} {
		owner, state, node := recursiveUnitFixture(t, true, []int64{1}, 3)
		stage, err := exec.OpenCTEScan(context.Background(), state, node)
		if err != nil {
			t.Fatal(err)
		}
		cteUnitNext(t, &stage)
		producer := owner.Producers[node.CTE]
		cleanup := errors.New("required recursive child close failed")
		primary := errors.New("recursive term failed")
		closes := 0
		if primaryFailure {
			n := node.Query.Root.(*plan.ProjectNode).Child.(*plan.RecursiveUnionNode)
			n.RightExpressions = []expr.Expr{queryProbe(n.Schema()[0].Usage, func(*exec.Context) (sqltype.Value, error) {
				producer.Recursion.Arm.Inner.Finish = func() error { closes++; return cleanup }
				return sqltype.Value{}, primary
			})}
		} else {
			producer.Recursion.Arm.Inner.Finish = func() error { closes++; return cleanup }
			primary = cleanup
		}
		if _, _, err := stage.NextOccurrence(context.Background()); err != primary {
			t.Fatal("primary/close precedence", err, primary)
		}
		if err := owner.Close(); err != primary || closes != 1 {
			t.Fatal(err, closes)
		}
		if _, err := producer.Ensure(context.Background(), 0); err != primary {
			t.Fatal("failure not sticky on replay", err)
		}
		if err := owner.Close(); err != primary || closes != 1 {
			t.Fatal("closed recursive child twice", err, closes)
		}
	}
}

func TestQueryRecursiveCTELongIterationAndFreshInvocation(t *testing.T) {
	initial, base, node := recursiveUnitFixture(t, true, []int64{1}, 2000)
	defer initial.Close()
	for range 2 {
		stateCopy := *base
		owner := &exec.CTEInvocation{Frame: 1, State: stateCopy}
		stateCopy.Ctes = owner
		state := &stateCopy
		stage, err := exec.OpenCTEScan(context.Background(), state, node)
		if err != nil {
			t.Fatal(err)
		}
		for want := int64(1); want <= 2000; want++ {
			row := cteUnitNext(t, &stage)
			got, err := row[0].Integer()
			if err != nil || got != want {
				t.Fatal(got, want, err)
			}
		}
		if _, present, err := stage.NextOccurrence(context.Background()); present || err != nil {
			t.Fatal(present, err)
		}
		if err := owner.Close(); err != nil {
			t.Fatal(err)
		}
		if owner.Producers[node.CTE].Recursion.Seen != nil {
			t.Fatal("ALL retained seen state")
		}
	}
}

func recursiveUnitFixture(t *testing.T, all bool, seeds []int64, terminal int64) (*exec.CTEInvocation, *exec.State, *plan.DerivedNode) {
	t.Helper()
	typ := cteUnitType(t)
	boolean, _ := engine.New().CatalogRead().LookupType(sqltype.Bool)
	schema := plan.QuerySchema{{Name: "n", Usage: typ.Usage()}}
	d := &plan.CTEDefinition{Owner: 1, Recursive: true, Name: "r", Schema: schema}
	column := func(frame uint32) plan.ColumnExpr {
		return plan.ColumnExpr{Info: expr.Info{Usage: typ.Usage(), Property: expr.Properties{RowDependent: true}}, Scope: 1, Frame: frame, Slot: 0}
	}
	rows := make([][]expr.Expr, len(seeds))
	for i, n := range seeds {
		rows[i] = []expr.Expr{plan.LiteralExpr{Info: expr.Info{Usage: typ.Usage()}, Value: cteUnitValue(t, typ, n)}}
	}
	left := &plan.ProjectNode{QuerySchema: schema, Child: &plan.ValuesNode{QuerySchema: schema, Rows: rows}, Expressions: []expr.Expr{column(2)}, Frame: 2}
	self := &plan.WorktableScanNode{DerivedNode: plan.DerivedNode{QuerySchema: schema, CTE: d, ScanFrame: 3}}
	read := column(3)
	predicate := queryProbe(boolean.Usage(), func(c *exec.Context) (sqltype.Value, error) {
		value, err := read.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		n, err := value.Integer()
		return sqltype.Boolean(boolean, n < terminal), err
	})
	successor := queryProbe(typ.Usage(), func(c *exec.Context) (sqltype.Value, error) {
		value, err := read.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		n, err := value.Integer()
		if err != nil {
			return sqltype.Value{}, err
		}
		return sqltype.Integer(typ, n+1)
	})
	right := &plan.ProjectNode{QuerySchema: schema, Child: &plan.FilterNode{QuerySchema: schema, Child: self, Predicate: predicate}, Expressions: []expr.Expr{successor}, Frame: 3}
	n := &plan.RecursiveUnionNode{SetNode: plan.SetNode{QuerySchema: schema, Left: left, Right: right, Operation: "union", All: all, LeftExpressions: left.Expressions, RightExpressions: right.Expressions}, Definition: d}
	if !all {
		grouping, err := sqltype.BindGrouping(typ.Usage())
		if err != nil {
			t.Fatal(err)
		}
		n.Grouping = []plan.Grouping{{Grouping: grouping}}
	}
	project := &plan.ProjectNode{QuerySchema: schema, Child: n, Expressions: []expr.Expr{column(4)}, Frame: 4}
	d.Query = &plan.Query{Root: project}
	state := &exec.State{Source: &exec.Source{Materialized: true}}
	owner := &exec.CTEInvocation{Frame: 1, State: *state}
	state.Ctes = owner
	return owner, state, &plan.DerivedNode{QuerySchema: schema, Query: d.Query, CTE: d}
}

func cteUnitNext(t *testing.T, stage *exec.Stage) []sqltype.Value {
	t.Helper()
	index, present, err := stage.NextOccurrence(context.Background())
	if err != nil || !present {
		t.Fatal(index, present, err)
	}
	return stage.Source.Row(stage.Index(index))
}

func cteUnitType(t *testing.T) *sqltype.Descriptor {
	t.Helper()
	typ, ok := engine.New().CatalogRead().LookupType(sqltype.Int4)
	if !ok {
		t.Fatal("INT4 descriptor not found")
	}
	return typ
}

func cteUnitValue(t *testing.T, typ *sqltype.Descriptor, n int64) sqltype.Value {
	t.Helper()
	value, err := sqltype.Integer(typ, n)
	if err != nil {
		t.Fatal(err)
	}
	return value
}
