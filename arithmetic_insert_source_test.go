package stuntgres_test

import (
	"database/sql"
	"fmt"
	"stuntgres"
	"testing"
)

func TestArithmeticInsertSourceContextualPositions(t *testing.T) {
	for _, populated := range []bool{false, true} {
		t.Run(map[bool]string{false: "empty", true: "populated"}[populated], func(t *testing.T) {
			_, db := arithmeticDB(t)
			arithmeticExec(t, db, "CREATE TABLE d(f BOOL)", 0)
			arithmeticExec(t, db, "INSERT INTO d VALUES(TRUE)", 1)
			arithmeticExec(t, db, "CREATE TABLE s(i INT)", 0)
			if populated {
				arithmeticExec(t, db, "INSERT INTO s VALUES(7)", 1)
			}
			for _, operand := range []struct {
				expr, typ string
				offset    int
			}{
				{"1+2", "integer", 0}, {"(1+2)", "integer", 1}, {"((1+2)*3)", "integer", 2}, {"-(1+2)", "integer", 0}, {"(1+2)::int4", "integer", 1}, {"(1+2)::bigint", "bigint", 1},
				{"CAST(1+2 AS bigint)", "bigint", 0}, {"int8(1+2)", "bigint", 0},
			} {
				for _, prefix := range []struct {
					text   string
					offset int
				}{{"", 0}, {"/*é*/ ", 6}} {
					for _, source := range []struct {
						before, after, code, message, hint string
						start                              int
					}{
						{"INSERT INTO d SELECT ", "", "42804", `column "f" is of type boolean but expression is of type ` + operand.typ, "You will need to rewrite or cast the expression.", 22},
						{"INSERT INTO d VALUES(TRUE),(", ") ORDER BY 1", "42804", "VALUES types boolean and " + operand.typ + " cannot be matched", "", 29},
						{"INSERT INTO d SELECT TRUE,", "", "42601", "INSERT has more expressions than target columns", "", 27},
						{"INSERT INTO d(f) SELECT TRUE,", "", "42601", "INSERT has more expressions than target columns", "", 30},
					} {
						query := prefix.text + source.before + operand.expr + source.after
						want := stuntgres.Error{Code: source.code, Message: source.message, Hint: source.hint, Position: fmt.Sprint(source.start + operand.offset + prefix.offset)}
						result, err := db.Exec(query)
						if result != nil {
							t.Fatal("analysis error exposed result", query)
						}
						arithmeticError(t, err, want)
						stmt, err := db.Prepare(query)
						if stmt != nil {
							stmt.Close()
							t.Fatal("analysis error exposed handle", query)
						}
						arithmeticError(t, err, want)
						arithmeticRows(t, db, "SELECT f FROM d", []string{"BOOL"}, [][]any{{true}})
					}
				}
			}
			for _, tc := range []struct{ query, position, typ string }{
				{"INSERT INTO d SELECT i+1 FROM s", "22", "integer"},
				{"INSERT INTO d SELECT (i+1) FROM s", "23", "integer"},
				{"INSERT INTO d SELECT CAST(i+1 AS bigint) FROM s", "22", "bigint"},
				{"/*é*/ INSERT INTO d SELECT i+1 FROM s", "28", "integer"},
				{"/*é*/ INSERT INTO d SELECT (i+1) FROM s", "29", "integer"},
				{"/*é*/ INSERT INTO d SELECT CAST(i+1 AS bigint) FROM s", "28", "bigint"},
				{"INSERT INTO d SELECT (i+1)::bigint FROM s", "23", "bigint"},
				{"/*é*/ INSERT INTO d SELECT (i+1)::bigint FROM s", "29", "bigint"},
			} {
				want := stuntgres.Error{Code: "42804", Message: `column "f" is of type boolean but expression is of type ` + tc.typ, Hint: "You will need to rewrite or cast the expression.", Position: tc.position}
				result, err := db.Exec(tc.query)
				if result != nil {
					t.Fatal("analysis error exposed result")
				}
				arithmeticError(t, err, want)
				stmt, err := db.Prepare(tc.query)
				if stmt != nil {
					stmt.Close()
					t.Fatal("analysis error exposed handle")
				}
				arithmeticError(t, err, want)
				arithmeticRows(t, db, "SELECT f FROM d", []string{"BOOL"}, [][]any{{true}})
			}
			result, err := db.Exec("INSERT INTO d SELECT 1+TRUE")
			if result != nil {
				t.Fatal("operator failure exposed result")
			}
			want := stuntgres.Error{Code: "42883", Message: "operator does not exist: integer + boolean", Position: "23", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."}
			arithmeticError(t, err, want)
			stmt, err := db.Prepare("INSERT INTO d SELECT 1+TRUE")
			if stmt != nil {
				stmt.Close()
				t.Fatal("operator failure exposed handle")
			}
			arithmeticError(t, err, want)
			arithmeticRows(t, db, "SELECT f FROM d", []string{"BOOL"}, [][]any{{true}})
		})
	}
}

func TestArithmeticInsertSourceLimitBoundary(t *testing.T) {
	_, db := arithmeticDB(t)
	arithmeticExec(t, db, "CREATE TABLE d(f BOOL)", 0)
	arithmeticExec(t, db, "INSERT INTO d VALUES(TRUE)", 1)
	for _, tc := range []struct{ query, position string }{
		{"INSERT INTO d VALUES(TRUE),(1+2) LIMIT 1", "34"},
		{"/*é*/ INSERT INTO d VALUES(TRUE),(1+2) LIMIT 1", "40"},
	} {
		want := stuntgres.Error{Code: "0A000", Message: "INSERT suffix is unsupported", Position: tc.position}
		result, err := db.Exec(tc.query)
		if result != nil {
			t.Fatal("excluded LIMIT exposed result")
		}
		arithmeticError(t, err, want)
		stmt, err := db.Prepare(tc.query)
		if stmt != nil {
			stmt.Close()
			t.Fatal("excluded LIMIT exposed handle")
		}
		arithmeticError(t, err, want)
		arithmeticRows(t, db, "SELECT f FROM d", []string{"BOOL"}, [][]any{{true}})
	}
}

func TestArithmeticInsertSourceTableExcessPositions(t *testing.T) {
	for _, populated := range []bool{false, true} {
		t.Run(map[bool]string{false: "empty", true: "populated"}[populated], func(t *testing.T) {
			_, db := arithmeticDB(t)
			arithmeticExec(t, db, "CREATE TABLE d(i INT)", 0)
			arithmeticExec(t, db, "INSERT INTO d VALUES(42)", 1)
			arithmeticExec(t, db, "CREATE TABLE s(i INT)", 0)
			if populated {
				arithmeticExec(t, db, "INSERT INTO s VALUES(7)", 1)
			}
			for _, operand := range []struct {
				expr   string
				offset int
			}{
				{"i+1", 0}, {"(i+1)", 1}, {"(i+1)::bigint", 1}, {"CAST(i+1 AS bigint)", 0},
			} {
				for _, target := range []struct {
					text   string
					offset int
				}{{"", 0}, {"(i)", 3}} {
					for _, prefix := range []struct {
						text   string
						offset int
					}{{"", 0}, {"/*é*/ ", 6}} {
						query := prefix.text + "INSERT INTO d" + target.text + " SELECT i," + operand.expr + " FROM s"
						want := stuntgres.Error{Code: "42601", Message: "INSERT has more expressions than target columns", Position: fmt.Sprint(24 + operand.offset + target.offset + prefix.offset)}
						result, err := db.Exec(query)
						if result != nil {
							t.Fatal("excess source exposed result", query)
						}
						arithmeticError(t, err, want)
						stmt, err := db.Prepare(query)
						if stmt != nil {
							stmt.Close()
							t.Fatal("excess source exposed handle", query)
						}
						arithmeticError(t, err, want)
						arithmeticRows(t, db, "SELECT i FROM d", []string{"INT4"}, [][]any{{int64(42)}})
					}
				}
			}
		})
	}
}

func TestArithmeticInsertSourceSuccess(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			_, db := arithmeticDB(t)
			arithmeticExec(t, db, "CREATE TABLE good(id INT,i INT,b BIGINT)", 0)
			const query = "INSERT INTO good SELECT 1,7/2,-7%2"
			var result sql.Result
			var err error
			if prepared {
				stmt, prepareErr := db.Prepare(query)
				if prepareErr != nil {
					t.Fatal(prepareErr)
				}
				defer stmt.Close()
				result, err = stmt.Exec()
			} else {
				result, err = db.Exec(query)
			}
			if err != nil {
				t.Fatal(err)
			}
			count, err := result.RowsAffected()
			if err != nil || count != 1 {
				t.Fatal(count, err)
			}
			rows, err := db.Query("SELECT id,i,b FROM good")
			compareNullRows(t, rows, err, []string{"id", "i", "b"}, []string{"INT4", "INT4", "INT8"}, [][]any{{int64(1), int64(3), int64(-1)}})
		})
	}
}
