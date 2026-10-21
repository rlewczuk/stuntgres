package engine

import (
	"context"
	"strings"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
)

func TestPgClassBuiltinIndexMetadata(t *testing.T) {
	e := New()
	// Independent released identity/fact matrix, not helper-generated expected
	// output. Exercise initialized descriptors and the real typed projection.
	tables := []struct {
		oid  ids.OID
		name string
	}{
		{1213, "pg_tablespace"}, {1249, "pg_attribute"}, {1255, "pg_proc"}, {1259, "pg_class"},
		{1262, "pg_database"},
		{2600, "pg_aggregate"},
		{2224, "pg_sequence"}, {2396, "pg_shdescription"}, {2604, "pg_attrdef"},
		{2605, "pg_cast"}, {2606, "pg_constraint"}, {2607, "pg_conversion"},
		{2608, "pg_depend"}, {2609, "pg_description"}, {2610, "pg_index"},
		{2612, "pg_language"}, {2617, "pg_operator"},
		{2618, "pg_rewrite"}, {3079, "pg_extension"}, {3456, "pg_collation"},
		{3501, "pg_enum"}, {3541, "pg_range"}, {3600, "pg_ts_dict"}, {3764, "pg_ts_template"},
		{3576, "pg_transform"},
		{3602, "pg_ts_config"},
	}
	views := []string{"pg_roles", "pg_group", "pg_user", "pg_cursors", "pg_locks", "pg_timezone_names", "pg_timezone_abbrevs", "pg_available_extensions", "pg_available_extension_versions", "pg_sequences", "pg_views", "pg_prepared_xacts", "pg_indexes", "pg_tables", "pg_matviews", "table_privileges"}
	check := func() {
		view := e.CatalogRead()
		source := pgClassUnitSource(t, e)
		roles := e.Roles()
		project := func(d *catalog.Relation, want bool) {
			t.Helper()
			row, err := pgcatalog.ClassRow(pgcatalog.Input{Context: context.Background(), Catalog: view, Roles: roles, Owner: e.catalog.owner(), Source: source, Runtime: engineCatalogRuntime{}}, d)
			if err != nil {
				t.Fatal(d.Name(), err)
			}
			indexed, err := row[14].Boolean()
			if err != nil || indexed != want || d.History.HasIndexes != want {
				t.Fatal("owned index metadata/projection", d.Name(), d.History.HasIndexes, indexed, want, err)
			}
		}
		for _, fact := range tables {
			d, ok := view.Relation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: fact.oid})
			if !ok || d.Name() != fact.name || d.RelationKind != catalog.CatalogRelation {
				t.Fatal("represented table identity", fact, d)
			}
			project(d, true)
		}
		for _, name := range views {
			found := 0
			for _, descriptor := range view.Descriptors() {
				d, ok := descriptor.(*catalog.Relation)
				if ok && d.Name() == name && d.RelationKind == catalog.CatalogRelation {
					found++
					project(d, false)
				}
			}
			if found != 1 {
				t.Fatal("represented view multiplicity", name, found)
			}
		}
		// Ordinary descriptors must not acquire compatibility facts merely
		// because their implementation/source kind resembles a table.
		ordinary := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 200555}, ObjectName: "unindexed", RelationKind: catalog.TableRelation, Owner: e.roles.domain.Principal(10)}
		project(ordinary, false)
	}
	check()
	if err := e.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	check()
	if err := e.Restore(context.Background(), strings.NewReader("")); err != nil {
		t.Fatal(err)
	}
	check()
}
