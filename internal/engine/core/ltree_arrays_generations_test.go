package core_test

import (
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/sqltype"
)

// Exercise the published companions and actual stored generations. Constructing
// an Array descriptor independently cannot prove catalog storage/import admission.
func ltreeArrayStoredGenerations(t *testing.T) {
	for _, family := range []string{"ltree", "lquery"} {
		t.Run(family, func(t *testing.T) {
			e := engine.New()
			op, done := enginetest.RoleOperation(t, e, "postgres")
			defer done()
			enginetest.RoleExecute(t, e, op, "CREATE EXTENSION ltree")
			enginetest.RoleExecute(t, e, op, "CREATE SCHEMA la_generation_ns")
			enginetest.RoleExecute(t, e, op, "CREATE TABLE la_generation(a "+family+"[], s "+family+")")
			enginetest.RoleExecute(t, e, op, "INSERT INTO la_generation VALUES ('[7:8]={a,NULL}','a'), ('{{a,b}}','b'), ('{}',NULL), (NULL,'a')")
			capture := func(owner *core.Engine) (*catalog.Relation, *storage.Relation) {
				t.Helper()
				view := owner.CatalogRead()
				address := view.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "la_generation"})
				return view.MustRelation(address), owner.Storage.Relations[address]
			}
			assertRemint := func(old, current *catalog.Relation, before, after *storage.Relation) {
				t.Helper()
				if old == current || before == after || len(before.Versions) != 4 || len(after.Versions) != len(before.Versions) {
					t.Fatal("stored generations were not independently reowned")
				}
				array, scalar := current.Columns[0].Usage.Type, current.Columns[1].Usage.Type
				if array == old.Columns[0].Usage.Type || scalar == old.Columns[1].Usage.Type || array.Element() != scalar || !array.Concrete() || !typedef.StoredArrayElement(scalar) {
					t.Fatal("published concrete companion lost exact current scalar generation")
				}
				checkScalar := func(previous, value sqltype.Value) {
					t.Helper()
					if value.Type() != scalar || value.IsNull() != previous.IsNull() {
						t.Fatal("stored scalar/element NULL retained old authority")
					}
					var back sqltype.Value
					var err error
					if family == "ltree" {
						back, err = value.RemapLtree(previous.Type())
					} else {
						back, err = value.RemapLquery(previous.Type())
					}
					if err != nil || back != previous {
						t.Fatal("stored import changed immutable compiled backing", err)
					}
				}
				for i, row := range before.Versions {
					previous, value := row.Values[0], after.Versions[i].Values[0]
					if previous.Type() != old.Columns[0].Usage.Type || value.Type() != array || value.IsNull() != previous.IsNull() || !reflect.DeepEqual(value.ArrayDimensions(), previous.ArrayDimensions()) || value.ArrayLen() != previous.ArrayLen() {
						t.Fatal("array generation, NULL framing, rank or bounds changed")
					}
					for j := range previous.ArrayLen() {
						checkScalar(previous.ArrayElement(j), value.ArrayElement(j))
					}
					checkScalar(row.Values[1], after.Versions[i].Values[1])
				}
			}
			old, before := capture(e)
			oldValues := make([][]sqltype.Value, len(before.Versions))
			for i, row := range before.Versions {
				oldValues[i] = append([]sqltype.Value(nil), row.Values...)
			}
			enginetest.RoleExecute(t, e, op, "ALTER TYPE "+family+" RENAME TO la_generation_scalar")
			renamed, renamedStore := capture(e)
			assertRemint(old, renamed, before, renamedStore)
			enginetest.RoleExecute(t, e, op, "ALTER TYPE la_generation_scalar SET SCHEMA la_generation_ns")
			moved, movedStore := capture(e)
			assertRemint(renamed, moved, renamedStore, movedStore)

			target := engine.New()
			if _, err := target.ReplaceCatalog(t.Context(), e, e.CatalogRead()); err != nil {
				t.Fatal(err)
			}
			imported, importedStore := capture(target)
			assertRemint(moved, imported, movedStore, importedStore)
			view := target.CatalogRead()
			installed := view.MustExtension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "ltree"}))
			if err := extension.StockLtree.ValidateExtension(view, installed); err != nil {
				t.Fatal("imported companion/provider graph invalid", err)
			}
			for i, row := range before.Versions {
				if !reflect.DeepEqual(row.Values, oldValues[i]) {
					t.Fatal("rename/move/replacement mutated retained source backing")
				}
			}
		})
	}
}
