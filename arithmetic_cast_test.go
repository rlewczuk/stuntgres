package stuntgres_test

import (
	"bytes"
	"context"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
)

func TestArithmeticCastComposition(t *testing.T) {
	_, db := arithmeticDB(t)
	arithmeticExec(t, db, "CREATE TABLE t(id INT,i INT,b BIGINT)", 0)
	arithmeticExec(t, db, "INSERT INTO t VALUES(1,7,7),(2,-7,-7),(3,NULL,NULL)", 3)
	arithmeticRows(t, db, "SELECT i::bigint+1,CAST(i+1 AS bigint),(i+1)::bigint,i::int4+1 FROM t ORDER BY id", []string{"INT8", "INT8", "INT8", "INT4"}, [][]any{{int64(8), int64(8), int64(8), int64(8)}, {int64(-6), int64(-6), int64(-6), int64(-6)}, {nil, nil, nil, nil}})
	arithmeticRows(t, db, "SELECT i::bigint+1,CAST(i+1 AS bigint),(i+1)::bigint FROM t WHERE FALSE", []string{"INT8", "INT8", "INT8"}, [][]any{})
	arithmeticRows(t, db, "SELECT i::bigint FROM t ORDER BY id", []string{"INT8"}, [][]any{{int64(7)}, {int64(-7)}, {nil}})
	arithmeticRows(t, db, "SELECT (i+1)::bigint=8,CAST(i+1 AS bool) IS TRUE FROM t ORDER BY id", []string{"BOOL", "BOOL"}, [][]any{{true, true}, {false, true}, {nil, false}})
	arithmeticRows(t, db, "SELECT id FROM t WHERE CAST(i AS bigint)+1=8 ORDER BY id", []string{"INT4"}, [][]any{{int64(1)}})
	arithmeticRows(t, db, "SELECT 2147483647::bigint+1 FROM t WHERE id=1", []string{"INT8"}, [][]any{{int64(2147483648)}})
	arithmeticRows(t, db, "SELECT num_nulls((+1)::int4,NULL)", []string{"INT4"}, [][]any{{int64(1)}})
	arithmeticRows(t, db, "SELECT num_nulls(CAST(+1 AS int4),NULL),num_nonnulls(+1::int4,NULL)", []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}})
	stmt, err := db.Prepare("SELECT CAST($1 AS bigint)+i FROM t ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	for _, arg := range []any{2, nil, 3} {
		want := [][]any{{nil}, {nil}, {nil}}
		if arg == 2 {
			want = [][]any{{int64(9)}, {int64(-5)}, {nil}}
		}
		if arg == 3 {
			want = [][]any{{int64(10)}, {int64(-4)}, {nil}}
		}
		rows, err := stmt.Query(arg)
		compareOpsRows(t, rows, err, []string{"INT8"}, want)
		arithmeticRows(t, db, "SELECT CAST($1 AS bigint)+i FROM t ORDER BY id", []string{"INT8"}, want, arg)
	}
	arithmeticExec(t, db, "CREATE TABLE mins(i INT)", 0)
	arithmeticExec(t, db, "INSERT INTO mins VALUES(-2147483648)", 1)
	arithmeticRows(t, db, "SELECT -i::bigint FROM mins", []string{"INT8"}, [][]any{{int64(2147483648)}})
	for _, query := range []string{"SELECT (-i)::bigint FROM mins", "SELECT CAST(2147483647+1 AS bigint) FROM t WHERE id=1"} {
		stmt, err := db.Prepare(query)
		if err != nil {
			t.Fatal("overflow moved into Prepare", err)
		}
		for _, prepared := range []bool{false, true} {
			var rows *sql.Rows
			if prepared {
				rows, err = stmt.Query()
			} else {
				rows, err = db.Query(query)
			}
			if rows != nil {
				rows.Close()
				t.Fatal("overflow exposed cursor")
			}
			arithmeticError(t, err, stuntgres.Error{Code: "22003", Message: "integer out of range"})
		}
		stmt.Close()
	}
}

func TestArithmeticCastPreflightComposition(t *testing.T) {
	_, db := arithmeticDB(t)
	arithmeticExec(t, db, "CREATE TABLE e(i INT)", 0)
	arithmeticExec(t, db, "CREATE TABLE t(id INT,i INT)", 0)
	arithmeticExec(t, db, "INSERT INTO t VALUES(1,7),(2,NULL)", 2)
	for _, expression := range []string{"i+CAST($1::text AS int4)", "CAST($1::text AS int4)+i", "-CAST($1::text AS int4)+i"} {
		for _, source := range []string{"e", "t WHERE FALSE", "t ORDER BY id"} {
			query := "SELECT " + expression + " FROM " + source
			stmt, err := db.Prepare(query)
			if err != nil {
				t.Fatal("conversion moved into Prepare", query, err)
			}
			for _, arg := range []any{"oops", "2", nil, "oops", "7"} {
				for _, prepared := range []bool{false, true} {
					var rows *sql.Rows
					if prepared {
						rows, err = stmt.Query(arg)
					} else {
						rows, err = db.Query(query, arg)
					}
					if arg == "oops" {
						if rows != nil {
							rows.Close()
							t.Fatal("cast failure exposed cursor", query)
						}
						arithmeticError(t, err, stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`})
						continue
					}
					var value any
					if arg != nil {
						n := int64(2)
						if arg == "7" {
							n = 7
						}
						if strings.HasPrefix(expression, "-") {
							n = -n
						}
						value = int64(7) + n
					}
					want := [][]any{}
					if source == "t ORDER BY id" {
						want = [][]any{{value}, {nil}}
					}
					compareOpsRows(t, rows, err, []string{"INT4"}, want)
				}
			}
			stmt.Close()
		}
	}
	for _, query := range []string{"SELECT i+CAST('oops'::text AS int4) FROM e", "SELECT i+CAST('oops'::text AS int4) FROM t WHERE FALSE"} {
		stmt, err := db.Prepare(query)
		if err != nil {
			t.Fatal("literal conversion moved into Prepare", err)
		}
		for _, prepared := range []bool{false, true} {
			var rows *sql.Rows
			if prepared {
				rows, err = stmt.Query()
			} else {
				rows, err = db.Query(query)
			}
			if rows != nil {
				rows.Close()
				t.Fatal("literal cast failure exposed cursor")
			}
			arithmeticError(t, err, stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`})
		}
		stmt.Close()
	}
	arithmeticRows(t, db, "SELECT i/$1 FROM e", []string{"INT4"}, [][]any{}, 0)
	arithmeticRows(t, db, "SELECT i/$1 FROM t WHERE FALSE", []string{"INT4"}, [][]any{}, 0)
	stmt, err := db.Prepare("SELECT i/$1 FROM t WHERE id=1")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	for _, prepared := range []bool{false, true} {
		var rows *sql.Rows
		if prepared {
			rows, err = stmt.Query(0)
		} else {
			rows, err = db.Query("SELECT i/$1 FROM t WHERE id=1", 0)
		}
		if rows != nil {
			rows.Close()
			t.Fatal("division failure exposed cursor")
		}
		arithmeticError(t, err, stuntgres.Error{Code: "22012", Message: "division by zero"})
	}
}

func TestArithmeticCastDiagnosticComposition(t *testing.T) {
	_, db := arithmeticDB(t)
	arithmeticExec(t, db, "CREATE TABLE t(i INT)", 0)
	arithmeticExec(t, db, "CREATE TABLE d(i INT)", 0)
	for _, populated := range []bool{false, true} {
		if populated {
			arithmeticExec(t, db, "INSERT INTO t VALUES(7),(NULL)", 2)
		}
		for _, tc := range []struct {
			query string
			want  stuntgres.Error
		}{
			{"SELECT (i+1)::int4 IS TRUE FROM t", stuntgres.Error{Code: "42804", Message: "argument of IS TRUE must be type boolean, not type integer", Position: "9"}},
			{"SELECT /*é*/ (i+1)::int4 IS TRUE FROM t", stuntgres.Error{Code: "42804", Message: "argument of IS TRUE must be type boolean, not type integer", Position: "15"}},
			{"SELECT i FROM t WHERE (i+1)::int4 IS TRUE", stuntgres.Error{Code: "42804", Message: "argument of IS TRUE must be type boolean, not type integer", Position: "24"}},
			{"SELECT CAST(i+1 AS bigint) IS TRUE FROM t", stuntgres.Error{Code: "42804", Message: "argument of IS TRUE must be type boolean, not type bigint", Position: "8"}},
			{"SELECT i::int8+TRUE FROM t", stuntgres.Error{Code: "42883", Message: "operator does not exist: bigint + boolean", Position: "15", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."}},
			{"SELECT num_nulls(CAST(1+2 AS int4)) FROM t", stuntgres.Error{Code: "0A000", Message: "arithmetic in null-count arguments is unsupported", Position: "24"}},
			{"SELECT num_nulls((1+2)::int4) FROM t", stuntgres.Error{Code: "0A000", Message: "arithmetic in null-count arguments is unsupported", Position: "20"}},
			{"SELECT num_nulls(CAST(1+2 AS int4))", stuntgres.Error{Code: "0A000", Message: "SELECT without FROM is unsupported", Position: "1"}},
		} {
			stmt, err := db.Prepare(tc.query)
			if stmt != nil {
				stmt.Close()
				t.Fatal("failure exposed statement", tc.query)
			}
			arithmeticError(t, err, tc.want)
			rows, err := db.Query(tc.query)
			if rows != nil {
				rows.Close()
				t.Fatal("failure exposed cursor", tc.query)
			}
			arithmeticError(t, err, tc.want)
		}
	}
	for _, tc := range []struct{ query, position string }{
		{"INSERT INTO d VALUES(CAST(i+1 AS int4))", "27"},
		{"INSERT INTO d VALUES((i+1)::int4)", "23"},
	} {
		stmt, err := db.Prepare(tc.query)
		if stmt != nil {
			stmt.Close()
			t.Fatal("column input exposed statement")
		}
		arithmeticError(t, err, stuntgres.Error{Code: "0A000", Message: "column references are unsupported in scalar inputs", Position: tc.position})
		_, err = db.Exec(tc.query)
		arithmeticError(t, err, stuntgres.Error{Code: "0A000", Message: "column references are unsupported in scalar inputs", Position: tc.position})
	}
	_, err := db.Exec("CREATE TABLE prefix(i INT); INSERT INTO d VALUES((i+1)::int4)")
	if err == nil {
		t.Fatal("forbidden script accepted")
	}
	rows, err := db.Query("SELECT i FROM prefix")
	if rows != nil {
		rows.Close()
		t.Fatal("script prefix published")
	}
	arithmeticError(t, err, stuntgres.Error{Code: "42P01", Message: `relation "prefix" does not exist`, Position: "15"})
}

func TestArithmeticCastAtomicityAndRestore(t *testing.T) {
	ctx := context.Background()
	d, db := arithmeticDB(t)
	connector, err := d.OpenConnector(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	peer := sql.OpenDB(connector)
	defer peer.Close()
	arithmeticExec(t, db, "CREATE TABLE dest(i INT,b BIGINT)", 0)
	arithmeticExec(t, db, "INSERT INTO dest VALUES(CAST(7/2 AS int4),(-7%2)::int8)", 1)
	arithmeticRows(t, peer, "SELECT i,b FROM dest", []string{"INT4", "INT8"}, [][]any{{int64(3), int64(-1)}})
	arithmeticExec(t, db, "CREATE TABLE d(v BIGINT)", 0)
	arithmeticExec(t, db, "INSERT INTO d VALUES(9)", 1)
	for _, tc := range []struct{ query, code, message string }{
		{"INSERT INTO d VALUES(CAST(1+1 AS bigint)),(CAST(2147483647+1 AS bigint))", "22003", "integer out of range"},
		{"INSERT INTO d VALUES(CAST(1+1 AS bigint)),((2147483648::bigint+0)::int4)", "22003", "integer out of range"},
		{"INSERT INTO d VALUES(CAST(1+1 AS bigint)),((1/0)::bigint)", "22012", "division by zero"},
	} {
		for _, prepared := range []bool{false, true} {
			stmt, err := db.Prepare(tc.query)
			if err != nil {
				t.Fatal("arithmetic failure moved into Prepare", err)
			}
			if prepared {
				_, err = stmt.Exec()
			} else {
				_, err = db.Exec(tc.query)
			}
			stmt.Close()
			arithmeticError(t, err, stuntgres.Error{Code: tc.code, Message: tc.message})
			arithmeticRows(t, peer, "SELECT v FROM d", []string{"INT8"}, [][]any{{int64(9)}})
		}
		tx, err := db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		_, err = tx.Exec(tc.query)
		arithmeticError(t, err, stuntgres.Error{Code: tc.code, Message: tc.message})
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		arithmeticRows(t, peer, "SELECT v FROM d", []string{"INT8"}, [][]any{{int64(9)}})
	}
	held, err := db.Query("SELECT v+1 FROM d")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	err = stuntgres.Restore(ctx, db, strings.NewReader("CREATE TABLE d(v BIGINT); INSERT INTO d VALUES(CAST(7/2 AS bigint)+1); CREATE TABLE dest(i INT,b BIGINT); INSERT INTO dest VALUES(CAST(7/2 AS int4),(-7%2)::int8)"))
	if err != nil {
		t.Fatal(err)
	}
	arithmeticRows(t, db, "SELECT v FROM d", []string{"INT8"}, [][]any{{int64(4)}})
	arithmeticRows(t, db, "SELECT i,b FROM dest", []string{"INT4", "INT8"}, [][]any{{int64(3), int64(-1)}})
	var first, second bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &first); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(ctx, db, strings.NewReader(first.String())); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Dump(ctx, db, &second); err != nil || first.String() != second.String() {
		t.Fatal(first.String(), second.String(), err)
	}
	err = stuntgres.Restore(ctx, db, strings.NewReader("CREATE TABLE d(v BIGINT); INSERT INTO d VALUES(CAST(2147483647+1 AS bigint))"))
	arithmeticError(t, err, stuntgres.Error{Code: "22003", Message: "integer out of range"})
	for _, prefix := range []string{"", "/*é*/ "} {
		position := "50"
		if prefix != "" {
			position = "56"
		}
		err = stuntgres.Restore(ctx, db, strings.NewReader(prefix+"CREATE TABLE d(i INT); INSERT INTO d VALUES(CAST($1+1 AS int4));"))
		arithmeticError(t, err, stuntgres.Error{Code: "42P02", Message: "parameters are not allowed in restore", Position: position})
	}
	arithmeticRows(t, peer, "SELECT v FROM d", []string{"INT8"}, [][]any{{int64(4)}})
	meta, err := held.ColumnTypes()
	if err != nil || len(meta) != 1 || meta[0].Name() != "?column?" || meta[0].DatabaseTypeName() != "INT8" {
		t.Fatal(meta, err)
	}
	if !held.Next() {
		t.Fatal(held.Err())
	}
	var old int64
	if err := held.Scan(&old); err != nil || old != 10 {
		t.Fatal(old, err)
	}
}

func TestArithmeticSignedCastLiteralComposition(t *testing.T) {
	_, db := arithmeticDB(t)
	arithmeticExec(t, db, "CREATE TABLE t(id INT,i INT)", 0)
	arithmeticExec(t, db, "INSERT INTO t VALUES(1,7),(2,NULL)", 2)
	arithmeticRows(t, db, "SELECT num_nulls(-1::int4,NULL),num_nonnulls(-(CAST(1 AS bigint)::int8),NULL),num_nulls((-1)::int4,NULL),num_nonnulls(-1.5,NULL),num_nulls(+1.5,NULL)", []string{"INT4", "INT4", "INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(1), int64(1), int64(1), int64(1)}})
	for _, tc := range []struct{ query, message string }{
		{"SELECT num_nulls(-CAST(2147483648 AS int4),NULL)", "integer out of range"},
		{"SELECT num_nulls(-CAST(-2147483648 AS int4),NULL)", "integer out of range"},
		{"SELECT num_nulls(-CAST(-9223372036854775808 AS int8),NULL)", "bigint out of range"},
	} {
		stmt, err := db.Prepare(tc.query)
		if err != nil {
			t.Fatal("literal cast/sign error moved into Prepare", tc.query, err)
		}
		rows, err := db.Query(tc.query)
		if rows != nil {
			rows.Close()
			t.Fatal("overflow exposed cursor")
		}
		arithmeticError(t, err, stuntgres.Error{Code: "22003", Message: tc.message})
		rows, err = stmt.Query()
		if rows != nil {
			rows.Close()
			t.Fatal("prepared overflow exposed cursor")
		}
		arithmeticError(t, err, stuntgres.Error{Code: "22003", Message: tc.message})
		stmt.Close()
	}
	for _, sign := range []string{"+", "-"} {
		for _, child := range []string{"CAST(1+2 AS int4)", "CAST(i AS int4)", "CAST($1 AS int4)"} {
			args := []any{}
			if strings.Contains(child, "$1") {
				args = []any{2}
			}
			for _, from := range []bool{false, true} {
				query := "SELECT num_nulls(" + sign + child + ",NULL)"
				message, position := "SELECT without FROM is unsupported", "1"
				if from {
					query += " FROM t ORDER BY id"
					message, position = "arithmetic in null-count arguments is unsupported", "18"
				}
				rows, err := db.Query(query, args...)
				if rows != nil {
					rows.Close()
					t.Fatal("excluded argument exposed cursor", query)
				}
				arithmeticError(t, err, stuntgres.Error{Code: "0A000", Message: message, Position: position})
				stmt, err := db.Prepare(query)
				if stmt != nil {
					stmt.Close()
					t.Fatal("excluded argument exposed handle", query)
				}
				arithmeticError(t, err, stuntgres.Error{Code: "0A000", Message: message, Position: position})
			}
		}
	}
}
