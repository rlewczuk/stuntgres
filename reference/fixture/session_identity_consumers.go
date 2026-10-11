package fixture

import (
	"database/sql"
	"reflect"
	"testing"
)

func SessionIdentityMetadata(pg bool) func(*testing.T, *sql.Rows) {
	return func(t *testing.T, rows *sql.Rows) {
		t.Helper()
		types, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		for _, typ := range types {
			if typ.DatabaseTypeName() != "NAME" && typ.DatabaseTypeName() != "TEXT" {
				continue
			}
			scan := reflect.TypeFor[string]()
			if pg && typ.DatabaseTypeName() == "NAME" {
				scan = reflect.TypeFor[any]()
			}
			if typ.ScanType() != scan {
				t.Fatal(typ.Name(), typ.ScanType(), scan)
			}
		}
	}
}
