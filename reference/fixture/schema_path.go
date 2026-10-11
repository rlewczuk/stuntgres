package fixture

import (
	"database/sql"
	"reflect"
	"testing"
)

func SchemaReferenceRow(t *testing.T, r *sql.Rows, label, typ string, want any) {
	t.Helper()
	defer r.Close()
	labels, err := r.Columns()
	if err != nil || !reflect.DeepEqual(labels, []string{label}) {
		t.Fatal(labels, err)
	}
	types, err := r.ColumnTypes()
	if err != nil || len(types) != 1 || types[0].DatabaseTypeName() != typ {
		t.Fatal(types, err)
	}
	if !r.Next() {
		t.Fatal("missing row", r.Err())
	}
	var got any
	if err = r.Scan(&got); err != nil {
		t.Fatal(err)
	}
	if bytes, ok := got.([]byte); ok {
		got = string(bytes)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("value %#v want %#v", got, want)
	}
	if r.Next() || r.Err() != nil {
		t.Fatal("extra row/error", r.Err())
	}
}
