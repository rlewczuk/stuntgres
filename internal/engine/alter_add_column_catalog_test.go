package engine

import (
	"context"
	"reflect"
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/visibility"
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
		d.ColumnMeta.HighWater = tc.high
		d.Attributes = []int32{tc.attribute}
		stage := e.BeginCatalogStage(auth.Catalog)
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
	if err := table.BindChecks(AlterHost{TableHost{core.DDLHost{E: e, Op: scope.Op}}, scope}, auth, old, []*parser.CheckConstraint{raw("CREATE TABLE t(CONSTRAINT original CHECK(a>0))")}, scope.Input, true, nil); err != nil {
		t.Fatal(err)
	}
	old.Constraints.Check[0].Ordinal = 7
	next := *old
	next.Constraints.Check = slices.Clone(old.Constraints.Check)
	checks := []*parser.CheckConstraint{raw("CREATE TABLE t(CONSTRAINT z_first CHECK(a>1))"), raw("CREATE TABLE t(CONSTRAINT a_second CHECK(a<9))")}
	if err := table.BindChecks(AlterHost{TableHost{core.DDLHost{E: e, Op: scope.Op}}, scope}, auth, &next, checks, scope.Input, true, nil); err != nil {
		t.Fatal(err)
	}
	ordered := table.OrderedChecks(&next)
	if len(ordered) != 3 || ordered[0].Ordinal != 7 || ordered[1].Ordinal != 8 || ordered[2].Ordinal != 9 || ordered[1].Name != "z_first" || ordered[2].Name != "a_second" || next.Constraints.Check[0].Name != "a_second" || len(old.Constraints.Check) != 1 || old.Constraints.Check[0].Name != "original" {
		t.Fatal("CHECK ordering/source invariant", ordered, next.Constraints.Check, old.Constraints.Check)
	}
	e.Mu.Lock()
	err := table.AssignDefinitionOIDs(core.DDLHost{E: e}, &next)
	e.Mu.Unlock()
	if err != nil || old.Constraints.Check[0].OID != 0 || next.Constraints.Check[0].OID == 0 {
		t.Fatal("OID mutation reached source", err)
	}
	stage := e.BeginCatalogStage(auth.Catalog)
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
	old := &catalog.Relation{Columns: []catalog.Column{{Name: "old", Usage: u}}, Attributes: []int32{1}, ColumnMeta: catalog.ColumnMetadata{HighWater: 1}}
	next := &catalog.Relation{Columns: []catalog.Column{{Name: "new", Usage: u}}, Attributes: []int32{2}, ColumnMeta: catalog.ColumnMetadata{HighWater: 2}}
	original := &storage.Relation{Versions: []storage.RowVersion{{Row: 1, Version: 1, Born: 1, Values: []sqltype.Value{one}}}}
	historical := []storage.RowVersion{{Row: 2, Version: 2, Born: 1, Values: []sqltype.Value{one}}}
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20000}
	shape := session.StorageChange{Original: original, Root: original.Versions, Relation: next, Replacement: &storage.Relation{}, Fills: map[int32]sqltype.Value{2: seven}}
	catalog := &session.Journal{Storage: map[ids.ObjectAddress]session.StorageChange{address: shape}}
	roots := map[ids.ObjectAddress]visibility.Root{address: {Storage: original, Relation: old, Versions: historical}}
	projected, err := session.ProjectPrivateSnapshotRoots(context.Background(), catalog, roots)
	if err != nil || projected == catalog || !projected.Storage[address].HasSnapshot || catalog.Storage[address].HasSnapshot {
		t.Fatal(projected, err)
	}
	rows := projected.Storage[address].Snapshot
	if len(rows) != 1 || rows[0].Row != 2 || rows[0].Version != 2 || !reflect.DeepEqual(rows[0].Values, []sqltype.Value{seven}) || !reflect.DeepEqual(historical[0].Values, []sqltype.Value{one}) {
		t.Fatal("equal-width historical root changed", rows, historical)
	}
}

func TestAlterAddColumnNativeImportPreservesCeilingAndSource(t *testing.T) {
	source, scope, auth, old := alterAddUnitScope(t)
	next, _, err := AddColumnCandidate(source, scope, scope.Op, auth, old, alterAddUnitAction(t, "ALTER TABLE t ADD b INT DEFAULT 7"), nil)
	if err != nil {
		t.Fatal(err)
	}
	source.Mu.Lock()
	err = table.AssignDefinitionOIDs(core.DDLHost{E: source}, next)
	source.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	stage := source.BeginCatalogStage(auth.Catalog)
	defer source.DiscardCatalogStage(stage)
	if err := stage.Create(next); err != nil {
		t.Fatal(err)
	}
	prepared, err := source.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := publishCatalog(source, prepared, core.PublicationToken{Epoch: auth.Catalog.Epoch()}); err != nil {
		t.Fatal(err)
	}
	before := source.CatalogRead()
	destination := New()
	if _, err := destination.ReplaceCatalog(context.Background(), source, before); err != nil {
		t.Fatal(err)
	}
	imported := destination.CatalogRead().MustRelation(next.Address())
	if imported.ColumnMeta.HighWater != 4 || !reflect.DeepEqual(imported.Attributes, []int32{1, 4}) || imported.Columns[1].OriginAttribute != 4 || source.CatalogRead().State() != before.State() || before.Get(next.Address()) != next || old.ColumnMeta.HighWater != 3 {
		t.Fatal("import changed source or stable identity", imported, next, old)
	}
	// Foreign replacement adopts an immutable catalog generation. Provider
	// validity is membership in the installed generation, not pointer inequality
	// across owners (ReplaceCatalog clones the source catalog and remaps refs).
	view := destination.CatalogRead()
	node := imported.ColumnMeta.Defaults[4].Expression
	usage := node.ResultUsage()
	provider, exists := view.LookupType(usage.Type.ID())
	if !exists || provider != usage.Type || usage != imported.Columns[1].Usage {
		t.Fatal("imported default uses a noninstalled type provider")
	}
	value, err := node.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}})
	if err != nil {
		t.Fatal(err)
	}
	integer, err := value.Integer()
	if err != nil || integer != 7 {
		t.Fatal("imported default changed", integer, err)
	}
	if err := destination.Catalog.Providers[ids.RelationClassID].Validate(view, imported); err != nil {
		t.Fatal("invalid imported definition/provider manifest", err)
	}
}
