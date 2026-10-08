package stuntgres_test

import (
	"strconv"
	"strings"
	"stuntgres"
	"testing"
)

func TestInsertSelectValuesDiagnostics(t *testing.T) {
	for _, tc := range []struct{ q, code, message, token string }{
		{"INSERT INTO t VALUES(1),(TRUE) ORDER BY 1", "42804", "VALUES types integer and boolean cannot be matched", "TRUE"},
		{"INSERT INTO t VALUES(TRUE),(1) ORDER BY 1", "42804", "VALUES types boolean and integer cannot be matched", "1)"},
		{"INSERT INTO t VALUES(TIME '00:00'),(TIMESTAMP 'epoch') ORDER BY 1", "42846", "VALUES could not convert type timestamp without time zone to time without time zone", "'epoch'"},
		{"INSERT INTO t VALUES(TIMESTAMP 'epoch'),(TIME '00:00') ORDER BY 1", "42846", "VALUES could not convert type time without time zone to timestamp without time zone", "'00:00'"},
		{"INSERT INTO t VALUES(1),(2,3) ORDER BY 1", "42601", "VALUES lists must all be the same length", "2,3"},
		{"INSERT INTO t VALUES(1) ORDER BY 0", "42P10", "ORDER BY position 0 is not in select list", "0"},
		{"INSERT INTO t VALUES(1) ORDER BY 2", "42P10", "ORDER BY position 2 is not in select list", "2"},
		{"INSERT INTO t VALUES(1) ORDER BY 'x'", "42601", "non-integer constant in ORDER BY", "'x'"},
		{"INSERT INTO t VALUES(1) ORDER BY TRUE", "42601", "non-integer constant in ORDER BY", "TRUE"},
		{"INSERT INTO t VALUES(1) ORDER BY NULL", "42601", "non-integer constant in ORDER BY", "NULL"},
		{"INSERT INTO t VALUES(1) ORDER BY 1.5", "42601", "non-integer constant in ORDER BY", "1.5"},
		{`INSERT INTO t VALUES(1) ORDER BY "1"`, "42703", `column "1" does not exist`, `"1"`},
		{"INSERT INTO t(i,b) SELECT $1,$1", "42P08", "inconsistent types deduced for parameter $1", "$1"},
	} {
		t.Run(tc.q, func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), "insert-select-values-"+tc.q)
			execSQL(t, db, "CREATE TABLE t(i INT,b BOOL)")
			execSQL(t, db, "INSERT INTO t VALUES(9,TRUE)")
			index := strings.Index(tc.q, tc.token)
			if tc.code == "42P08" {
				index = strings.LastIndex(tc.q, tc.token)
			}
			want := stuntgres.Error{Code: tc.code, Message: tc.message, Position: strconv.Itoa(index + 1)}
			if tc.code == "42P08" {
				want.Detail = "integer versus boolean"
			}
			_, err := db.Exec(tc.q)
			typeCoreDiagnostic(t, err, want)
			s, err := db.Prepare(tc.q)
			if s != nil {
				s.Close()
				t.Fatal("failed prepare exposed handle")
			}
			typeCoreDiagnostic(t, err, want)
			wantTuples(t, db, "SELECT * FROM t", [][]any{{int64(9), true}})
		})
	}
}

func TestInsertSelectParameterOccurrences(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), "insert-select-parameter-occurrences")
	execSQL(t, db, "CREATE TABLE s(i INT,b BOOL)")
	execSQL(t, db, "INSERT INTO s VALUES(1,TRUE),(2,FALSE)")
	execSQL(t, db, "CREATE TABLE t(i BIGINT,n NUMERIC(3,1),m NUMERIC(5,2))")
	stmt, err := db.Prepare("INSERT INTO t(i) SELECT i FROM s WHERE i=$1")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	for _, arg := range []any{int64(1), int64(2)} {
		result, err := stmt.Exec(arg)
		insertSelectCount(t, result, err, 1)
	}
	_, err = stmt.Exec("bad")
	typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`,Context:"unnamed portal parameter $1 = '...'"})
	_, err = stmt.Exec()
	if err == nil {
		t.Fatal("missing Go argument accepted")
	}
	if _, ok := err.(interface{ SQLState() string }); ok {
		t.Fatal("ordinary argument error gained SQLSTATE", err)
	}
	_, err = db.Exec("INSERT INTO t(i) SELECT $1 FROM s WHERE i=$1", int64(1))
	typeCoreDiagnostic(t, err, stuntgres.Error{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "integer versus bigint", Position: "25"})
	// Per-target sizing does not change the shared unconstrained input type.
	stmt2, err := db.Prepare("INSERT INTO t(n,m) SELECT $1,$1")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt2.Close()
	result, err := stmt2.Exec("1.234")
	insertSelectCount(t, result, err, 1)
	rows, err := db.Query("SELECT n,m FROM t WHERE i=1")
	queryCoreRows(t, rows, err, []string{"n", "m"}, []string{"NUMERIC", "NUMERIC"}, [][]any{{nil, nil}}, false)
	rows, err = db.Query("SELECT n,m FROM t ORDER BY n")
	queryCoreRows(t, rows, err, []string{"n", "m"}, []string{"NUMERIC", "NUMERIC"}, [][]any{{"1.2", "1.23"}, {nil, nil}, {nil, nil}}, false)
}
