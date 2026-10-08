package reference_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"reflect"
	"testing"
)

func TestReferenceFixtureByteOwnership(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value driver.Value
	}{
		{"null", nil},
		{"typed_nil", []byte(nil)},
		{"empty", []byte{}},
		{"nonempty", []byte{1, 2, 3}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := referenceCopyValue(tc.value)
			if !reflect.DeepEqual(got, tc.value) {
				t.Fatalf("owned value = %#v, want %#v", got, tc.value)
			}
			r := &referenceRecorder{}
			op := r.begin("owned argument", []driver.NamedValue{{Ordinal: 1, Value: tc.value}})
			if !reflect.DeepEqual(op.Args[0].Value, tc.value) {
				t.Fatalf("owned argument = %#v, want %#v", op.Args[0].Value, tc.value)
			}
			if source, ok := tc.value.([]byte); ok && len(source) > 0 {
				source[0] = 9
				if got.([]byte)[0] != 1 || op.Args[0].Value.([]byte)[0] != 1 {
					t.Fatal("source mutation changed owned bytes")
				}
				got.([]byte)[1] = 8
				if source[1] != 2 || op.Args[0].Value.([]byte)[1] != 2 {
					t.Fatal("copy mutation changed source or separately owned argument")
				}
			}
		})
	}

	// Consume real public adapter rows through sqlmw, before normalization can
	// hide a non-NULL empty BYTEA becoming a typed nil slice.
	db, err := sql.Open("stuntgres_reference_memory", "owned-bytes/"+t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	r := &referenceRecorder{}
	ctx := context.WithValue(context.Background(), referenceRecorderKey{}, r)
	if _, err := db.ExecContext(ctx, "CREATE TABLE owned_bytes (id INT, v BYTEA)"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO owned_bytes VALUES (1, ''), (2, '\\x0102'), (3, NULL)"); err != nil {
		t.Fatal(err)
	}
	rows, err := db.QueryContext(ctx, "SELECT v FROM owned_bytes ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	want := []any{[]byte{}, []byte{1, 2}, nil}
	for i, expected := range want {
		if !rows.Next() {
			t.Fatalf("row %d missing: %v", i, rows.Err())
		}
		var value any
		if err := rows.Scan(&value); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(value, expected) {
			t.Fatalf("adapter row %d = %#v, want %#v", i, value, expected)
		}
		if b, ok := value.([]byte); ok && len(b) > 0 {
			b[0] = 9
		}
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatalf("unexpected trailing rows/error: %v", rows.Err())
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	op := r.operations[len(r.operations)-1]
	if !op.Closed || op.CloseErr != nil {
		t.Fatalf("recorded cursor cleanup = %v, %v", op.Closed, op.CloseErr)
	}
	if !reflect.DeepEqual(op.Rows, [][]driver.Value{{[]byte{}}, {[]byte{1, 2}}, {nil}}) {
		t.Fatalf("owned recorded rows = %#v", op.Rows)
	}
}
