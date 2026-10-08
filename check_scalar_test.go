package stuntgres_test

import (
	"bytes"
	"context"
	"stuntgres"
	"testing"
)

func TestCheckScalarPersistence(t *testing.T) {
	for _, tc := range []struct{ column, expr, good, bad string }{
		{"a INT", "a=1", "1", "2"},
		{"a BOOL", "a", "TRUE", "FALSE"},
		{"a INT", "a IS NOT NULL", "1", "NULL"},
		{"a INT", "(a=1 OR a IS NULL) AND NOT(a=2)", "1", "2"},
		{"a INT", "a IS DISTINCT FROM 2", "1", "2"},
		{"a INT", "a IS NOT DISTINCT FROM 1", "1", "2"},
		{"a INT", "a BETWEEN 1 AND 3", "3", "4"},
		{"a INT", "CASE WHEN a IS NULL THEN TRUE ELSE a=1 END", "1", "2"},
		{"a INT", "COALESCE(a,1)=1", "NULL", "2"},
		{"a INT", "NULLIF(a,1) IS NULL", "1", "2"},
		{"a INT", "CAST(a AS BIGINT)+1=2", "1", "2"},
		{"a INT", "abs(a)=1", "-1", "2"},
		{"a INT", "num_nonnulls(a)=1", "1", "NULL"},
		{"a INT", "num_nulls(a)=0", "1", "NULL"},
		{"a TEXT", "current_schemas(false) IS NOT NULL AND a='yes'", "'yes'", "'no'"},
		{"a INT", "current_schemas(false) IS NOT NULL AND a=1", "1", "2"},
		{"a DATE", "a=DATE '2024-01-02'", "'2024-01-02'", "'2024-01-03'"},
		{"a INT", "(a & 1)=1", "1", "2"},
	} {
		t.Run(tc.expr, func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), t.Name())
			execSQL(t, db, "CREATE TABLE t("+tc.column+",CONSTRAINT c CHECK("+tc.expr+"))")
			execSQL(t, db, "INSERT INTO t VALUES("+tc.good+")")
			var dump bytes.Buffer
			if err := stuntgres.Dump(context.Background(), db, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(context.Background(), db, &dump); err != nil {
				t.Fatal(err)
			}
			detail := tc.bad
			if detail == "NULL" {
				detail = "null"
			} else if detail == "FALSE" {
				detail = "f"
			} else if len(detail) > 1 && detail[0] == '\'' {
				detail = detail[1 : len(detail)-1]
			}
			checkExecError(t, db, false, "INSERT INTO t VALUES("+tc.bad+")", checkViolation("t", "c", detail))
		})
	}
	for _, expr := range []string{"NULL", "TRUE", "'true'"} {
		t.Run(expr, func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), t.Name())
			execSQL(t, db, "CREATE TABLE t(a INT CHECK("+expr+"))")
			execSQL(t, db, "INSERT INTO t VALUES(NULL),(1),(2)")
		})
	}
}
func TestCheckVolatileAndProviderErrors(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	db.SetMaxOpenConns(1)
	execSQL(t, db, "CREATE SEQUENCE s START 1")
	execSQL(t, db, "CREATE TABLE t(a INT CHECK(nextval('s')>0),CHECK(random()>=0 AND random()<1))")
	var dump bytes.Buffer
	if err := stuntgres.Dump(context.Background(), db, &dump); err != nil {
		t.Fatal(err)
	}
	execSQL(t, db, "INSERT INTO t VALUES(1),(2)")
	wantTuples(t, db, "SELECT currval('s')", [][]any{{int64(2)}})
	execSQL(t, db, "CREATE TABLE u(a INT CHECK(10/a>0))")
	execSQL(t, db, "INSERT INTO u VALUES(1)")
	_, err := db.Exec("INSERT INTO u VALUES(2),(0)")
	sqlState(t, err, "22012")
	wantTuples(t, db, "SELECT a FROM u", [][]any{{int64(1)}})
}
