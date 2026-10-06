package stuntgres_test

import (
	"context"
	"database/sql"
	"reflect"
	"stuntgres"
	"testing"
)

func TestArithmeticDeleteComposition(t *testing.T) {
	for _, query := range []string{"DELETE FROM t WHERE i+1=8", "DELETE FROM t AS d WHERE i+1=8", "DELETE FROM t WHERE CAST(i AS bigint)+1=8", "DELETE FROM t WHERE b+i=14", "DELETE FROM t WHERE t.i=7"} {
		for _, prepared := range []bool{false, true} {
			t.Run(query+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				_, db := arithmeticDB(t)
				arithmeticExec(t, db, "CREATE TABLE t(id INT,i INT,b BIGINT)", 0)
				arithmeticExec(t, db, "INSERT INTO t VALUES(1,7,7),(2,-7,-7),(3,NULL,NULL),(4,7,7)", 4)
				var result sql.Result
				var err error
				if prepared {
					stmt, e := db.Prepare(query)
					if e != nil {
						t.Fatal(e)
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
				if err != nil || count != 2 {
					t.Fatal(count, err)
				}
				rows, err := db.Query("SELECT id,i,b FROM t ORDER BY id")
				compareNullRows(t, rows, err, []string{"id", "i", "b"}, []string{"INT4", "INT4", "INT8"}, [][]any{{int64(2), int64(-7), int64(-7)}, {int64(3), nil, nil}})
			})
		}
	}
	_, db := arithmeticDB(t)
	arithmeticExec(t, db, "CREATE TABLE t(id INT,i INT,b BIGINT)", 0)
	arithmeticExec(t, db, "INSERT INTO t VALUES(1,7,7),(2,-7,-7),(3,NULL,NULL),(4,7,7)", 4)
	stmt, err := db.Prepare("DELETE FROM t WHERE i+$1=9")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	for index, arg := range []any{2, 16, nil} {
		result, err := stmt.Exec(arg)
		if err != nil {
			t.Fatal(err)
		}
		count, err := result.RowsAffected()
		if err != nil || count != []int64{2, 1, 0}[index] {
			t.Fatal(count, err)
		}
	}
	rows, err := db.Query("SELECT id,i,b FROM t ORDER BY id")
	compareNullRows(t, rows, err, []string{"id", "i", "b"}, []string{"INT4", "INT4", "INT8"}, [][]any{{int64(3), nil, nil}})
}

func TestArithmeticDeleteAtomicity(t *testing.T) {
	for _, tc := range []struct {
		input, query, code, message string
		rows                        [][]any
	}{
		{"(1,7),(2,0),(3,NULL)", "DELETE FROM fail WHERE 14/i=2", "22012", "division by zero", [][]any{{int64(1), int64(7)}, {int64(2), int64(0)}, {int64(3), nil}}},
		{"(1,-1),(2,2147483647)", "DELETE FROM fail WHERE i+1=0", "22003", "integer out of range", [][]any{{int64(1), int64(-1)}, {int64(2), int64(2147483647)}}},
	} {
		for _, prepared := range []bool{false, true} {
			for _, explicit := range []bool{false, true} {
				t.Run(tc.code+map[bool]string{false: "/direct", true: "/prepared"}[prepared]+map[bool]string{false: "/implicit", true: "/explicit"}[explicit], func(t *testing.T) {
					d, db := arithmeticDB(t)
					connector, err := d.OpenConnector(t.Name())
					if err != nil {
						t.Fatal(err)
					}
					peer := sql.OpenDB(connector)
					defer peer.Close()
					arithmeticExec(t, db, "CREATE TABLE fail(id INT,i INT)", 0)
					arithmeticExec(t, db, "INSERT INTO fail VALUES"+tc.input, int64(len(tc.rows)))
					var result sql.Result
					if explicit {
						tx, e := db.Begin()
						if e != nil {
							t.Fatal(e)
						}
						if prepared {
							stmt, e := tx.Prepare(tc.query)
							if e != nil {
								t.Fatal(e)
							}
							result, err = stmt.Exec()
							stmt.Close()
						} else {
							result, err = tx.Exec(tc.query)
						}
						if result != nil {
							t.Fatal("failure exposed result")
						}
						arithmeticError(t, err, stuntgres.Error{Code: tc.code, Message: tc.message})
						_, err = tx.Exec("SELECT i FROM fail")
						arithmeticError(t, err, stuntgres.Error{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"})
						arithmeticRows(t, peer, "SELECT id,i FROM fail ORDER BY id", []string{"INT4", "INT4"}, tc.rows)
						if err := tx.Rollback(); err != nil {
							t.Fatal(err)
						}
					} else {
						if prepared {
							stmt, e := db.Prepare(tc.query)
							if e != nil {
								t.Fatal(e)
							}
							result, err = stmt.Exec()
							stmt.Close()
						} else {
							result, err = db.Exec(tc.query)
						}
						if result != nil {
							t.Fatal("failure exposed result")
						}
						arithmeticError(t, err, stuntgres.Error{Code: tc.code, Message: tc.message})
					}
					arithmeticRows(t, db, "SELECT id,i FROM fail ORDER BY id", []string{"INT4", "INT4"}, tc.rows)
					arithmeticRows(t, peer, "SELECT id,i FROM fail ORDER BY id", []string{"INT4", "INT4"}, tc.rows)
				})
			}
		}
	}
}

func TestArithmeticDeleteDiagnostics(t *testing.T) {
	for _, populated := range []bool{false, true} {
		t.Run(map[bool]string{false: "empty", true: "populated"}[populated], func(t *testing.T) {
			_, db := arithmeticDB(t)
			arithmeticExec(t, db, "CREATE TABLE t(i INT)", 0)
			if populated {
				arithmeticExec(t, db, "INSERT INTO t VALUES(7)", 1)
			}
			for _, tc := range []struct {
				query string
				want  stuntgres.Error
			}{
				{"DELETE FROM t WHERE i+1", stuntgres.Error{Code: "42804", Message: "argument of WHERE must be type boolean, not type integer", Position: "21"}},
				{"DELETE FROM t WHERE (i+1)", stuntgres.Error{Code: "42804", Message: "argument of WHERE must be type boolean, not type integer", Position: "22"}},
				{"/*é*/ DELETE FROM t WHERE i+1", stuntgres.Error{Code: "42804", Message: "argument of WHERE must be type boolean, not type integer", Position: "27"}},
				{"/*é*/ DELETE FROM t WHERE (i+1)", stuntgres.Error{Code: "42804", Message: "argument of WHERE must be type boolean, not type integer", Position: "28"}},
				{"DELETE FROM t WHERE i+1 IS TRUE", stuntgres.Error{Code: "42804", Message: "argument of IS TRUE must be type boolean, not type integer", Position: "21"}},
				{"/*é*/ DELETE FROM t WHERE i+1 IS TRUE", stuntgres.Error{Code: "42804", Message: "argument of IS TRUE must be type boolean, not type integer", Position: "27"}},
				{"DELETE FROM t WHERE (i+1) IS TRUE", stuntgres.Error{Code: "42804", Message: "argument of IS TRUE must be type boolean, not type integer", Position: "22"}},
				{"/*é*/ DELETE FROM t WHERE (i+1) IS TRUE", stuntgres.Error{Code: "42804", Message: "argument of IS TRUE must be type boolean, not type integer", Position: "28"}},
				{"DELETE FROM t WHERE i+TRUE=8", stuntgres.Error{Code: "42883", Message: "operator does not exist: integer + boolean", Position: "22", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."}},
				{"DELETE FROM t WHERE t.i+1=8", stuntgres.Error{Code: "42601", Message: `syntax error at or near "+"`, Position: "24"}},
			} {
				result, err := db.Exec(tc.query)
				if result != nil {
					t.Fatal("analysis error exposed result")
				}
				arithmeticError(t, err, tc.want)
				stmt, err := db.Prepare(tc.query)
				if stmt != nil {
					stmt.Close()
					t.Fatal("analysis error exposed handle")
				}
				arithmeticError(t, err, tc.want)
			}
			want := [][]any{}
			if populated {
				want = [][]any{{int64(7)}}
			}
			arithmeticRows(t, db, "SELECT i FROM t", []string{"INT4"}, want)
		})
	}
}

func TestArithmeticDateComposition(t *testing.T) {
	_, db := arithmeticDB(t)
	arithmeticExec(t, db, "CREATE TABLE t(id INT,i INT)", 0)
	arithmeticExec(t, db, "INSERT INTO t VALUES(1,7)", 1)
	for _, prepared := range []bool{false, true} {
		var rows *sql.Rows
		var err error
		const query = "SELECT i+1,DATE '2000-01-01' FROM t WHERE id=1"
		if prepared {
			stmt, e := db.Prepare(query)
			if e != nil {
				t.Fatal(e)
			}
			defer stmt.Close()
			rows, err = stmt.Query()
		} else {
			rows, err = db.QueryContext(context.Background(), query)
		}
		if err != nil {
			t.Fatal(err)
		}
		names, err := rows.Columns()
		if err != nil || !reflect.DeepEqual(names, []string{"?column?", "date"}) {
			t.Fatal(names, err)
		}
		meta, err := rows.ColumnTypes()
		if err != nil || len(meta) != 2 || meta[0].DatabaseTypeName() != "INT4" || meta[1].DatabaseTypeName() != "DATE" || meta[1].ScanType() != reflect.TypeFor[string]() {
			t.Fatal(meta, err)
		}
		if !rows.Next() {
			t.Fatal(rows.Err())
		}
		var integer, date any
		if err := rows.Scan(&integer, &date); err != nil || integer != int64(8) || date != "2000-01-01" {
			t.Fatal(integer, date, err)
		}
		if rows.Next() || rows.Err() != nil {
			t.Fatal(rows.Err())
		}
		rows.Close()
	}
}

func TestArithmeticDeleteParameterComposition(t *testing.T) {
	for index, arg := range []any{2, 16, nil} {
		t.Run([]string{"two", "sixteen", "null"}[index], func(t *testing.T) {
			_, db := arithmeticDB(t)
			arithmeticExec(t, db, "CREATE TABLE t(id INT,i INT,b BIGINT)", 0)
			arithmeticExec(t, db, "INSERT INTO t VALUES(1,7,7),(2,-7,-7),(3,NULL,NULL),(4,7,7)", 4)
			result, err := db.Exec("DELETE FROM t WHERE i+$1=9", arg)
			if err != nil {
				t.Fatal(err)
			}
			count, err := result.RowsAffected()
			if err != nil || count != []int64{2, 1, 0}[index] {
				t.Fatal(count, err)
			}
			want := [][][]any{
				{{int64(2), int64(-7), int64(-7)}, {int64(3), nil, nil}},
				{{int64(1), int64(7), int64(7)}, {int64(3), nil, nil}, {int64(4), int64(7), int64(7)}},
				{{int64(1), int64(7), int64(7)}, {int64(2), int64(-7), int64(-7)}, {int64(3), nil, nil}, {int64(4), int64(7), int64(7)}},
			}[index]
			rows, err := db.Query("SELECT id,i,b FROM t ORDER BY id")
			compareNullRows(t, rows, err, []string{"id", "i", "b"}, []string{"INT4", "INT4", "INT8"}, want)
		})
	}
}
