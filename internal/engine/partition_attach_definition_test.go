package engine

import (
	"reflect"
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestPartitionAttachLayout(t *testing.T) {
	e := New()
	parent := inheritanceUnitRelation(e, "parent", 32000, "k", "payload")
	parent.attributes = []int32{3, 8}
	parent.attributeHighWater = 8
	child := inheritanceUnitRelation(e, "child", 32001, "payload", "k")
	child.attributes = []int32{2, 9}
	child.attributeHighWater = 9
	layout, err := inheritanceQueryLayout(parent, child)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(layout, []int{1, 0}) {
		t.Fatal(layout)
	}
	one, _ := sqltype.Integer(parent.columns[0].Usage.Type, 1)
	two, _ := sqltype.Integer(parent.columns[0].Usage.Type, 2)
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
	state := partitionInsertState{target: parent, destination: child, layouts: map[ids.ObjectAddress]*inheritanceQuerySegment{child.address: &back}}
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
	parent.columns[0].Nullable = false
	parent.notNullConstraints = []notNullConstraint{{OID: 32100, Name: "parent_nn", Attribute: 1}}
	child.columns[1].Nullable = false
	child.notNullConstraints = []notNullConstraint{{OID: 32101, Name: "child_nn", Attribute: 2, partitionLocal: true}}
	view := inheritanceUnitView(e, parent, child)
	scope := &StatementScope{op: op}
	next, err := partitionAttachSchema(scope, view, parent, child)
	if err != nil {
		t.Fatal(err)
	}
	if next == child || next.notNullConstraints[0].Name != "child_nn" || next.notNullConstraints[0].OID != 32101 || next.notNullConstraints[0].partitionLocal || !child.notNullConstraints[0].partitionLocal {
		t.Fatal("NN identity or source changed")
	}
	next.membership = &partitionMembership{parent: parent.address}
	view = inheritanceUnitView(e, parent, next)
	local, count := nonuniqueConstraintProvenance(view, next, "child_nn", 2, false)
	if local || count != 1 {
		t.Fatal(local, count)
	}
	child.columns[1].Nullable = true
	if _, err := partitionAttachSchema(scope, view, parent, child); err == nil {
		t.Fatal("missing required NN accepted")
	}
	child.columns[1].Nullable = false
	child.columns[1].Usage = testUsage(e, sqltype.Int8)
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
	usage := parent.columns[0].Usage
	class, supported := sqltype.DefaultIndexClass(usage.Type)
	if !supported {
		t.Fatal("missing default index class")
	}
	ordering, err := sqltype.BindIndexOrdering(usage, class)
	if err != nil {
		t.Fatal(err)
	}
	parent.partition = &partitionDefinition{keys: []partitionKeyEntry{{attribute: 1, usage: usage, class: class, comparison: ordering, label: "k"}}}
	view := inheritanceUnitView(e, parent, child)
	ref := e.catalog.domain.Ref(view.Epoch(), child.address)
	ref.Address.SubID = 1
	key := columnExpression{Info: expressionInfo{Usage: usage, Property: ExprProperties{RowDependent: true}}, origin: ref, scope: 1, frame: 1, slot: 0}
	boolean := testUsage(e, sqltype.Bool)
	value := func(n int64) sqltype.Value {
		v, err := sqltype.Integer(usage.Type, n)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	cmp := func(operator string, n int64) BoundExpr {
		operators := map[string]sqltype.ComparisonOperator{"<": sqltype.CompareLess, ">=": sqltype.CompareGreaterEqual}
		predicate, err := sqltype.BindComparisonPredicate(operators[operator], usage.Type, usage.Type, boolean.Type, usage.Collation, false)
		if err != nil {
			t.Fatal(err)
		}
		return comparisonExpression{Info: expressionInfo{Usage: boolean, Property: ExprProperties{RowDependent: true}}, left: key, right: literalExpression{Info: expressionInfo{Usage: usage}, value: value(n)}, predicate: predicate, operator: operator}
	}
	and := func(a, b BoundExpr) BoundExpr {
		return logicalExpression{Info: expressionInfo{Usage: boolean}, left: a, right: b, op: parser.LogicalAnd}
	}
	or := func(a, b BoundExpr) BoundExpr {
		return logicalExpression{Info: expressionInfo{Usage: boolean}, left: a, right: b, op: parser.LogicalOr}
	}
	bounds := and(cmp(">=", 1), cmp("<", 3))
	bound := &partitionMembership{parent: parent.address, lower: []partitionDatum{{value: value(1)}}, upper: []partitionDatum{{value: value(3)}}}
	scope := &StatementScope{op: op}
	check := func(exclude, want bool) {
		t.Helper()
		got, err := partitionConstraintProven(scope, view, parent, child, bound, exclude)
		if err != nil || got != want {
			t.Fatal(got, want, err)
		}
	}
	child.checkConstraints = []checkConstraint{{Name: "bounds", expression: bounds}}
	check(false, false) // nullable CHECK permits UNKNOWN
	child.notNullConstraints = []notNullConstraint{{Attribute: 1}}
	check(false, true)
	child.notNullConstraints[0].NotValid = true
	check(false, false)
	child.notNullConstraints = nil
	child.checkConstraints[0].expression = and(nullTestExpression{Info: expressionInfo{Usage: boolean}, child: key, not: true}, bounds)
	check(false, true)
	child.checkConstraints[0].NotValid = true
	check(false, false)
	child.checkConstraints[0].NotValid = false
	child.checkConstraints[0].NoInherit = true
	check(false, true) // local validated NO INHERIT remains a scan proof
	child.checkConstraints[0].expression = or(cmp("<", 1), cmp(">=", 3))
	check(true, true) // NULL belongs to the range complement
	child.checkConstraints[0].NotValid = true
	check(true, false)
	goal := partitionProofTuple([]partitionDatum{{value: value(1)}, {kind: -1}}, 0, true)
	if goal.op != "or" || goal.right.op != "=" {
		t.Fatal("MINVALUE suffix did not simplify lexicographic goal", goal)
	}
}
