package stuntgres_test

import (
	"database/sql"
	"reflect"
	"stuntgres"
	"testing"
)

func TestBinarySliceBehavior(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	for _, q := range []string{
		"CREATE TABLE t(v BYTEA,s BYTEA)",
		`INSERT INTO t VALUES('\x00c3a9ff00414200','\xa9ff')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(q, err)
		}
	}
	cases := []struct {
		query string
		want  []any
		types []string
	}{
		{`SELECT substring(v,2,1),substr(v,3,1),pg_catalog.position(v,s) FROM t`, []any{[]byte{0xc3}, []byte{0xa9}, int64(3)}, []string{"BYTEA", "BYTEA", "INT4"}},
		{`SELECT substring(v FROM 2 FOR 1),substring(v FOR 1 FROM 3),position(s IN v) FROM t`, []any{[]byte{0xc3}, []byte{0xa9}, int64(3)}, []string{"BYTEA", "BYTEA", "INT4"}},
		{`SELECT overlay('abcdef'::bytea,'XY'::bytea,3,-2),overlay('abcdef'::bytea PLACING 'XY'::bytea FROM 3),substring('abcdef'::bytea FOR 3)`, []any{[]byte("abXYabcdef"), []byte("abXYef"), []byte("abc")}, []string{"BYTEA", "BYTEA", "BYTEA"}},
		{`SELECT trim('\xff8000'::bytea FROM '\x00ff8041008042ff00'::bytea),trim(LEADING '\xff8000'::bytea FROM '\x00ff8041008042ff00'::bytea),trim(TRAILING '\x00ff8041008042ff00'::bytea,'\xff8000'::bytea)`, []any{[]byte{65, 0, 128, 66}, []byte{65, 0, 128, 66, 255, 0}, []byte{0, 255, 128, 65, 0, 128, 66}}, []string{"BYTEA", "BYTEA", "BYTEA"}},
		{`SELECT pg_catalog.position(''::bytea,''::bytea),pg_catalog.position('abc'::bytea,'z'::bytea),substr('abcdef'::bytea,0,3),substring('abcdef'::bytea,-2,4)`, []any{int64(1), int64(0), []byte("ab"), []byte("a")}, []string{"INT4", "INT4", "BYTEA", "BYTEA"}},
		{`SELECT overlay(NULL::bytea,'XY'::bytea,3,2),substring('abc'::bytea,1,NULL::integer),pg_catalog.position('abc'::bytea,NULL::bytea),trim(NULL::bytea FROM 'abc'::bytea)`, []any{nil, nil, nil, nil}, []string{"BYTEA", "BYTEA", "INT4", "BYTEA"}},
	}
	for _, tc := range cases {
		t.Run(tc.query, func(t *testing.T) {
			check := func(rows *sql.Rows) {
				t.Helper()
				defer rows.Close()
				types, err := rows.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
				if len(types) != len(tc.types) {
					t.Fatalf("metadata columns %d", len(types))
				}
				for i, typ := range types {
					scan := reflect.TypeOf([]byte{})
					if tc.types[i] == "INT4" {
						scan = reflect.TypeOf(int64(0))
					}
					if typ.DatabaseTypeName() != tc.types[i] || typ.ScanType() != scan {
						t.Fatalf("column %d: %s/%v", i, typ.DatabaseTypeName(), typ.ScanType())
					}
				}
				if !rows.Next() {
					t.Fatal("missing row", rows.Err())
				}
				got := make([]any, len(tc.want))
				dest := make([]any, len(got))
				for i := range got {
					dest[i] = &got[i]
				}
				if err := rows.Scan(dest...); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(got, tc.want) {
					t.Fatalf("got %#v, want %#v", got, tc.want)
				}
				if rows.Next() || rows.Err() != nil {
					t.Fatal("trailing row/error", rows.Err())
				}
			}
			rows, err := db.Query(tc.query)
			if err != nil {
				t.Fatal("direct", err)
			}
			check(rows)
			stmt, err := db.Prepare(tc.query)
			if err != nil {
				t.Fatal("prepare", err)
			}
			defer stmt.Close()
			rows, err = stmt.Query()
			if err != nil {
				t.Fatal("prepared", err)
			}
			check(rows)
		})
	}
}
