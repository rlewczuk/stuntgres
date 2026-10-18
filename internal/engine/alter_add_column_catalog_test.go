package engine

import (
	"context"
	"reflect"
	"slices"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestAlterAddColumnDescriptorValidation(t *testing.T) {
	e, _, auth, original := alterAddUnitScope(t)
	for _, tc := range []struct {
		high      int32
		attribute int32
		valid     bool
	}{{3, 1, true}, {0, 1, false}, {-1, 1, false}, {3, 4, false}} {
		d := *original
		d.AttributeHighWater = tc.high
		d.Attributes = []int32{tc.attribute}
		stage := e.BeginCatalogStage(auth.catalog)
		if err := stage.Create(&d); err != nil {
			t.Fatal(err)
		}
		_, err := e.ValidateCatalogStage(context.Background(), stage)
		e.DiscardCatalogStage(stage)
		if (err == nil) != tc.valid {
			t.Fatal(tc, err)
		}
	}
}

func TestAlterAddColumnSparseCheckOrdinalsAndOwnership(t *testing.T) {
	e, scope, auth, old := alterAddUnitScope(t)
	raw := func(query string) *parser.CheckConstraint {
		statements, err := parser.Parse(query)
		if err != nil {
			t.Fatal(err)
		}
		return statements[0].(*parser.CreateTable).Elements[0].Check
	}
	if err := e.bindColumnChecks(scope.op, auth, []*parser.CheckConstraint{raw("CREATE TABLE t(CONSTRAINT original CHECK(a>0))")}, old, scope.input, true, nil); err != nil {
		t.Fatal(err)
	}
	old.CheckConstraints[0].Ordinal = 7
	next := *old
	next.CheckConstraints = slices.Clone(old.CheckConstraints)
	checks := []*parser.CheckConstraint{raw("CREATE TABLE t(CONSTRAINT z_first CHECK(a>1))"), raw("CREATE TABLE t(CONSTRAINT a_second CHECK(a<9))")}
	if err := e.bindColumnChecks(scope.op, auth, checks, &next, scope.input, true, nil); err != nil {
		t.Fatal(err)
	}
	ordered := orderedChecks(&next)
	if len(ordered) != 3 || ordered[0].Ordinal != 7 || ordered[1].Ordinal != 8 || ordered[2].Ordinal != 9 || ordered[1].Name != "z_first" || ordered[2].Name != "a_second" || next.CheckConstraints[0].Name != "a_second" || len(old.CheckConstraints) != 1 || old.CheckConstraints[0].Name != "original" {
		t.Fatal("CHECK ordering/source invariant", ordered, next.CheckConstraints, old.CheckConstraints)
	}
	e.mu.Lock()
	err := e.assignDefinitionOIDsLocked(&next)
	e.mu.Unlock()
	if err != nil || old.CheckConstraints[0].OID != 0 || next.CheckConstraints[0].OID == 0 {
		t.Fatal("OID mutation reached source", err)
	}
	stage := e.BeginCatalogStage(auth.catalog)
	defer e.DiscardCatalogStage(stage)
	if err := stage.Create(&next); err != nil {
		t.Fatal(err)
	}
	if _, err := e.ValidateCatalogStage(context.Background(), stage); err != nil {
		t.Fatal("valid sparse ordinals rejected", err)
	}
}

func TestAlterAddColumnPrivateSnapshotEqualWidthProjection(t *testing.T) {
	e := New()
	u := testUsage(e, sqltype.Int4)
	one, err := sqltype.Integer(u.Type, 1)
	if err != nil {
		t.Fatal(err)
	}
	seven, err := sqltype.Integer(u.Type, 7)
	if err != nil {
		t.Fatal(err)
	}
	old := &relationDescriptor{Columns: []ColumnDescriptor{{Name: "old", Usage: u}}, Attributes: []int32{1}, AttributeHighWater: 1}
	next := &relationDescriptor{Columns: []ColumnDescriptor{{Name: "new", Usage: u}}, Attributes: []int32{2}, AttributeHighWater: 2}
	original := &relationStorage{versions: []rowVersion{{row: 1, version: 1, born: 1, values: []sqltype.Value{one}}}}
	historical := []rowVersion{{row: 2, version: 2, born: 1, values: []sqltype.Value{one}}}
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20000}
	shape := catalogStorageChange{original: original, root: original.versions, relation: next, replacement: &relationStorage{}, fills: map[int32]sqltype.Value{2: seven}}
	catalog := &catalogChanges{storage: map[ids.ObjectAddress]catalogStorageChange{address: shape}}
	roots := map[ids.ObjectAddress]transactionRoot{address: {storage: original, relation: old, versions: historical}}
	projected, err := projectPrivateSnapshotRoots(context.Background(), catalog, roots)
	if err != nil || projected == catalog || !projected.storage[address].hasSnapshot || catalog.storage[address].hasSnapshot {
		t.Fatal(projected, err)
	}
	rows := projected.storage[address].snapshot
	if len(rows) != 1 || rows[0].row != 2 || rows[0].version != 2 || !reflect.DeepEqual(rows[0].values, []sqltype.Value{seven}) || !reflect.DeepEqual(historical[0].values, []sqltype.Value{one}) {
		t.Fatal("equal-width historical root changed", rows, historical)
	}
}

func TestAlterAddColumnNativeImportPreservesCeilingAndSource(t *testing.T) {
	source, scope, auth, old := alterAddUnitScope(t)
	next, _, err := source.addColumnCandidate(scope, scope.op, auth, old, alterAddUnitAction(t, "ALTER TABLE t ADD b INT DEFAULT 7"), nil)
	if err != nil {
		t.Fatal(err)
	}
	source.mu.Lock()
	err = source.assignDefinitionOIDsLocked(next)
	source.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	stage := source.BeginCatalogStage(auth.catalog)
	defer source.DiscardCatalogStage(stage)
	if err := stage.Create(next); err != nil {
		t.Fatal(err)
	}
	prepared, err := source.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.PublishCatalog(prepared, PublicationToken{Epoch: auth.catalog.Epoch()}); err != nil {
		t.Fatal(err)
	}
	before := source.CatalogRead()
	destination := New()
	if _, err := destination.ReplaceCatalog(context.Background(), source, before); err != nil {
		t.Fatal(err)
	}
	imported := destination.CatalogRead().MustRelation(next.Address())
	if imported.AttributeHighWater != 4 || !reflect.DeepEqual(imported.Attributes, []int32{1, 4}) || imported.Columns[1].OriginAttribute != 4 || source.CatalogRead().State() != before.State() || before.Get(next.Address()) != next || old.AttributeHighWater != 3 {
		t.Fatal("import changed source or stable identity", imported, next, old)
	}
	// Foreign replacement adopts an immutable catalog generation. Provider
	// validity is membership in the installed generation, not pointer inequality
	// across owners (ReplaceCatalog clones the source catalog and remaps refs).
	view := destination.CatalogRead()
	node := imported.Defaults[4].Expression
	usage := node.ResultUsage()
	provider, exists := view.LookupType(usage.Type.ID())
	if !exists || provider != usage.Type || usage != imported.Columns[1].Usage {
		t.Fatal("imported default uses a noninstalled type provider")
	}
	value, err := node.Eval(&EvalContext{Context: context.Background(), Scope: 1, Frame: 1})
	if err != nil {
		t.Fatal(err)
	}
	integer, err := value.Integer()
	if err != nil || integer != 7 {
		t.Fatal("imported default changed", integer, err)
	}
	if err := destination.catalog.Providers[ids.RelationClassID].Validate(view, imported); err != nil {
		t.Fatal("invalid imported definition/provider manifest", err)
	}
}
