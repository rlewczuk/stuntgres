package stuntgres_test

import (
	"errors"
	"sync"
	"testing"

	"stuntgres"
)

func TestLogicalOperandDiagnosticsAndBoundaries(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), "logical-boundaries")
	execSQL(t, db, "CREATE TABLE t(id INT,a BOOL,s TEXT,n NUMERIC,tm TIME)")
	for _, tc := range []struct {
		q    string
		want stuntgres.Error
	}{
		{"SELECT 1 AND TRUE FROM t", stuntgres.Error{Code: "42804", Message: "argument of AND must be type boolean, not type integer", Position: "8"}},
		{"SELECT TRUE AND (1) FROM t", stuntgres.Error{Code: "42804", Message: "argument of AND must be type boolean, not type integer", Position: "18"}},
		{"SELECT NOT (s) FROM t", stuntgres.Error{Code: "42804", Message: "argument of NOT must be type boolean, not type text", Position: "13"}},
		{"SELECT FALSE AND missing FROM t", stuntgres.Error{Code: "42703", Message: `column "missing" does not exist`, Position: "18"}},
		{"SELECT TRUE OR n FROM t", stuntgres.Error{Code: "42804", Message: "argument of OR must be type boolean, not type numeric", Position: "16"}},
		{"SELECT NOT tm FROM t", stuntgres.Error{Code: "42804", Message: "argument of NOT must be type boolean, not type time without time zone", Position: "12"}},
		{"SELECT id FROM t WHERE TRUE AND 1", stuntgres.Error{Code: "42804", Message: "argument of AND must be type boolean, not type integer", Position: "33"}},
		{"INSERT INTO t(id) SELECT TRUE AND FALSE FROM t", stuntgres.Error{Code: "42804", Message: `column "id" is of type integer but expression is of type boolean`, Hint: "You will need to rewrite or cast the expression.", Position: "26"}},
		{"SELECT a+1 AND a FROM t", stuntgres.Error{Code: "42883", Message: "operator does not exist: boolean + integer", Position: "9", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."}},
	} {
		rows, err := db.Query(tc.q)
		if rows != nil {
			rows.Close()
			t.Fatal("failure exposed cursor")
		}
		typeCoreDiagnostic(t, err, tc.want)
		stmt, err := db.Prepare(tc.q)
		if stmt != nil {
			stmt.Close()
			t.Fatal("failure exposed handle")
		}
		typeCoreDiagnostic(t, err, tc.want)
	}
	for _, q := range []string{
		"SELECT TRUE AND FALSE", "SELECT id=1=TRUE FROM t",
		"SELECT (id=1)=TRUE FROM t",
		"INSERT INTO t(id) VALUES(TRUE AND FALSE)", "INSERT INTO t(id) VALUES(NOT TRUE)",
		"SELECT current_setting(TRUE AND FALSE)",
		"SELECT a AND a AS result FROM t",
		"SELECT f(a) AND a FROM t", "SELECT a FROM t ORDER BY a AND a",
	} {
		rows, err := db.Query(q)
		if rows != nil {
			rows.Close()
			t.Fatal("excluded query exposed rows", q)
		}
		var diagnostic *stuntgres.Error
		if !errors.As(err, &diagnostic) || (diagnostic.Code != "0A000" && diagnostic.Code != "42601") {
			t.Fatal(q, err)
		}
		stmt, err := db.Prepare(q)
		if stmt != nil {
			stmt.Close()
			t.Fatal("excluded query exposed handle", q)
		}
		if !errors.As(err, &diagnostic) {
			t.Fatal(q, err)
		}
	}
	wantTuples(t, db, "SELECT id FROM t", [][]any{})
	execSQL(t, db, "INSERT INTO t(id,a) VALUES(1,TRUE),(2,FALSE),(3,NULL)")
	compareOpsDirectPrepared(t, db, "SELECT a::BOOL AND a FROM t ORDER BY id", []string{"BOOL"}, [][]any{{true}, {false}, {nil}})
	compareOpsDirectPrepared(t, db, "SELECT t.a AND a FROM t ORDER BY id", []string{"BOOL"}, [][]any{{true}, {false}, {nil}})
	compareOpsDirectPrepared(t, db, "SELECT x.a AND y.a FROM t x JOIN t y USING(id) ORDER BY id", []string{"BOOL"}, [][]any{{true}, {false}, {nil}})
}

func TestLogicalConcurrentPreparedFrames(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), "logical-concurrent")
	execSQL(t, db, "CREATE TABLE t(id INT)")
	execSQL(t, db, "INSERT INTO t VALUES(1)")
	stmt, err := db.Prepare("SELECT $1 AND $1,NOT $1 FROM t")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	var wg sync.WaitGroup
	for i := range 12 {
		wg.Go(func() {
			arg := i%2 == 0
			rows, err := stmt.Query(arg)
			if err != nil {
				t.Error(err)
				return
			}
			defer rows.Close()
			if !rows.Next() {
				t.Error("missing logical row", rows.Err())
				return
			}
			var and, not bool
			if err := rows.Scan(&and, &not); err != nil || and != arg || not == arg {
				t.Error(and, not, err)
			}
			if rows.Next() || rows.Err() != nil {
				t.Error("extra row", rows.Err())
			}
		})
	}
	wg.Wait()
}
