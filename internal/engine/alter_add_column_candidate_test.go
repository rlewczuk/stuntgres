package engine

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func alterAddUnitScope(t *testing.T) (*Engine, *StatementScope, AuthorizationView, *catalog.Relation) {
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
	old := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20000}, ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, ObjectName: "t", ObjectRevision: 1, Owner: auth.state.CurrentUser, Columns: []catalog.Column{{Name: "a", Usage: u, Nullable: true, NullabilityKnown: true, OriginTable: 20000, OriginAttribute: 1}}, Attributes: []int32{1}, ColumnMeta: catalog.ColumnMetadata{HighWater: 3}, Index: map[string]int{"a": 0}}
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
	if next.ColumnMeta.HighWater != 4 || !reflect.DeepEqual(next.Attributes, []int32{1, 4}) || next.Columns[1].OriginAttribute != 4 || next.Columns[1].OriginTable != 20000 || added.attribute != 4 || len(old.Columns) != 1 || old.ColumnMeta.HighWater != 3 || len(old.ColumnMeta.Defaults) != 0 {
		t.Fatal("stable identities/source changed", old, next, added)
	}
	if &next.Columns[0] == &old.Columns[0] || &next.Attributes[0] == &old.Attributes[0] {
		t.Fatal("candidate shares mutable shape")
	}
	next.Index["sentinel"] = 2
	if _, exists := old.Index["sentinel"]; exists {
		t.Fatal("candidate shares index")
	}
	delete(next.Index, "sentinel")
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
	if err != nil || same != old || ignored.column != nil || old.ColumnMeta.HighWater != 3 {
		t.Fatal(same, ignored, err)
	}
	limit := *old
	limit.ColumnMeta.HighWater = 1600
	_, _, err = e.addColumnCandidate(scope, scope.op, auth, &limit, action, nil)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "54011" || limit.ColumnMeta.HighWater != 1600 {
		t.Fatal(err)
	}
}

func TestAlterAddColumnNonvolatileDemandOnce(t *testing.T) {
	_, scope, _, old := alterAddUnitScope(t)
	u := old.Columns[0].Usage
	value, err := sqltype.Integer(u.Type, 7)
	if err != nil {
		t.Fatal(err)
	}
	for _, volatility := range []expr.Volatility{expr.Immutable, expr.Stable} {
		calls := 0
		expression := queryProbeExpression{Info: expr.Info{Usage: u, Property: expr.Properties{Volatility: volatility}}, eval: func(*EvalContext) (sqltype.Value, error) { calls++; return value, nil }}
		additions := []addedColumn{{attribute: 4, expression: expression, value: sqltype.Null(u.Type)}}
		fills, volatile, err := planAddedColumns(scope, additions)
		if err != nil || volatile || calls != 1 || fills[4] != value || additions[0].value != value {
			t.Fatal(fills, volatile, calls, err)
		}
		// Empty backfill does not suppress the earlier one-time demand.
		if rows, err := projectVersions(context.Background(), nil, old, old, fills); err != nil || len(rows) != 0 || calls != 1 {
			t.Fatal(rows, calls, err)
		}
		if expression.Property.Volatility != volatility {
			t.Fatal("stored expression changed")
		}
	}
}

func TestAlterAddColumnVolatileEffectiveLiveRewrite(t *testing.T) {
	_, scope, _, old := alterAddUnitScope(t)
	u := old.Columns[0].Usage
	one, err := sqltype.Integer(u.Type, 1)
	if err != nil {
		t.Fatal(err)
	}
	next := *old
	next.Columns = append(append([]catalog.Column(nil), old.Columns...), catalog.Column{Name: "b", Usage: u, Nullable: true})
	next.Attributes = []int32{1, 4}
	next.Index = map[string]int{"a": 0, "b": 1}
	next.ColumnMeta.HighWater = 4
	calls := 0
	expression := queryProbeExpression{Info: expr.Info{Usage: u, Property: expr.Properties{Volatility: expr.Volatile}}, eval: func(*EvalContext) (sqltype.Value, error) { calls++; return sqltype.Integer(u.Type, int64(calls)) }}
	additions := []addedColumn{{attribute: 4, column: &parser.Column{Name: "b"}, expression: expression, value: sqltype.Null(u.Type)}}
	fills, volatile, err := planAddedColumns(scope, additions)
	if err != nil || !volatile || calls != 0 || len(fills) != 0 {
		t.Fatal(fills, volatile, calls, err)
	}
	store := &storage.Relation{Generation: 1, Versions: []storage.RowVersion{
		{Row: 1, Version: 1, Born: 1, Values: []sqltype.Value{one}},
		{Row: 2, Version: 2, Born: 1, Values: []sqltype.Value{one}},
		{Row: 3, Version: 3, Born: 1, Values: []sqltype.Value{one}},
		{Row: 4, Version: 4, Born: 1, Values: []sqltype.Value{one}},
	}, Ends: &storage.DeletionRoot{Rows: map[ids.RowID]storage.RowEnd{2: {Version: 2, End: 2}}}}
	scope.ownWrites = map[ids.ObjectAddress][]storage.RowVersion{old.Address(): {{Row: 3, Version: 5, Base: 3, Values: []sqltype.Value{one}}, {Row: 5, Version: 6, Values: []sqltype.Value{one}}}}
	scope.ownDeletes = map[ids.ObjectAddress]*privateDeletionRoot{old.Address(): {rows: map[ids.RowID]deleteCandidate{4: {row: 4, version: 4}}}}
	transformCalls := 0
	transformed, err := sqltype.Integer(u.Type, 31)
	if err != nil {
		t.Fatal(err)
	}
	transform := queryProbeExpression{Info: expr.Info{Usage: u}, eval: func(ec *EvalContext) (sqltype.Value, error) {
		transformCalls++
		if len(ec.Row) != 1 || ec.Row[0] != one {
			t.Fatal("TYPE did not read original frame", ec.Row)
		}
		return transformed, nil
	}}
	transforms := []columnTransformation{{attribute: 1, expression: transform, rewrite: true}}
	replacement, own, deleted, err := rewriteTypeRows(scope, scope.op, old, &next, store, 2, transforms, additions, nil, newAlterRowValidator(&next, ""))
	if err != nil || calls != 3 || len(replacement.Versions) != 1 || len(own) != 2 || deleted != nil {
		t.Fatal(replacement, own, calls, err)
	}
	if replacement.Versions[0].Row != 1 || own[0].Row != 3 || own[0].Base != 0 || own[1].Row != 5 || len(store.Versions) != 4 || len(store.Versions[0].Values) != 1 || replacement.Generation != 2 {
		t.Fatal("identity/source rewrite invariant", replacement, own, store)
	}
	if transformCalls != 3 {
		t.Fatal("TYPE effective demand", transformCalls)
	}
	for i, row := range append(append([]storage.RowVersion(nil), replacement.Versions...), own...) {
		expectedDraw, drawErr := sqltype.Integer(u.Type, int64(i+1))
		if drawErr != nil {
			t.Fatal(drawErr)
		}
		if len(row.Values) != 2 || row.Values[0] != transformed || row.Values[1] != expectedDraw {
			t.Fatal("complete transformed/add frame", row.Values)
		}
	}
	for _, row := range store.Versions {
		if len(row.Values) != 1 || row.Values[0] != one {
			t.Fatal("committed predecessor mutated", row)
		}
	}
	for _, row := range scope.ownWrites[old.Address()] {
		if len(row.Values) != 1 || row.Values[0] != one {
			t.Fatal("private predecessor mutated", row)
		}
	}
}
