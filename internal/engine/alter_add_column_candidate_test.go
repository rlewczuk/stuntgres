package engine

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func alterAddUnitScope(t *testing.T) (*Engine, *StatementScope, AuthorizationView, *relationDescriptor) {
	t.Helper()
	e := New()
	op, done := roleOperation(t, e, "postgres")
	t.Cleanup(done)
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = scope.Finish(errors.New("unit candidate discarded")) })
	auth, err := op.Authorization()
	if err != nil {
		t.Fatal(err)
	}
	u := testUsage(e, sqltype.Int4)
	old := &relationDescriptor{address: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20000}, namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, name: "t", revision: 1, owner: auth.state.CurrentUser, columns: []ColumnDescriptor{{Name: "a", Usage: u, Nullable: true, NullabilityKnown: true, OriginTable: 20000, OriginAttribute: 1}}, attributes: []int32{1}, attributeHighWater: 3, index: map[string]int{"a": 0}}
	return e, scope, auth, old
}

func alterAddUnitAction(t *testing.T, query string) parser.AlterTableAction {
	t.Helper()
	statements, err := parser.Parse(query)
	if err != nil {
		t.Fatal(err)
	}
	return statements[0].(*parser.AlterTable).Actions[0]
}

func TestAlterAddColumnOwnedCandidateAndHighWater(t *testing.T) {
	e, scope, auth, old := alterAddUnitScope(t)
	action := alterAddUnitAction(t, "ALTER TABLE t ADD b INT DEFAULT 7 NOT NULL CHECK(b>a)")
	next, added, err := e.addColumnCandidate(scope, scope.op, auth, old, action, nil)
	if err != nil {
		t.Fatal(err)
	}
	if next.attributeHighWater != 4 || !reflect.DeepEqual(next.attributes, []int32{1, 4}) || next.columns[1].OriginAttribute != 4 || next.columns[1].OriginTable != 20000 || added.attribute != 4 || len(old.columns) != 1 || old.attributeHighWater != 3 || len(old.defaults) != 0 {
		t.Fatal("stable identities/source changed", old, next, added)
	}
	if &next.columns[0] == &old.columns[0] || &next.attributes[0] == &old.attributes[0] {
		t.Fatal("candidate shares mutable shape")
	}
	next.index["sentinel"] = 2
	if _, exists := old.index["sentinel"]; exists {
		t.Fatal("candidate shares index")
	}
	delete(next.index, "sentinel")
	fills, volatile, err := planAddedColumns(scope, []addedColumn{added})
	if err != nil || volatile {
		t.Fatal(fills, volatile, err)
	}
	value, err := fills[4].Integer()
	if err != nil || value != 7 {
		t.Fatal(value, err)
	}
	// A syntactically valid skipped declaration never resolves its unknown type.
	skip := alterAddUnitAction(t, "ALTER TABLE t ADD IF NOT EXISTS a nonexistent_type DEFAULT 'oops'")
	same, ignored, err := e.addColumnCandidate(scope, scope.op, auth, old, skip, nil)
	if err != nil || same != old || ignored.column != nil || old.attributeHighWater != 3 {
		t.Fatal(same, ignored, err)
	}
	limit := *old
	limit.attributeHighWater = 1600
	_, _, err = e.addColumnCandidate(scope, scope.op, auth, &limit, action, nil)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "54011" || limit.attributeHighWater != 1600 {
		t.Fatal(err)
	}
}

func TestAlterAddColumnNonvolatileDemandOnce(t *testing.T) {
	_, scope, _, old := alterAddUnitScope(t)
	u := old.columns[0].Usage
	value, err := sqltype.Integer(u.Type, 7)
	if err != nil {
		t.Fatal(err)
	}
	for _, volatility := range []Volatility{Immutable, Stable} {
		calls := 0
		expression := queryProbeExpression{expressionInfo: expressionInfo{usage: u, properties: ExprProperties{Volatility: volatility}}, eval: func(*EvalContext) (sqltype.Value, error) { calls++; return value, nil }}
		additions := []addedColumn{{attribute: 4, expression: expression, value: sqltype.Null(u.Type)}}
		fills, volatile, err := planAddedColumns(scope, additions)
		if err != nil || volatile || calls != 1 || fills[4] != value || additions[0].value != value {
			t.Fatal(fills, volatile, calls, err)
		}
		// Empty backfill does not suppress the earlier one-time demand.
		if rows, err := projectVersions(context.Background(), nil, old, old, fills); err != nil || len(rows) != 0 || calls != 1 {
			t.Fatal(rows, calls, err)
		}
		if expression.properties.Volatility != volatility {
			t.Fatal("stored expression changed")
		}
	}
}

func TestAlterAddColumnVolatileEffectiveLiveRewrite(t *testing.T) {
	_, scope, _, old := alterAddUnitScope(t)
	u := old.columns[0].Usage
	one, err := sqltype.Integer(u.Type, 1)
	if err != nil {
		t.Fatal(err)
	}
	next := *old
	next.columns = append(append([]ColumnDescriptor(nil), old.columns...), ColumnDescriptor{Name: "b", Usage: u, Nullable: true})
	next.attributes = []int32{1, 4}
	next.index = map[string]int{"a": 0, "b": 1}
	next.attributeHighWater = 4
	calls := 0
	expression := queryProbeExpression{expressionInfo: expressionInfo{usage: u, properties: ExprProperties{Volatility: Volatile}}, eval: func(*EvalContext) (sqltype.Value, error) { calls++; return sqltype.Integer(u.Type, int64(calls)) }}
	additions := []addedColumn{{attribute: 4, column: &parser.Column{Name: "b"}, expression: expression, value: sqltype.Null(u.Type)}}
	fills, volatile, err := planAddedColumns(scope, additions)
	if err != nil || !volatile || calls != 0 || len(fills) != 0 {
		t.Fatal(fills, volatile, calls, err)
	}
	store := &relationStorage{generation: 1, versions: []rowVersion{
		{row: 1, version: 1, born: 1, values: []sqltype.Value{one}},
		{row: 2, version: 2, born: 1, values: []sqltype.Value{one}},
		{row: 3, version: 3, born: 1, values: []sqltype.Value{one}},
		{row: 4, version: 4, born: 1, values: []sqltype.Value{one}},
	}, ends: &deletionRoot{rows: map[ids.RowID]rowEnd{2: {version: 2, end: 2}}}}
	scope.ownWrites = map[ids.ObjectAddress][]rowVersion{old.address: {{row: 3, version: 5, base: 3, values: []sqltype.Value{one}}, {row: 5, version: 6, values: []sqltype.Value{one}}}}
	scope.ownDeletes = map[ids.ObjectAddress]*privateDeletionRoot{old.address: {rows: map[ids.RowID]deleteCandidate{4: {row: 4, version: 4}}}}
	transformCalls := 0
	transformed, err := sqltype.Integer(u.Type, 31)
	if err != nil {
		t.Fatal(err)
	}
	transform := queryProbeExpression{expressionInfo: expressionInfo{usage: u}, eval: func(ec *EvalContext) (sqltype.Value, error) {
		transformCalls++
		if len(ec.Row) != 1 || ec.Row[0] != one {
			t.Fatal("TYPE did not read original frame", ec.Row)
		}
		return transformed, nil
	}}
	transforms := []columnTransformation{{attribute: 1, expression: transform, rewrite: true}}
	replacement, own, deleted, err := rewriteTypeRows(scope, scope.op, old, &next, store, 2, transforms, additions, nil, newAlterRowValidator(&next, ""))
	if err != nil || calls != 3 || len(replacement.versions) != 1 || len(own) != 2 || deleted != nil {
		t.Fatal(replacement, own, calls, err)
	}
	if replacement.versions[0].row != 1 || own[0].row != 3 || own[0].base != 0 || own[1].row != 5 || len(store.versions) != 4 || len(store.versions[0].values) != 1 || replacement.generation != 2 {
		t.Fatal("identity/source rewrite invariant", replacement, own, store)
	}
	if transformCalls != 3 {
		t.Fatal("TYPE effective demand", transformCalls)
	}
	for i, row := range append(append([]rowVersion(nil), replacement.versions...), own...) {
		expectedDraw, drawErr := sqltype.Integer(u.Type, int64(i+1))
		if drawErr != nil {
			t.Fatal(drawErr)
		}
		if len(row.values) != 2 || row.values[0] != transformed || row.values[1] != expectedDraw {
			t.Fatal("complete transformed/add frame", row.values)
		}
	}
	for _, row := range store.versions {
		if len(row.values) != 1 || row.values[0] != one {
			t.Fatal("committed predecessor mutated", row)
		}
	}
	for _, row := range scope.ownWrites[old.address] {
		if len(row.values) != 1 || row.values[0] != one {
			t.Fatal("private predecessor mutated", row)
		}
	}
}
