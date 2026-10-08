package stuntgres_test

import (
	"stuntgres"
	"testing"
)

func TestBitwiseContextualInputWidths(t *testing.T) {
	_, db := arithmeticDB(t)
	arithmeticExec(t, db, "CREATE TABLE one(v INTEGER)", 0)
	arithmeticExec(t, db, "INSERT INTO one VALUES(1)", 1)
	for _, tc := range []struct{ q, arg, code, message string }{
		{"SELECT 1::smallint & $1 FROM one", "bad", "22P02", "invalid input syntax for type smallint: \"bad\""},
		{"SELECT 1::smallint & $1 FROM one", "32768", "22003", "value \"32768\" is out of range for type smallint"},
		{"SELECT 1::integer & $1 FROM one", "bad", "22P02", "invalid input syntax for type integer: \"bad\""},
		{"SELECT 1::integer & $1 FROM one", "2147483648", "22003", "value \"2147483648\" is out of range for type integer"},
		{"SELECT 1::bigint & $1 FROM one", "bad", "22P02", "invalid input syntax for type bigint: \"bad\""},
		{"SELECT 1::bigint & $1 FROM one", "9223372036854775808", "22003", "value \"9223372036854775808\" is out of range for type bigint"},
		{"SELECT 1::smallint << $1 FROM one", "bad", "22P02", "invalid input syntax for type integer: \"bad\""},
		{"SELECT 1::bigint >> $1 FROM one", "2147483648", "22003", "value \"2147483648\" is out of range for type integer"},
	} {
		stmt, err := db.Prepare(tc.q)
		if err != nil {
			t.Fatal("must prepare before deferred decoding", err)
		}
		rows, err := stmt.Query(tc.arg)
		if rows != nil {
			rows.Close()
		}
		stmt.Close()
		arithmeticError(t, err, stuntgres.Error{Code: tc.code, Message: tc.message,Context:"unnamed portal parameter $1 = '...'"})
		rows, err = db.Query(tc.q, tc.arg)
		if rows != nil {
			rows.Close()
		}
		arithmeticError(t, err, stuntgres.Error{Code: tc.code, Message: tc.message,Context:"unnamed portal parameter $1 = '...'"})
	}
	for _, tc := range []struct{ typ, meta string }{{"smallint", "INT2"}, {"integer", "INT4"}, {"bigint", "INT8"}} {
		arithmeticRows(t, db, "SELECT 1::"+tc.typ+" & '3' FROM one", []string{tc.meta}, [][]any{{int64(1)}})
	}
}
