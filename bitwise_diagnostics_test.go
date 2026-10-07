package stuntgres_test

import (
	"stuntgres"
	"testing"
)

func TestBitwiseDiagnostics(t *testing.T) {
	_, db := arithmeticDB(t)
	arithmeticExec(t, db, "CREATE TABLE one(v INTEGER,n NUMERIC)", 0)
	for _, tc := range []struct{ q, code, message, pos, hint string }{
		{"SELECT '1' & '2' FROM one", "42725", "operator is not unique: unknown & unknown", "12", "Could not choose a best candidate operator. You might need to add explicit type casts."},
		{"SELECT ~NULL FROM one", "42725", "operator is not unique: ~ unknown", "8", "Could not choose a best candidate operator. You might need to add explicit type casts."},
		{"SELECT ~'1' FROM one", "42725", "operator is not unique: ~ unknown", "8", "Could not choose a best candidate operator. You might need to add explicit type casts."},
		{"SELECT /*é*/ 1&true FROM one", "42883", "operator does not exist: integer & boolean", "15", "No operator matches the given name and argument types. You might need to add explicit type casts."},
		{"SELECT NULL & NULL FROM one", "42725", "operator is not unique: unknown & unknown", "13", "Could not choose a best candidate operator. You might need to add explicit type casts."},
		{"SELECT ~true FROM one", "42883", "operator does not exist: ~ boolean", "8", "No operator matches the given name and argument type. You might need to add an explicit type cast."},
		{"SELECT ~n FROM one", "42883", "operator does not exist: ~ numeric", "8", "No operator matches the given name and argument type. You might need to add an explicit type cast."},
		{"SELECT ~'1'::text FROM one", "42883", "operator does not exist: ~ text", "8", "No operator matches the given name and argument type. You might need to add an explicit type cast."},
		{"SELECT 1::bigint << 1::bigint FROM one", "42883", "operator does not exist: bigint << bigint", "18", "No operator matches the given name and argument types. You might need to add explicit type casts."},
		{"SELECT 1&-1 FROM one", "42883", "operator does not exist: integer &- integer", "9", "No operator matches the given name and argument types. You might need to add explicit type casts."},
		{"SELECT 1|~1 FROM one", "42883", "operator does not exist: integer |~ integer", "9", "No operator matches the given name and argument types. You might need to add explicit type casts."},
		{"SELECT 1<<~1 FROM one", "42883", "operator does not exist: integer <<~ integer", "9", "No operator matches the given name and argument types. You might need to add explicit type casts."},
		{"SELECT '1' << 1::smallint FROM one", "42725", "operator is not unique: unknown << smallint", "12", "Could not choose a best candidate operator. You might need to add explicit type casts."},
		{"SELECT '1' << 1::bigint FROM one", "42883", "operator does not exist: unknown << bigint", "12", "No operator matches the given name and argument types. You might need to add explicit type casts."},
		{"SELECT '1' >> 1::bigint FROM one", "42883", "operator does not exist: unknown >> bigint", "12", "No operator matches the given name and argument types. You might need to add explicit type casts."},
		{"SELECT NULL::integer & 'bad' FROM one", "22P02", "invalid input syntax for type integer: \"bad\"", "24", ""},
		{"SELECT 1::smallint & '32768' FROM one", "22003", "value \"32768\" is out of range for type smallint", "22", ""},
	} {
		rows, err := db.Query(tc.q)
		if rows != nil {
			rows.Close()
		}
		arithmeticError(t, err, stuntgres.Error{Code: tc.code, Message: tc.message, Position: tc.pos, Hint: tc.hint})
		stmt, err := db.Prepare(tc.q)
		if stmt != nil {
			stmt.Close()
		}
		arithmeticError(t, err, stuntgres.Error{Code: tc.code, Message: tc.message, Position: tc.pos, Hint: tc.hint})
	}
	for _, q := range []string{"SELECT 1&1", "SELECT ~1", "SELECT 1<<1"} {
		rows, err := db.Query(q)
		if rows != nil {
			rows.Close()
		}
		arithmeticError(t, err, stuntgres.Error{Code: "0A000", Message: "SELECT without FROM is unsupported", Position: "1"})
	}
}
