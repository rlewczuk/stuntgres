package stuntgres_test

import (
	"database/sql"
	"reflect"
	"stuntgres"
	"testing"
)

func TestLikeBaselineBehavior(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(v TEXT,c CHAR(3),b BYTEA)")
	execSQL(t, db, "INSERT INTO t VALUES('AbC','a','\\xc3a9')")
	for _, tc := range []struct {
		query string
		want  []any
	}{
		{"SELECT v LIKE 'AbC',v LIKE 'b',v ILIKE 'abc',v NOT ILIKE 'abc' FROM t", []any{true, false, true, false}},
		{"SELECT v ~~ 'A%',v !~~ 'A%',v ~~* 'a%',v !~~* 'a%' FROM t", []any{true, false, true, false}},
		{"SELECT c LIKE 'a',c LIKE 'a__',CAST(c AS TEXT) LIKE 'a' FROM t", []any{false, true, true}},
		{"SELECT b LIKE '\\x5f',b LIKE '\\x5f5f',NULL LIKE 'x',v LIKE NULL FROM t", []any{false, true, nil, nil}},
		{"SELECT 'a%b' LIKE 'a!%b' ESCAPE '!','ab' ILIKE 'a_' ESCAPE 'A','É' ILIKE 'é' FROM t", []any{true, true, false}},
	} {
		t.Run(tc.query, func(t *testing.T) {
			rows, err := db.Query(tc.query)
			if err != nil {
				t.Fatal(err)
			}
			likeAssertRow(t, rows, tc.want)
			stmt, err := db.Prepare(tc.query)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			rows, err = stmt.Query()
			if err != nil {
				t.Fatal(err)
			}
			likeAssertRow(t, rows, tc.want)
		})
	}
}

func likeAssertRow(t *testing.T, rows *sql.Rows, want []any) {
	t.Helper()
	defer rows.Close()
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != len(want) {
		t.Fatal("column metadata", types, err)
	}
	for _, typ := range types {
		if typ.DatabaseTypeName() != "BOOL" {
			t.Fatal("matching result type", typ.DatabaseTypeName())
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
		t.Fatalf("got %#v want %#v", got, want)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("unexpected row/error", rows.Err())
	}
}
