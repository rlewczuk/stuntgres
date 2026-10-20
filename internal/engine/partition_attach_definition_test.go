package engine

import (
	"reflect"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestPartitionAttachLayout(t *testing.T) {
	e := New()
	parent := inheritanceUnitRelation(e, "parent", 32000, "k", "payload")
	parent.Attributes = []int32{3, 8}
	parent.ColumnMeta.HighWater = 8
	child := inheritanceUnitRelation(e, "child", 32001, "payload", "k")
	child.Attributes = []int32{2, 9}
	child.ColumnMeta.HighWater = 9
	layout, err := inheritanceQueryLayout(parent, child)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(layout, []int{1, 0}) {
		t.Fatal(layout)
	}
	one, _ := sqltype.Integer(parent.Columns[0].Usage.Type, 1)
	two, _ := sqltype.Integer(parent.Columns[0].Usage.Type, 2)
	physical := []sqltype.Value{two, one}
	segment := inheritanceQuerySegment{layout: layout, scratch: make([]sqltype.Value, 2)}
	logical := segment.mapValues(physical)
	if !sqltype.SameDatum(logical[0], one) || !sqltype.SameDatum(logical[1], two) || !sqltype.SameDatum(physical[0], two) {
		t.Fatal("physical tuple rewritten")
	}
	inverse, err := inheritanceQueryLayout(child, parent)
	if err != nil {
		t.Fatal(err)
	}
	back := inheritanceQuerySegment{layout: inverse, scratch: make([]sqltype.Value, 2)}
	if !reflect.DeepEqual(back.mapValues(logical), physical) {
		t.Fatal("inverse mapping")
	}
	identity, err := inheritanceQueryLayout(parent, parent)
	if err != nil || identity != nil {
		t.Fatal(identity, err)
	}
	borrow := inheritanceQuerySegment{}
	if got := borrow.mapValues(physical); &got[0] != &physical[0] {
		t.Fatal("identity copied")
	}
	state := partitionInsertState{target: parent, destination: child, layouts: map[ids.ObjectAddress]*inheritanceQuerySegment{child.Address(): &back}}
	state.physicalRow = state.rowFor(child, logical)
	owned := append([]sqltype.Value(nil), logical...)
	scope := StatementScope{statementMutation: statementMutation{partitionInsert: &state}}
	stored := scope.partitionStoredRow(owned)
	if &stored[0] != &owned[0] || !reflect.DeepEqual(stored, physical) {
		t.Fatal("non-returning storage ownership")
	}
}

func TestPartitionAttachSchema(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	parent := inheritanceUnitRelation(e, "parent", 32010, "k", "payload")
	child := inheritanceUnitRelation(e, "child", 32011, "payload", "k")
	parent.Columns[0].Nullable = false
	parent.Constraints.NotNull = []catalog.NotNullConstraint{{OID: 32100, Name: "parent_nn", Attribute: 1}}
	child.Columns[1].Nullable = false
	child.Constraints.NotNull = []catalog.NotNullConstraint{{OID: 32101, Name: "child_nn", Attribute: 2, PartitionLocal: true}}
	view := inheritanceUnitView(e, parent, child)
	scope := &StatementScope{op: op}
	next, err := partitionAttachSchema(scope, view, parent, child)
	if err != nil {
		t.Fatal(err)
	}
	if next == child || next.Constraints.NotNull[0].Name != "child_nn" || next.Constraints.NotNull[0].OID != 32101 || next.Constraints.NotNull[0].PartitionLocal || !child.Constraints.NotNull[0].PartitionLocal {
		t.Fatal("NN identity or source changed")
	}
	next.Membership = &catalog.PartitionMembership{Parent: parent.Address()}
	view = inheritanceUnitView(e, parent, next)
	local, count := NonuniqueConstraintProvenance(view, next, "child_nn", 2, false)
	if local || count != 1 {
		t.Fatal(local, count)
	}
	child.Columns[1].Nullable = true
	if _, err := partitionAttachSchema(scope, view, parent, child); err == nil {
		t.Fatal("missing required NN accepted")
	}
	child.Columns[1].Nullable = false
	child.Columns[1].Usage = testUsage(e, sqltype.Int8)
	if _, err := partitionAttachSchema(scope, view, parent, child); err == nil {
		t.Fatal("different storage type accepted")
	}
}

func TestPartitionAttachProof(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	parent := inheritanceUnitRelation(e, "parent", 32020, "k")
	child := inheritanceUnitRelation(e, "child", 32021, "k")
	usage := parent.Columns[0].Usage
	class, supported := sqltype.DefaultIndexClass(usage.Type)
	if !supported {
		t.Fatal("missing default index class")
	}
	ordering, err := sqltype.BindIndexOrdering(usage, class)
	if err != nil {
		t.Fatal(err)
	}
	parent.Partition = &catalog.PartitionDefinition{Keys: []catalog.PartitionKeyEntry{{Attribute: 1, Usage: usage, Class: class, Comparison: ordering, Label: "k"}}}
	view := inheritanceUnitView(e, parent, child)
	ref := e.catalog.Domain.Ref(view.Epoch(), child.Address())
	ref.Address.SubID = 1
	key := columnExpression{Info: expr.Info{Usage: usage, Property: expr.Properties{RowDependent: true}}, origin: ref, scope: 1, frame: 1, slot: 0}
	boolean := testUsage(e, sqltype.Bool)
	value := func(n int64) sqltype.Value {
		v, err := sqltype.Integer(usage.Type, n)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	cmp := func(operator string, n int64) expr.Expr {
		operators := map[string]sqltype.ComparisonOperator{"<": sqltype.CompareLess, ">=": sqltype.CompareGreaterEqual}
		predicate, err := sqltype.BindComparisonPredicate(operators[operator], usage.Type, usage.Type, boolean.Type, usage.Collation, false)
		if err != nil {
			t.Fatal(err)
		}
		return comparisonExpression{Info: expr.Info{Usage: boolean, Property: expr.Properties{RowDependent: true}}, left: key, right: literalExpression{Info: expr.Info{Usage: usage}, value: value(n)}, predicate: predicate, operator: operator}
	}
	and := func(a, b expr.Expr) expr.Expr {
		return logicalExpression{Info: expr.Info{Usage: boolean}, left: a, right: b, op: parser.LogicalAnd}
	}
	or := func(a, b expr.Expr) expr.Expr {
		return logicalExpression{Info: expr.Info{Usage: boolean}, left: a, right: b, op: parser.LogicalOr}
	}
	bounds := and(cmp(">=", 1), cmp("<", 3))
	bound := &catalog.PartitionMembership{Parent: parent.Address(), Lower: []catalog.PartitionDatum{{Value: value(1)}}, Upper: []catalog.PartitionDatum{{Value: value(3)}}}
	scope := &StatementScope{op: op}
	check := func(exclude, want bool) {
		t.Helper()
		got, err := partitionConstraintProven(scope, view, parent, child, bound, exclude)
		if err != nil || got != want {
			t.Fatal(got, want, err)
		}
	}
	child.Constraints.Check = []catalog.CheckConstraint{{Name: "bounds", Expression: bounds}}
	check(false, false) // nullable CHECK permits UNKNOWN
	child.Constraints.NotNull = []catalog.NotNullConstraint{{Attribute: 1}}
	check(false, true)
	child.Constraints.NotNull[0].NotValid = true
	check(false, false)
	child.Constraints.NotNull = nil
	child.Constraints.Check[0].Expression = and(nullTestExpression{Info: expr.Info{Usage: boolean}, child: key, not: true}, bounds)
	check(false, true)
	child.Constraints.Check[0].NotValid = true
	check(false, false)
	child.Constraints.Check[0].NotValid = false
	child.Constraints.Check[0].NoInherit = true
	check(false, true) // local validated NO INHERIT remains a scan proof
	child.Constraints.Check[0].Expression = or(cmp("<", 1), cmp(">=", 3))
	check(true, true) // NULL belongs to the range complement
	child.Constraints.Check[0].NotValid = true
	check(true, false)
	goal := partitionProofTuple([]catalog.PartitionDatum{{Value: value(1)}, {Kind: -1}}, 0, true)
	if goal.op != "or" || goal.right.op != "=" {
		t.Fatal("MINVALUE suffix did not simplify lexicographic goal", goal)
	}
}
