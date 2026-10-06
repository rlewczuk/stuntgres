package stuntgres_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres"
)

func TestDeleteCoreIntegratedPredicates(t *testing.T) {
	for _, tc := range []struct {
		predicate string
		count     int64
		survivors [][]any
	}{
		{"d.v IS NULL", 1, [][]any{{int64(1)}, {int64(3)}, {int64(4)}, {int64(4)}}},
		{"d.v ISNULL", 1, [][]any{{int64(1)}, {int64(3)}, {int64(4)}, {int64(4)}}},
		{"d.v IS NOT NULL", 4, [][]any{{int64(2)}}},
		{"d.v NOTNULL", 4, [][]any{{int64(2)}}},
		{"d.f IS TRUE", 3, [][]any{{int64(2)}, {int64(3)}}},
		{"d.f IS NOT TRUE", 2, [][]any{{int64(1)}, {int64(4)}, {int64(4)}}},
		{"d.f IS FALSE", 1, [][]any{{int64(1)}, {int64(2)}, {int64(4)}, {int64(4)}}},
		{"d.f IS NOT FALSE", 4, [][]any{{int64(3)}}},
		{"d.f IS UNKNOWN", 1, [][]any{{int64(1)}, {int64(3)}, {int64(4)}, {int64(4)}}},
		{"d.f IS NOT UNKNOWN", 4, [][]any{{int64(2)}}},
		{"d.v IS DISTINCT FROM 10", 2, [][]any{{int64(1)}, {int64(4)}, {int64(4)}}},
		{"d.v IS NOT DISTINCT FROM 10", 3, [][]any{{int64(2)}, {int64(3)}}},
		{"d.v IS DISTINCT FROM NULL", 4, [][]any{{int64(2)}}},
		{"d.v IS NOT DISTINCT FROM NULL", 1, [][]any{{int64(1)}, {int64(3)}, {int64(4)}, {int64(4)}}},
		{"CAST(v AS BIGINT)=10", 3, [][]any{{int64(2)}, {int64(3)}}},
		{"v::BIGINT=10", 3, [][]any{{int64(2)}, {int64(3)}}},
		{"int8(v)=10", 3, [][]any{{int64(2)}, {int64(3)}}},
		{"d.v=10 IS TRUE", 3, [][]any{{int64(2)}, {int64(3)}}},
		{"NOT d.v IS NULL AND d.f IS TRUE OR d.v=20", 4, [][]any{{int64(2)}}},
		{"TRUE IS NULL IS TRUE", 0, [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {int64(4)}, {int64(4)}}},
		{"(d.f IS TRUE) IS DISTINCT FROM (d.v IS NULL OR d.f IS FALSE)", 5, [][]any{}},
	} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%t", tc.predicate, prepared), func(t *testing.T) {
				db := openLocal(t, stuntgres.NewDriver(), t.Name())
				execSQL(t, db, "CREATE TABLE t(id INT,v INT,f BOOL)")
				execSQL(t, db, "INSERT INTO t VALUES(1,10,TRUE),(2,NULL,NULL),(3,20,FALSE),(4,10,TRUE),(4,10,TRUE)")
				query := "DELETE FROM t AS d WHERE " + tc.predicate
				if prepared {
					query = "DELETE FROM t d WHERE " + tc.predicate
				}
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
				if count, err := result.RowsAffected(); err != nil || count != tc.count {
					t.Fatal(count, err)
				}
				rows, err := db.Query("SELECT id FROM t ORDER BY id")
				queryCoreRows(t, rows, err, []string{"id"}, []string{"INT4"}, tc.survivors, false)
			})
		}
	}
}

func TestDeleteCoreCastPreflight(t *testing.T) {
	for _, predicate := range []string{"int4($1::text)=id", "FALSE AND int4($1::text)=id", "TRUE OR int4($1::text)=id"} {
		for _, populated := range []bool{false, true} {
			for _, prepared := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%t/%t", predicate, populated, prepared), func(t *testing.T) {
					db := openLocal(t, stuntgres.NewDriver(), t.Name())
					execSQL(t, db, "CREATE TABLE t(id INT)")
					survivors := [][]any{}
					if populated {
						execSQL(t, db, "INSERT INTO t VALUES(7)")
						survivors = [][]any{{int64(7)}}
					}
					query := "DELETE FROM t WHERE " + predicate
					var result sql.Result
					var err error
					if prepared {
						stmt, prepareErr := db.Prepare(query)
						if prepareErr != nil {
							t.Fatal("Prepare must succeed", prepareErr)
						}
						defer stmt.Close()
						result, err = stmt.Exec("bad")
					} else {
						result, err = db.Exec(query, "bad")
					}
					if result != nil {
						t.Fatal("failed conversion exposed result")
					}
					typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`})
					rows, err := db.Query("SELECT id FROM t ORDER BY id")
					queryCoreRows(t, rows, err, []string{"id"}, []string{"INT4"}, survivors, false)
				})
			}
		}
	}
}

func TestDeleteCoreExpressionDiagnosticBoundaries(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(id INT,v INT)")
	execSQL(t, db, "INSERT INTO t VALUES(1,10),(2,20)")
	for _, tc := range []struct {
		query string
		want  stuntgres.Error
	}{
		{"DELETE FROM t WHERE v IS TRUE", stuntgres.Error{Code: "42804", Message: "argument of IS TRUE must be type boolean, not type integer", Position: "21"}},
		{"DELETE FROM t WHERE int8(TRUE) IS NULL", stuntgres.Error{Code: "42883", Message: "function int8(boolean) does not exist", Position: "21", Hint: "No function matches the given name and argument types. You might need to add explicit type casts."}},
		{"DELETE FROM t AS d WHERE d.v::int4=10", stuntgres.Error{Code: "42601", Message: `syntax error at or near "::"`, Position: "29"}},
		{"DELETE FROM t AS d WHERE CAST(d.v AS int4)=10", stuntgres.Error{Code: "42601", Message: `syntax error at or near "."`, Position: "32"}},
		{"DELETE FROM t AS d WHERE int4(d.v)=10", stuntgres.Error{Code: "42601", Message: `syntax error at or near "."`, Position: "32"}},
	} {
		if result, err := db.Exec(tc.query); result != nil {
			t.Fatal(result)
		} else {
			typeCoreDiagnostic(t, err, tc.want)
		}
		if stmt, err := db.Prepare(tc.query); stmt != nil {
			stmt.Close()
			t.Fatal("invalid Prepare succeeded")
		} else {
			typeCoreDiagnostic(t, err, tc.want)
		}
	}
	if stmt, err := db.Prepare("DELETE FROM t WHERE $1 IS NULL"); stmt != nil {
		stmt.Close()
		t.Fatal("untyped Prepare succeeded")
	} else {
		typeCoreDiagnostic(t, err, stuntgres.Error{Code: "42P18", Message: "could not determine data type of parameter $1"})
	}
	for _, query := range []string{"DELETE FROM t AS d WHERE d.v IS NULL; nonsense", "DELETE FROM t AS d WHERE d.v IS NULL RETURNING id"} {
		if result, err := db.Exec(query); err == nil || result != nil {
			t.Fatal("excluded input executed", query, result, err)
		}
		if stmt, err := db.Prepare(query); err == nil || stmt != nil {
			if stmt != nil {
				stmt.Close()
			}
			t.Fatal("excluded input prepared", query, err)
		}
	}
	rows, err := db.Query("SELECT id FROM t ORDER BY id")
	queryCoreRows(t, rows, err, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}, false)
}

func TestDeleteCoreCastPreflightReuseAndRows(t *testing.T) {
	t.Run("held_private_frames", func(t *testing.T) {
		db := openLocal(t, stuntgres.NewDriver(), t.Name())
		execSQL(t, db, "CREATE TABLE t(id INT,v INT)")
		execSQL(t, db, "INSERT INTO t VALUES(1,10),(2,NULL),(3,20),(4,10),(4,10)")
		stmt, err := db.Prepare("DELETE FROM t WHERE v IS NOT DISTINCT FROM int4($1::text)")
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, call := range []struct {
			arg   any
			count int64
			bad   bool
			ids   [][]any
		}{
			{"oops", 0, true, [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {int64(4)}, {int64(4)}}},
			{"10", 3, false, [][]any{{int64(2)}, {int64(3)}}},
			{nil, 1, false, [][]any{{int64(3)}}},
			{"oops", 0, true, [][]any{{int64(3)}}},
			{"20", 1, false, [][]any{}},
		} {
			result, err := stmt.Exec(call.arg)
			if call.bad {
				if result != nil {
					t.Fatal(result)
				}
				typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`})
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if count, err := result.RowsAffected(); err != nil || count != call.count {
					t.Fatal(count, err)
				}
			}
			rows, err := db.Query("SELECT id FROM t ORDER BY id")
			queryCoreRows(t, rows, err, []string{"id"}, []string{"INT4"}, call.ids, false)
		}
	})
	for _, populated := range []bool{false, true} {
		t.Run(fmt.Sprintf("overflow/%t", populated), func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), t.Name())
			execSQL(t, db, "CREATE TABLE t(id INT)")
			if populated {
				execSQL(t, db, "INSERT INTO t VALUES(1)")
			}
			if result, err := db.Exec("DELETE FROM t WHERE id=CAST($1::int8 AS int4)", int64(2147483648)); result != nil {
				t.Fatal(result)
			} else {
				typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22003", Message: "integer out of range"})
			}
			stmt, err := db.Prepare("DELETE FROM t WHERE id=CAST($1::int8 AS int4)")
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			if result, err := stmt.Exec(int64(2147483648)); result != nil {
				t.Fatal(result)
			} else {
				typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22003", Message: "integer out of range"})
			}
			ids := [][]any{}
			if populated {
				ids = [][]any{{int64(1)}}
			}
			rows, err := db.Query("SELECT id FROM t")
			queryCoreRows(t, rows, err, []string{"id"}, []string{"INT4"}, ids, false)
		})
	}
	for _, tc := range []struct {
		predicate string
		want      stuntgres.Error
	}{
		{"FALSE AND int4(s)=42", stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`}},
		{"TRUE OR int4(s)=42", stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`}},
		{"int4(s) IS DISTINCT FROM 0", stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`}},
		{"CAST(n AS int4) IS NOT NULL", stuntgres.Error{Code: "22003", Message: "integer out of range"}},
	} {
		for _, populated := range []bool{false, true} {
			for _, prepared := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%t/%t", tc.predicate, populated, prepared), func(t *testing.T) {
					db := openLocal(t, stuntgres.NewDriver(), t.Name())
					execSQL(t, db, "CREATE TABLE t(id INT,s TEXT,n INT8)")
					ids := [][]any{}
					if populated {
						execSQL(t, db, "INSERT INTO t VALUES(1,'42',42),(2,'oops',2147483648)")
						ids = [][]any{{int64(1)}, {int64(2)}}
					}
					query := "DELETE FROM t WHERE " + tc.predicate
					var result sql.Result
					var err error
					if prepared {
						stmt, prepareErr := db.Prepare(query)
						if prepareErr != nil {
							t.Fatal("Prepare must succeed", prepareErr)
						}
						defer stmt.Close()
						result, err = stmt.Exec()
					} else {
						result, err = db.Exec(query)
					}
					if populated {
						// Shared logical evaluation does not promise pruning.
						if result != nil {
							t.Fatal("failed row conversion exposed result", result)
						}
						typeCoreDiagnostic(t, err, tc.want)
					} else {
						if err != nil {
							t.Fatal(err)
						}
						if count, err := result.RowsAffected(); err != nil || count != 0 {
							t.Fatal(count, err)
						}
					}
					rows, err := db.Query("SELECT id FROM t ORDER BY id")
					queryCoreRows(t, rows, err, []string{"id"}, []string{"INT4"}, ids, false)
				})
			}
		}
	}
	t.Run("explicit_abort", func(t *testing.T) {
		db := openLocal(t, stuntgres.NewDriver(), t.Name())
		execSQL(t, db, "CREATE TABLE t(id INT,s TEXT)")
		execSQL(t, db, "INSERT INTO t VALUES(1,'42'),(2,'oops')")
		tx, err := db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		if _, err := tx.Exec("INSERT INTO t VALUES(7,'7')"); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec("DELETE FROM t WHERE id=1"); err != nil {
			t.Fatal(err)
		}
		if result, err := tx.Exec("DELETE FROM t WHERE int4(s) IS NOT NULL"); result != nil {
			t.Fatal(result)
		} else {
			typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`})
		}
		if _, err := tx.Exec("SELECT id FROM t"); err == nil {
			t.Fatal("failed transaction admitted command")
		} else {
			typeCoreDiagnostic(t, err, stuntgres.Error{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"})
		}
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		rows, err := db.Query("SELECT id FROM t ORDER BY id")
		queryCoreRows(t, rows, err, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}, false)
	})
}
