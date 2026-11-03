package enginetests_test

import (
	"strings"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/core/catalogrt"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/sqltype"
)

// Independent released namespace partition; no production filter is needed by
// the all view, and no missing sibling SQL relation is manufactured.
func statioSystemNamespace(name string) bool {
	return name == "pg_catalog" || name == "information_schema" || strings.HasPrefix(name, "pg_toast")
}

func TestStatioAllIndexesMembership(t *testing.T) {
	t.Run("represented_parent_kinds", func(t *testing.T) {
		f := newDefaultUnitFixture(t)
		state := catalog.CloneState(f.e.Catalog.State)
		integer, _ := f.e.Catalog.Read(state).LookupType(sqltype.Int4)
		type identity struct {
			parent, index ids.OID
		}
		want := make(map[string]identity)
		for i, tc := range []struct {
			name     string
			kind     catalog.RelationKind
			class    string
			included bool
		}{
			{"statio_heap", catalog.TableRelation, "r", true},
			{"statio_materialized", catalog.MaterializedViewRelation, "m", true},
			{"statio_partitioned", catalog.PartitionedRelation, "p", false},
		} {
			parent := &catalog.Relation{
				ObjectAddress:   ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ids.OID(20500 + i*2)},
				ObjectNamespace: f.schema, ObjectName: tc.name, ObjectRevision: 1, Owner: f.owner,
				RelationKind: tc.kind, Attributes: []int32{1},
				Columns: []catalog.Column{{Name: "k", Usage: integer.Usage()}},
			}
			parent.History.HasIndexes = true
			index := &catalog.Relation{
				ObjectAddress:   ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: parent.Address().ObjectID + 1},
				ObjectNamespace: f.schema, ObjectName: tc.name + "_i", ObjectRevision: 1, Owner: f.owner,
				RelationKind: catalog.IndexRelation,
				SQLIndex:     &catalog.IndexDefinition{Parent: parent.Address(), Keys: []catalog.IndexKey{{Attribute: 1}}},
			}
			state.SetDescriptor(parent.Address(), parent)
			state.SetDescriptor(index.Address(), index)
			if got := pgcatalog.ClassKind(f.e.Catalog.Read(state), parent); got != tc.class {
				t.Fatal("parent kind witness", tc.name, got, tc.class)
			}
			if tc.included {
				want[index.Name()] = identity{parent.Address().ObjectID, index.Address().ObjectID}
			}
		}
		// A real SQLIndex descriptor for the p parent is present in this
		// captured catalog: omission must come from the production kind filter.
		view := f.e.Catalog.Read(state)
		auth := dtpUnitAuthority(t, f, view)
		source := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.StatioAllIndexesOID})
		in := pgcatalog.Input{
			Context: t.Context(), Catalog: view, Roles: auth.Roles, Owner: view.Owner(),
			Ref: view.Owner().Domain.Ref(view.Epoch(), source.Address()), Source: source,
			Runtime: core.NewCatalogRuntime(core.CatalogRuntimeFields{Engine: f.e, Auth: auth}),
		}
		produce, ok := pgcatalog.ProducerFor(pgcatalog.StatioAllIndexesOID)
		if !ok {
			t.Fatal("missing statio producer")
		}
		captured, err := catalogrt.CaptureRows(in, produce)
		if err != nil {
			t.Fatal(err)
		}
		defer captured.Close()
		rows := captured.Anonymous
		if len(rows) != len(want) {
			t.Fatal("represented parent-kind membership", rows, want)
		}
		for _, row := range rows {
			name, err := row[4].Text()
			expected, ok := want[name]
			if err != nil || !ok {
				t.Fatal("excluded or invented index", row, err)
			}
			parentID, parentErr := row[0].OID()
			indexID, indexErr := row[1].OID()
			if parentErr != nil || indexErr != nil || parentID != uint32(expected.parent) || indexID != uint32(expected.index) {
				t.Fatal("represented identity", row, expected, parentErr, indexErr)
			}
			delete(want, name)
		}
		if len(want) != 0 {
			t.Fatal("missing included parent kind", want)
		}
	})
	for _, tc := range []struct {
		name   string
		system bool
	}{
		{"pg_catalog", true}, {"information_schema", true}, {"pg_toast", true},
		{"pg_toast_temp_7", true}, {"pg_toastlike", true},
		{"public", false}, {"pg_temp_7", false}, {"xpg_toast", false},
		{"PG_TOAST", false}, {"information_schemax", false}, {"", false},
	} {
		if got := statioSystemNamespace(tc.name); got != tc.system {
			t.Fatalf("namespace %q system=%v want %v", tc.name, got, tc.system)
		}
	}
}
