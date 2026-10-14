package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

// regtsAssertAliasRows checks retained publications without routing the new
// alias families through the generic fixture's closed raw-type inventory.
func regtsAssertAliasRows(t *testing.T, rows *sql.Rows, want [][]any, pg bool) {
	t.Helper()
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(columns, []string{"d", "c"}) {
		t.Fatalf("retained columns=%v", columns)
	}
	types, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	for i, typ := range types {
		family := []string{"REGDICTIONARY", "REGCONFIG"}[i]
		scan := reflect.TypeFor[string]()
		if pg {
			scan = reflect.TypeFor[any]()
		}
		length, lengthOK := typ.Length()
		precision, scale, decimalOK := typ.DecimalSize()
		if typ.DatabaseTypeName() != family || typ.ScanType() != scan || length != 0 || lengthOK || precision != 0 || scale != 0 || decimalOK {
			t.Fatalf("retained metadata column %d: type=%s scan=%v length=%d/%t decimal=%d,%d/%t", i, typ.DatabaseTypeName(), typ.ScanType(), length, lengthOK, precision, scale, decimalOK)
		}
	}
	var got [][]any
	for rows.Next() {
		var dictionary, config any
		if err := rows.Scan(&dictionary, &config); err != nil {
			t.Fatal(err)
		}
		fixture.RegtypeRaw(t, pg, dictionary)
		fixture.RegtypeRaw(t, pg, config)
		got = append(got, []any{fixture.Normalize(dictionary), fixture.Normalize(config)})
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("retained alias rows=%#v want %#v", got, want)
	}
}
