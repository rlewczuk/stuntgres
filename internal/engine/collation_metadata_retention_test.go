package engine

import (
	"context"
	"reflect"
	"strconv"
	"testing"

	"stuntgres/internal/engine/expr"
)

func TestCollationStoredExpressionMetadataRetention(t *testing.T) {
	e := New()
	owner := sessionStart(t, e, nil)
	sessionSQL(t, owner, `CREATE COLLATION retained_metadata FROM pg_catalog.pg_unicode_fast`)
	sessionSQL(t, owner, `CREATE TABLE retained_metadata_t(v TEXT DEFAULT upper('a' COLLATE retained_metadata))`)
	sessionSQL(t, owner, `CREATE VIEW retained_metadata_v AS SELECT upper('a' COLLATE retained_metadata) AS v`)
	snapshot := e.CatalogRead()
	tableRef := catalogRef(t, e, snapshot, "retained_metadata_t")
	oldTable := snapshot.MustRelation(tableRef.Address)
	oldDefault := oldTable.Defaults[oldTable.Attributes[0]].expression
	original, err := defaultSQL(context.Background(), snapshot, oldDefault)
	if err != nil {
		t.Fatal(err)
	}
	infoType, catalogType := reflect.TypeFor[expr.Info](), reflect.TypeFor[CatalogRead]()
	assertMetadata := func(root any) {
		t.Helper()
		seen := make(map[uintptr]bool)
		count := 0
		var walk func(reflect.Value)
		walk = func(value reflect.Value) {
			if !value.IsValid() {
				return
			}
			if value.Type() == infoType {
				count++
				for i := range value.NumField() {
					if value.Field(i).Type() == catalogType {
						t.Fatal("persisted scalar metadata retains historical catalog", value.Type().Field(i).Name)
					}
				}
				return
			}
			switch value.Kind() {
			case reflect.Interface:
				if !value.IsNil() {
					walk(value.Elem())
				}
			case reflect.Pointer:
				if !value.IsNil() && !seen[value.Pointer()] {
					seen[value.Pointer()] = true
					walk(value.Elem())
				}
			case reflect.Struct:
				for i := range value.NumField() {
					walk(value.Field(i))
				}
			case reflect.Array, reflect.Slice:
				for i := range value.Len() {
					walk(value.Index(i))
				}
			}
		}
		walk(reflect.ValueOf(root))
		if count == 0 {
			t.Fatal("regression did not inspect genuine stored expression metadata")
		}
	}
	for i := range 16 {
		literal := "'generation" + strconv.Itoa(i) + "'"
		sessionSQL(t, owner, `ALTER TABLE retained_metadata_t ALTER v SET DEFAULT upper(`+literal+` COLLATE retained_metadata)`)
		sessionSQL(t, owner, `CREATE OR REPLACE VIEW retained_metadata_v AS SELECT upper(`+literal+` COLLATE retained_metadata) AS v`)
		current := e.CatalogRead()
		table := current.MustRelation(tableRef.Address)
		viewRef := catalogRef(t, e, current, "retained_metadata_v")
		view := current.MustRelation(viewRef.Address)
		if current.state == snapshot.state || table == oldTable {
			t.Fatal("replacement did not produce a genuine successor catalog")
		}
		assertMetadata(table.Defaults[table.Attributes[0]].expression)
		assertMetadata(viewOf(view).query.Root)
	}
	// An explicitly user-held old read remains a legitimate snapshot; removal of
	// hidden expression metadata roots must not invalidate its concrete Usage.
	if actual, err := defaultSQL(context.Background(), snapshot, oldDefault); err != nil || actual != original {
		t.Fatal("held old definition snapshot changed", actual, original, err)
	}
	sessionSQL(t, owner, `INSERT INTO retained_metadata_t DEFAULT VALUES`)
	rows := tuples(sessionSQL(t, owner, `SELECT v FROM retained_metadata_t`))
	if len(rows) != 1 || len(rows[0]) != 1 || rows[0][0] != "GENERATION15" {
		t.Fatal("current default replacement semantics changed", rows)
	}
}
