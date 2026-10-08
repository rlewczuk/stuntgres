package stuntgres_test

import (
	"database/sql"
	"reflect"
	"stuntgres"
	"testing"
)

// TestBinaryBasicBehavior specifies all seven operations independently of the
// provider and of PostgreSQL comparison, through the public database/sql API.
func TestBinaryBasicBehavior(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	for _, tc := range []struct {
		query    string
		affected int64
	}{
		{"CREATE TABLE t(id INTEGER,v BYTEA,s BYTEA)", 0},
		{`INSERT INTO t VALUES(1,'\x00ff8041008042ff00','\xff8000')`, 1},
	} {
		result, err := db.Exec(tc.query)
		if err != nil {
			t.Fatal(tc.query, err)
		}
		if n, err := result.RowsAffected(); err != nil || n != tc.affected {
			t.Fatalf("affected=%d, want %d: %v", n, tc.affected, err)
		}
	}
	query := "SELECT v||s AS cat,bit_length(v) AS bits,octet_length(v) AS octets,length(v) AS len,btrim(v,s) AS bt,ltrim(v,s) AS lt,rtrim(v,s) AS rt FROM t WHERE id=1"
	want := []any{
		[]byte{0, 255, 128, 65, 0, 128, 66, 255, 0, 255, 128, 0},
		int64(72), int64(9), int64(9),
		[]byte{65, 0, 128, 66},
		[]byte{65, 0, 128, 66, 255, 0},
		[]byte{0, 255, 128, 65, 0, 128, 66},
	}
	check := func(t *testing.T, rows *sql.Rows) {
		t.Helper()
		defer rows.Close()
		columns, err := rows.Columns()
		if err != nil || !reflect.DeepEqual(columns, []string{"cat", "bits", "octets", "len", "bt", "lt", "rt"}) {
			t.Fatal(columns, err)
		}
		types, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		for i, typ := range types {
			name, scan := "BYTEA", reflect.TypeOf([]byte{})
			if i >= 1 && i <= 3 {
				name, scan = "INT4", reflect.TypeOf(int64(0))
			}
			if typ.DatabaseTypeName() != name || typ.ScanType() != scan {
				t.Fatalf("column %d: %s/%v, want %s/%v", i, typ.DatabaseTypeName(), typ.ScanType(), name, scan)
			}
		}
		if !rows.Next() {
			t.Fatal("missing row", rows.Err())
		}
		got := make([]any, len(want))
		dest := make([]any, len(want))
		for i := range got {
			dest[i] = &got[i]
		}
		if err := rows.Scan(dest...); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %#v, want %#v", got, want)
		}
		if rows.Next() || rows.Err() != nil {
			t.Fatal("unexpected trailing row/error", rows.Err())
		}
	}
	t.Run("direct", func(t *testing.T) {
		rows, err := db.Query(query)
		if err != nil {
			t.Fatal(err)
		}
		check(t, rows)
	})
	t.Run("prepared", func(t *testing.T) {
		stmt, err := db.Prepare(query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		rows, err := stmt.Query()
		if err != nil {
			t.Fatal(err)
		}
		check(t, rows)
	})
	wantTuples(t, db, "SELECT bit_length(v||s),octet_length(v||s),length(v||s) FROM t WHERE id=1", [][]any{{int64(96), int64(12), int64(12)}})
}
