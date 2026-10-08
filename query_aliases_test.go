package stuntgres_test

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"

	"stuntgres"
)

func queryAliasesPublicFixture(t *testing.T) *sql.DB {
	t.Helper()
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(id INT NOT NULL,a INT,b INT)")
	return db
}

func TestQueryAliasesResolution(t *testing.T) {
	all := [][]any{{int64(1), int64(2), int64(1)}, {int64(2), int64(1), int64(5)}, {int64(3), nil, int64(2)}, {int64(4), int64(2), int64(0)}}
	cases := []struct {
		query        string
		names, types []string
		want         [][]any
	}{
		{"SELECT x.iid AS shown,x.a,x.b FROM t AS x(iid) ORDER BY shown", []string{"shown", "a", "b"}, []string{"INT4", "INT4", "INT4"}, all},
		{"SELECT x.iid AS shown,x.a,x.b FROM t x(iid) ORDER BY shown", []string{"shown", "a", "b"}, []string{"INT4", "INT4", "INT4"}, all},
		{"SELECT x.* FROM t x(iid) ORDER BY x.iid", []string{"iid", "a", "b"}, []string{"INT4", "INT4", "INT4"}, all},
		{"SELECT iid,a,b FROM t x(iid) ORDER BY iid", []string{"iid", "a", "b"}, []string{"INT4", "INT4", "INT4"}, all},
		{`SELECT "Mixed"."Key" AS "Shown" FROM t AS "Mixed"("Key") ORDER BY "Shown"`, []string{"Shown"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {int64(4)}}},
		{"SELECT x.iid,x.iid+1 FROM t x(iid) ORDER BY x.iid", []string{"iid", "?column?"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}, {int64(2), int64(3)}, {int64(3), int64(4)}, {int64(4), int64(5)}}},
		{"SELECT a AS b,id FROM t WHERE b=1 ORDER BY b", []string{"b", "id"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(1)}}},
		{"SELECT a AS b,id FROM t ORDER BY b,id", []string{"b", "id"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}, {int64(2), int64(1)}, {int64(2), int64(4)}, {nil, int64(3)}}},
		{"SELECT a AS b,id FROM t ORDER BY t.b,id", []string{"b", "id"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(4)}, {int64(2), int64(1)}, {nil, int64(3)}, {int64(1), int64(2)}}},
		{"SELECT a AS b,id FROM t ORDER BY b+0,id", []string{"b", "id"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(4)}, {int64(2), int64(1)}, {nil, int64(3)}, {int64(1), int64(2)}}},
		{"SELECT a AS b,b FROM t ORDER BY id", []string{"b", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(1)}, {int64(1), int64(5)}, {nil, int64(2)}, {int64(2), int64(0)}}},
		{"SELECT a AS x,a AS x FROM t ORDER BY x,id", []string{"x", "x"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(2)}, {int64(2), int64(2)}, {nil, nil}}},
	}
	for _, population := range []string{"empty", "populated"} {
		t.Run(population, func(t *testing.T) {
			db := queryAliasesPublicFixture(t)
			if population == "populated" {
				execSQL(t, db, "INSERT INTO t VALUES(1,2,1),(2,1,5),(3,NULL,2),(4,2,0)")
			}
			for _, tc := range cases {
				t.Run(tc.query, func(t *testing.T) {
					want := tc.want
					if population == "empty" {
						want = [][]any{}
					}
					rows, err := db.Query(tc.query)
					queryCoreRows(t, rows, err, tc.names, tc.types, want, false)
					stmt, err := db.Prepare(tc.query)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					rows, err = stmt.Query()
					queryCoreRows(t, rows, err, tc.names, tc.types, want, false)
				})
			}
		})
	}
}

func TestQueryAliasesMetadataAndPreparedFreshness(t *testing.T) {
	db := queryAliasesPublicFixture(t)
	execSQL(t, db, "INSERT INTO t VALUES(1,2,1),(2,1,5),(3,NULL,2),(4,2,0)")
	stmt, err := db.Prepare("SELECT x.iid+$1 AS sum FROM t x(iid) WHERE x.iid=$2 ORDER BY sum")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	for _, tc := range []struct {
		args []any
		want [][]any
	}{
		{[]any{10, 1}, [][]any{{int64(11)}}},
		{[]any{20, 2}, [][]any{{int64(22)}}},
		{[]any{nil, 2}, [][]any{{nil}}},
		{[]any{10, nil}, [][]any{}},
	} {
		rows, err := stmt.Query(tc.args...)
		queryCoreRows(t, rows, err, []string{"sum"}, []string{"INT4"}, tc.want, false)
	}
	for _, args := range [][]any{{10}, {struct{}{}, 1}} {
		rows, err := stmt.Query(args...)
		if rows != nil {
			rows.Close()
			t.Fatal("invalid arguments exposed rows")
		}
		var diagnostic *stuntgres.Error
		if err == nil || errors.As(err, &diagnostic) {
			t.Fatal("argument failure must be ordinary", err)
		}
	}
	for _, q := range []string{"SELECT x.iid AS shown,x.a,x.iid+1 AS computed FROM t x(iid)", "SELECT x.iid AS shown,x.a,x.iid+1 AS computed FROM t x(iid) WHERE FALSE"} {
		rows, err := db.Query(q)
		if err != nil {
			t.Fatal(err)
		}
		names, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		names[0] = "mutated"
		next, err := rows.Columns()
		if err != nil || !reflect.DeepEqual(next, []string{"shown", "a", "computed"}) {
			t.Fatal(next, err)
		}
		metadata, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		if nullable, known := metadata[0].Nullable(); nullable || !known {
			t.Fatal("physical id lost NOT NULL")
		}
		if nullable, known := metadata[1].Nullable(); !nullable || !known {
			t.Fatal("physical a lost nullable contract")
		}
		if _, known := metadata[2].Nullable(); known {
			t.Fatal("computed alias invented nullability")
		}
		rows.Close()
	}
	execSQL(t, db, "CREATE TABLE typed(s VARCHAR(5),n NUMERIC(7,2))")
	execSQL(t, db, "INSERT INTO typed VALUES('hello',12.50)")
	for _, q := range []string{"SELECT x.word AS label,x.amount AS price FROM typed x(word,amount)", "SELECT x.word AS label,x.amount AS price FROM typed x(word,amount) WHERE FALSE"} {
		for _, prepared := range []bool{false, true} {
			var rows *sql.Rows
			var stmt *sql.Stmt
			if prepared {
				stmt, err = db.Prepare(q)
				if err != nil {
					t.Fatal(err)
				}
				rows, err = stmt.Query()
			} else {
				rows, err = db.Query(q)
			}
			if err != nil {
				t.Fatal(err)
			}
			metadata, err := rows.ColumnTypes()
			if err != nil || len(metadata) != 2 {
				t.Fatal(metadata, err)
			}
			if length, ok := metadata[0].Length(); !ok || length != 5 {
				t.Fatal("alias lost VARCHAR length", length, ok)
			}
			if precision, scale, ok := metadata[1].DecimalSize(); !ok || precision != 7 || scale != 2 {
				t.Fatal("alias lost NUMERIC precision/scale", precision, scale, ok)
			}
			want := [][]any{{"hello", "12.50"}}
			if q[len(q)-11:] == "WHERE FALSE" {
				want = [][]any{}
			}
			queryCoreRows(t, rows, nil, []string{"label", "price"}, []string{"VARCHAR", "NUMERIC"}, want, false)
			if stmt != nil {
				stmt.Close()
			}
		}
	}
}

func TestQueryAliasesFailureAndCancellation(t *testing.T) {
	db := queryAliasesPublicFixture(t)
	execSQL(t, db, "INSERT INTO t VALUES(1,2,1),(2,1,5),(3,NULL,2),(4,2,0)")
	q := "SELECT 1/(x.iid-2) AS quotient FROM t x(iid) ORDER BY x.iid"
	stmt, err := db.Prepare(q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	for _, prepared := range []bool{false, true} {
		var rows *sql.Rows
		if prepared {
			rows, err = stmt.Query()
		} else {
			rows, err = db.Query(q)
		}
		if rows != nil {
			rows.Close()
			t.Fatal("later division failure exposed partial rows")
		}
		var got *stuntgres.Error
		want := stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22012", Message: "division by zero"}
		if !errors.As(err, &got) || *got != want {
			t.Fatal(got, err, want)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, prepared := range []bool{false, true} {
		var rows *sql.Rows
		if prepared {
			rows, err = stmt.QueryContext(ctx)
		} else {
			rows, err = db.QueryContext(ctx, q)
		}
		if rows != nil {
			rows.Close()
			t.Fatal("canceled query exposed rows")
		}
		var got *stuntgres.Error
		if !errors.Is(err, context.Canceled) || errors.As(err, &got) {
			t.Fatal("pre-canceled context must remain ordinary", err)
		}
	}
}
