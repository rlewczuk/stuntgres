package smoke_test

import (
	"bytes"
	"context"
	"database/sql"
	"reflect"
	"stuntgres"
	"testing"
)

func TestBooleanAggregateSmoke(t *testing.T) {
	connector, err := stuntgres.NewDriver().OpenConnector(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(connector)
	defer db.Close()
	exec := func(q string, want int64) {
		t.Helper()
		r, err := db.Exec(q)
		if err != nil {
			t.Fatal(q, err)
		}
		n, err := r.RowsAffected()
		if err != nil || n != want {
			t.Fatal(q, n, err)
		}
	}
	exec("CREATE TABLE t(k TEXT,b BOOL)", 0)
	exec("INSERT INTO t VALUES('T',TRUE),('T',TRUE),('T',NULL),('F',FALSE),('F',FALSE),('F',NULL),('M',TRUE),('M',FALSE),('M',NULL),('N',NULL),('N',NULL)", 11)
	exec("CREATE TABLE empty(k TEXT,b BOOL)", 0)
	exec("CREATE TABLE m(b BOOL)", 0)
	exec("INSERT INTO m VALUES(TRUE),(FALSE),(NULL)", 3)
	check := func(q string, args []any, names, types []string, want [][]any) {
		t.Helper()
		for _, prepared := range []bool{false, true} {
			var rows *sql.Rows
			var err error
			if prepared {
				stmt, e := db.Prepare(q)
				if e != nil {
					t.Fatal(q, e)
				}
				rows, err = stmt.Query(args...)
				stmt.Close()
			} else {
				rows, err = db.Query(q, args...)
			}
			if err == nil && rows != nil {
				metadata, e := rows.ColumnTypes()
				if e != nil {
					t.Fatal(e)
				}
				for i, c := range metadata {
					if types[i] == "BOOL" && (names[i] == "bool_and" || names[i] == "bool_or" || names[i] == "every") {
						nullable, known := c.Nullable()
						if !nullable || !known || c.ScanType() != reflect.TypeOf(false) {
							t.Fatal(c, nullable, known)
						}
					}
				}
			}
			aggregateSmokeRows(t, rows, err, names, types, want)
		}
	}
	labels := []string{"bool_and", "bool_or", "every"}
	types := []string{"BOOL", "BOOL", "BOOL"}
	for _, tc := range []struct {
		k   string
		row []any
	}{{"T", []any{true, true, true}}, {"F", []any{false, false, false}}, {"M", []any{false, true, false}}, {"N", []any{nil, nil, nil}}} {
		check("SELECT bool_and(b),bool_or(b),every(b) FROM t WHERE k='"+tc.k+"'", nil, labels, types, [][]any{tc.row})
	}
	for _, q := range []string{"SELECT bool_and(b),bool_or(b),every(b) FROM empty", "SELECT bool_and(b),bool_or(b),every(b) FROM t WHERE FALSE", "SELECT bool_and(b),bool_or(b),every(b) FROM t WHERE NULL"} {
		check(q, nil, labels, types, [][]any{{nil, nil, nil}})
	}
	check("SELECT k,bool_and(b),bool_or(b),every(b) FROM t GROUP BY k", nil, []string{"k", "bool_and", "bool_or", "every"}, []string{"TEXT", "BOOL", "BOOL", "BOOL"}, [][]any{{"T", true, true, true}, {"F", false, false, false}, {"M", false, true, false}, {"N", nil, nil, nil}})
	check("SELECT k,bool_and(b),bool_or(b),every(b) FROM empty GROUP BY k", nil, []string{"k", "bool_and", "bool_or", "every"}, []string{"TEXT", "BOOL", "BOOL", "BOOL"}, nil)
	check("SELECT count(b),bool_and(b),bool_or(b),every(b),bool_and(b) FROM m", nil, []string{"count", "bool_and", "bool_or", "every", "bool_and"}, []string{"INT8", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{int64(2), false, true, false, false}})
	check("SELECT bool_and('true'),bool_or(NULL) FROM m", nil, labels[:2], types[:2], [][]any{{true, nil}})
	for _, arg := range []any{true, false, nil} {
		check("SELECT every($1) FROM m", []any{arg}, []string{"every"}, []string{"BOOL"}, [][]any{{arg}})
	}
	check("SELECT k FROM t GROUP BY k HAVING bool_or(b)", nil, []string{"k"}, []string{"TEXT"}, [][]any{{"T"}, {"M"}})
	check("SELECT k FROM t GROUP BY k HAVING every(b)", nil, []string{"k"}, []string{"TEXT"}, [][]any{{"T"}})
	check("SELECT COALESCE(bool_and(b),FALSE) AS answer FROM empty", nil, []string{"answer"}, []string{"BOOL"}, [][]any{{false}})
	check("SELECT pg_catalog.bool_and(b),\"bool_or\"(b),every(b IS NULL) FROM m", nil, labels, types, [][]any{{false, true, false}})
	exec("CREATE TABLE out(k TEXT,b BOOL)", 0)
	exec("INSERT INTO out SELECT k,bool_and(b) FROM t GROUP BY k", 4)
	exec("INSERT INTO out(b) SELECT every(b) FROM empty", 1)
	exec("INSERT INTO out(b) SELECT bool_or(b) FROM empty GROUP BY k", 0)
	exec("INSERT INTO out(b) SELECT bool_or(b) FROM m", 1)
	exec("CREATE TABLE d(v INT)", 0)
	exec("INSERT INTO d VALUES(1),(0)", 2)
	for _, q := range []string{"SELECT bool_and(10/v=0) FROM d", "SELECT bool_or(10/v>0) FROM d", "INSERT INTO out(b) SELECT bool_and(10/v=0) FROM d"} {
		for _, prepared := range []bool{false, true} {
			var err error
			if prepared {
				stmt, e := db.Prepare(q)
				if e != nil {
					t.Fatal(e)
				}
				_, err = stmt.Exec()
				stmt.Close()
			} else {
				_, err = db.Exec(q)
			}
			if e := typeCoreDiagnostic(err, stuntgres.Error{Code: "22012", Message: "division by zero"}); e != nil {
				t.Fatal(e)
			}
		}
	}
	check("SELECT k,b FROM out", nil, []string{"k", "b"}, []string{"TEXT", "BOOL"}, [][]any{{"T", true}, {"F", false}, {"M", false}, {"N", nil}, {nil, nil}, {nil, true}})
	const hint = "No function matches the given name and argument types. You might need to add explicit type casts."
	for _, name := range labels {
		for _, tc := range []struct{ q, signature, pos string }{{"SELECT " + name + "(1) FROM t", name + "(integer)", "8"}, {"SELECT " + name + "('true'::text) FROM t", name + "(text)", "8"}, {"SELECT /*é*/ " + name + "(1) FROM empty WHERE FALSE", name + "(integer)", "14"}} {
			want := stuntgres.Error{Code: "42883", Message: "function " + tc.signature + " does not exist", Hint: hint, Position: tc.pos}
			rows, err := db.Query(tc.q)
			if rows != nil {
				rows.Close()
				t.Fatal("error exposed rows")
			}
			if e := typeCoreDiagnostic(err, want); e != nil {
				t.Fatal(e)
			}
			stmt, err := db.Prepare(tc.q)
			if stmt != nil {
				stmt.Close()
				t.Fatal("error exposed statement")
			}
			if e := typeCoreDiagnostic(err, want); e != nil {
				t.Fatal(e)
			}
		}
	}
	exec("CREATE VIEW aggregated(a,o,e,u) AS SELECT bool_and(b),bool_or(b),every(b),every('true') FROM m", 0)
	stmt, err := db.Prepare("SELECT bool_and(b) FROM t WHERE k='T'")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held, err := stmt.Query()
	if err != nil {
		t.Fatal(err)
	}
	exec("INSERT INTO t VALUES('T',FALSE)", 1)
	rows, err := stmt.Query()
	aggregateSmokeRows(t, rows, err, labels[:1], types[:1], [][]any{{false}})
	check("SELECT a,o,e,u FROM aggregated", nil, []string{"a", "o", "e", "u"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{false, true, false, true}})
	var dump bytes.Buffer
	if err := stuntgres.Dump(context.Background(), db, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(context.Background(), db, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	aggregateSmokeRows(t, held, nil, labels[:1], types[:1], [][]any{{true}})
	check("SELECT a,o,e,u FROM aggregated", nil, []string{"a", "o", "e", "u"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{false, true, false, true}})
}
