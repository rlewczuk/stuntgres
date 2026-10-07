package stuntgres_test

import (
	"stuntgres"
	"testing"
)

func TestBitwiseParameterAmbiguityAndAdmission(t *testing.T) {
	_, db := arithmeticDB(t)
	arithmeticExec(t, db, "CREATE TABLE t(s SMALLINT)", 0)
	arithmeticExec(t, db, "INSERT INTO t VALUES(1)", 1)
	for _, tc := range []struct {
		q, message, pos string
		args            []any
	}{
		{"SELECT ~$1 FROM t", "operator is not unique: ~ unknown", "8", []any{1}},
		{"SELECT $1 & $2 FROM t", "operator is not unique: unknown & unknown", "11", []any{1, 1}},
		{"SELECT $1 << 1::smallint FROM t", "operator is not unique: unknown << smallint", "11", []any{1}},
		{"SELECT $1 >> 1::smallint FROM t", "operator is not unique: unknown >> smallint", "11", []any{1}},
	} {
		want := stuntgres.Error{Code: "42725", Message: tc.message, Position: tc.pos, Hint: "Could not choose a best candidate operator. You might need to add explicit type casts."}
		rows, err := db.Query(tc.q, tc.args...)
		if rows != nil {
			rows.Close()
		}
		arithmeticError(t, err, want)
		s, err := db.Prepare(tc.q)
		if s != nil {
			s.Close()
		}
		arithmeticError(t, err, want)
	}
	arithmeticRows(t, db, "SELECT s << 1::smallint,s >> 1::smallint FROM t", []string{"INT2", "INT2"}, [][]any{{int64(2), int64(0)}})
	rows, err := db.Query("SELECT num_nulls(s & 1) FROM t")
	if rows != nil {
		rows.Close()
	}
	arithmeticError(t, err, stuntgres.Error{Code: "0A000", Message: "arithmetic in null-count arguments is unsupported", Position: "20"})
	rows, err = db.Query("SELECT num_nulls(~1)")
	if rows != nil {
		rows.Close()
	}
	arithmeticError(t, err, stuntgres.Error{Code: "0A000", Message: "SELECT without FROM is unsupported", Position: "1"})
}

func TestBitwiseScopedSmallintCasts(t *testing.T) {
	_, db := arithmeticDB(t)
	arithmeticExec(t, db, "CREATE TABLE one(v INTEGER,s SMALLINT)", 0)
	arithmeticExec(t, db, "INSERT INTO one VALUES(1,1)", 1)
	for _, tc := range []struct {
		query, position string
	}{
		{"SELECT 1::smallint FROM one", "9"},
		{"SELECT CAST(1 AS smallint) FROM one", "8"},
		{"SELECT ~1::smallint,1::smallint FROM one", "22"},
		{"SELECT COALESCE(1 & 1,2::smallint) FROM one", "24"},
		{"SELECT CAST('1'::text AS smallint) & 1 FROM one", "8"},
	} {
		rows, err := db.Query(tc.query)
		if rows != nil {
			rows.Close()
		}
		arithmeticError(t, err, stuntgres.Error{Code: "0A000", Message: "explicit casts are supported only for int4, int8, text and bool", Position: tc.position})
		stmt, err := db.Prepare(tc.query)
		if stmt != nil {
			stmt.Close()
		}
		arithmeticError(t, err, stuntgres.Error{Code: "0A000", Message: "explicit casts are supported only for int4, int8, text and bool", Position: tc.position})
	}
	arithmeticRows(t, db, "SELECT ~CAST(1 AS smallint),CAST(32767 AS smallint) << CAST(1 AS smallint),CAST(32767::smallint << 1 AS bigint),~NULL::smallint FROM one", []string{"INT2", "INT2", "INT8", "INT2"}, [][]any{{int64(-2), int64(-2), int64(-2), nil}})
	arithmeticExec(t, db, "CREATE TABLE scoped(v BIGINT DEFAULT CAST(32767::smallint << 1 AS bigint))", 0)
	arithmeticExec(t, db, "INSERT INTO scoped DEFAULT VALUES", 1)
	arithmeticRows(t, db, "SELECT v FROM scoped", []string{"INT8"}, [][]any{{int64(-2)}})
}
