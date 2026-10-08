package stuntgres_test

import (
	"strconv"
	"stuntgres"
	"testing"
)

func TestUpdateReturningCountCoexistence(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(id INT)")
	execSQL(t, db, "INSERT INTO t VALUES(1),(NULL),(2)")
	q := "SELECT count(*),count(id),count(NULL) FROM t"
	rows, err := db.Query(q)
	queryCoreRows(t, rows, err, []string{"count", "count", "count"}, []string{"INT8", "INT8", "INT8"}, [][]any{{int64(3), int64(2), int64(0)}}, false)
	held, err := db.Prepare(q)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	rows, err = held.Query()
	queryCoreRows(t, rows, err, []string{"count", "count", "count"}, []string{"INT8", "INT8", "INT8"}, [][]any{{int64(3), int64(2), int64(0)}}, false)
	hint := "No function matches the given name and argument types. You might need to add explicit type casts."
	for _, tc := range []struct {
		output string
		d      stuntgres.Error
	}{
		{"count(*)", stuntgres.Error{Code: "42803", Message: "aggregate functions are not allowed in RETURNING"}},
		{"count(id)", stuntgres.Error{Code: "42803", Message: "aggregate functions are not allowed in RETURNING"}},
		{"count(NULL)", stuntgres.Error{Code: "42803", Message: "aggregate functions are not allowed in RETURNING"}},
		{"count()", stuntgres.Error{Code: "42809", Message: "count(*) must be used to call a parameterless aggregate function"}},
		{"count(id,id)", stuntgres.Error{Code: "42883", Message: "function count(integer, integer) does not exist", Hint: hint}},
		{"count(zzzzz)", stuntgres.Error{Code: "42703", Message: `column "zzzzz" does not exist`}},
	} {
		for _, command := range []struct {
			sql      string
			position int
		}{{"UPDATE t SET id=2 RETURNING ", 29}, {"DELETE FROM t RETURNING ", 25}} {
			for _, prefix := range []string{"", "/*é*/ "} {
				q := prefix + command.sql + tc.output
				d := tc.d
				position := command.position
				if tc.output == "count(zzzzz)" {
					position += 6
				}
				if prefix != "" {
					position += 6
				}
				d.Position = strconv.Itoa(position)
				_, err := db.Exec(q)
				typeCoreDiagnostic(t, err, d)
				rows, err := db.Query(q)
				if rows != nil {
					rows.Close()
					t.Fatal("invalid aggregate cursor", q)
				}
				typeCoreDiagnostic(t, err, d)
				handle, err := db.Prepare(q)
				if handle != nil {
					handle.Close()
					t.Fatal("invalid aggregate handle", q)
				}
				typeCoreDiagnostic(t, err, d)
			}
		}
	}
	rows, err = db.Query("SELECT id FROM t")
	queryCoreRows(t, rows, err, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {nil}, {int64(2)}}, true)
}
