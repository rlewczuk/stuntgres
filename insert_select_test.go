package stuntgres_test

import (
	"database/sql"
	"testing"

	"stuntgres"
)

func insertSelectCount(t *testing.T, result sql.Result, err error, want int64) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	count, err := result.RowsAffected()
	if err != nil || count != want {
		t.Fatalf("RowsAffected = %d, %v; want %d", count, err, want)
	}
	id, err := result.LastInsertId()
	if id != 0 || err == nil || err.Error() != "LastInsertId is not supported by this driver" {
		t.Fatalf("LastInsertId = %d, %v; want existing unsupported boundary", id, err)
	}
}

func TestInsertSelectCardinality(t *testing.T) {
	for _, mode := range []string{"direct", "prepared"} {
		t.Run(mode, func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), "insert-select-cardinality-"+mode)
			execSQL(t, db, "CREATE TABLE s(i INT,v TEXT,b BOOL)")
			execSQL(t, db, "INSERT INTO s VALUES(1,'a',TRUE),(1,'a',TRUE),(2,NULL,FALSE),(3,'c',NULL)")
			execSQL(t, db, "CREATE TABLE t(v TEXT,i BIGINT,b BOOL)")
			query := "INSERT INTO t(i,v) SELECT i,v FROM s WHERE b=$1 ORDER BY i"
			execute := func(arg any) (sql.Result, error) { return db.Exec(query, arg) }
			if mode == "prepared" {
				stmt, err := db.Prepare(query)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				execute = func(arg any) (sql.Result, error) { return stmt.Exec(arg) }
			}
			wantTuples(t, db, "SELECT * FROM t", [][]any{}) // Prepare has no effects.
			want := [][]any{}
			for _, tc := range []struct {
				arg   any
				count int64
				added [][]any
			}{
				{nil, 0, nil},
				{true, 2, [][]any{{"a", int64(1), nil}, {"a", int64(1), nil}}},
				{false, 1, [][]any{{nil, int64(2), nil}}},
				{nil, 0, nil},
			} {
				result, err := execute(tc.arg)
				insertSelectCount(t, result, err, tc.count)
				want = append(want, tc.added...)
				rows, err := db.Query("SELECT * FROM t ORDER BY i")
				queryCoreRows(t, rows, err, []string{"v", "i", "b"}, []string{"TEXT", "INT8", "BOOL"}, want, false)
			}
			// Without a target list, a shorter source maps the first declared
			// columns and leaves the remaining nullable column at its default NULL.
			query = "INSERT INTO t SELECT v,i FROM s"
			var result sql.Result
			var err error
			if mode == "prepared" {
				stmt, prepareErr := db.Prepare(query)
				if prepareErr != nil {
					t.Fatal(prepareErr)
				}
				defer stmt.Close()
				result, err = stmt.Exec()
			} else {
				result, err = db.Exec(query)
			}
			insertSelectCount(t, result, err, 4)
			want = append(want, []any{"a", int64(1), nil}, []any{"a", int64(1), nil}, []any{nil, int64(2), nil}, []any{"c", int64(3), nil})
			rows, err := db.Query("SELECT * FROM t")
			queryCoreRows(t, rows, err, []string{"v", "i", "b"}, []string{"TEXT", "INT8", "BOOL"}, want, true)
		})
	}
}

func TestInsertSelectContextualTyping(t *testing.T) {
	for _, mode := range []string{"direct", "prepared"} {
		t.Run(mode, func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), "insert-select-context-"+mode)
			execSQL(t, db, "CREATE TABLE t(i INT)")
			for _, query := range []string{
				"INSERT INTO t(i) SELECT '1'",
				"INSERT INTO t(i) SELECT NULL",
				"INSERT INTO t(i) (VALUES ('1'))",
				"INSERT INTO t(i) VALUES (2),(3) ORDER BY 1 DESC",
			} {
				var result sql.Result
				var err error
				if mode == "prepared" {
					stmt, prepareErr := db.Prepare(query)
					if prepareErr != nil {
						t.Fatal(prepareErr)
					}
					result, err = stmt.Exec()
					stmt.Close()
				} else {
					result, err = db.Exec(query)
				}
				count := int64(1)
				if query == "INSERT INTO t(i) VALUES (2),(3) ORDER BY 1 DESC" {
					count = 2
				}
				insertSelectCount(t, result, err, count)
			}
			query := "INSERT INTO t(i) SELECT $1"
			execute := func(arg any) (sql.Result, error) { return db.Exec(query, arg) }
			if mode == "prepared" {
				stmt, err := db.Prepare(query)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				execute = func(arg any) (sql.Result, error) { return stmt.Exec(arg) }
			}
			for _, arg := range []any{"7", int64(8), nil} {
				result, err := execute(arg)
				insertSelectCount(t, result, err, 1)
			}
			rows, err := db.Query("SELECT i FROM t ORDER BY i")
			queryCoreRows(t, rows, err, []string{"i"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(1)}, {int64(2)}, {int64(3)}, {int64(7)}, {int64(8)}, {nil}, {nil}}, false)
		})
	}
}

func TestInsertSelectDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		name, query string
		want        stuntgres.Error
		runtime     bool
	}{
		{"target_relation", "INSERT INTO missing SELECT 1", stuntgres.Error{Code: "42P01", Message: `relation "missing" does not exist`, Position: "13"}, false},
		{"target_column", "INSERT INTO t(missing) SELECT 1", stuntgres.Error{Code: "42703", Message: `column "missing" of relation "t" does not exist`, Position: "15"}, false},
		{"duplicate_target", "INSERT INTO t(i,i) SELECT 1,2", stuntgres.Error{Code: "42701", Message: `column "i" specified more than once`, Position: "17"}, false},
		{"wide_source", "INSERT INTO t(i) SELECT 1,2", stuntgres.Error{Code: "42601", Message: "INSERT has more expressions than target columns", Position: "27"}, false},
		{"short_source", "INSERT INTO t(i,j) SELECT 1", stuntgres.Error{Code: "42601", Message: "INSERT has more target columns than expressions", Position: "17"}, false},
		{"literal_input", "INSERT INTO t(i) SELECT 'bad'", stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`, Position: "25"}, false},
		{"utf8_literal_input", "/*é*/ INSERT INTO t(i) SELECT 'bad'", stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`, Position: "31"}, false},
		{"source_relation", "INSERT INTO t(i) SELECT i FROM missing", stuntgres.Error{Code: "42P01", Message: `relation "missing" does not exist`, Position: "32"}, false},
		{"source_column", "INSERT INTO t(i) SELECT missing FROM s", stuntgres.Error{Code: "42703", Message: `column "missing" does not exist`, Position: "25"}, false},
		{"empty_known_text", "INSERT INTO t(i) SELECT v FROM empty", stuntgres.Error{Code: "42804", Message: `column "i" is of type integer but expression is of type text`, Hint: "You will need to rewrite or cast the expression.", Position: "25"}, false},
		{"decorated_unknown", "INSERT INTO t(i) VALUES ('1') ORDER BY 1", stuntgres.Error{Code: "42804", Message: `column "i" is of type integer but expression is of type text`, Hint: "You will need to rewrite or cast the expression."}, false},
		{"late_narrowing", "INSERT INTO t(i) SELECT i FROM s ORDER BY i", stuntgres.Error{Code: "22003", Message: "integer out of range"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), "insert-select-diagnostics-"+tc.name)
			execSQL(t, db, "CREATE TABLE t(i INT,j INT)")
			execSQL(t, db, "INSERT INTO t VALUES(9,9)")
			execSQL(t, db, "CREATE TABLE s(i BIGINT)")
			execSQL(t, db, "INSERT INTO s VALUES(1),(2147483648)")
			execSQL(t, db, "CREATE TABLE empty(v TEXT)")
			t.Run("direct", func(t *testing.T) {
				result, err := db.Exec(tc.query)
				if result != nil {
					t.Fatal("failed Exec exposed a result")
				}
				typeCoreDiagnostic(t, err, tc.want)
			})
			t.Run("prepared", func(t *testing.T) {
				stmt, err := db.Prepare(tc.query)
				if tc.runtime {
					if err != nil {
						t.Fatal("runtime cast failed at Prepare", err)
					}
					defer stmt.Close()
					result, executeErr := stmt.Exec()
					if result != nil {
						t.Fatal("failed prepared Exec exposed a result")
					}
					typeCoreDiagnostic(t, executeErr, tc.want)
				} else {
					if stmt != nil {
						stmt.Close()
						t.Fatal("failed Prepare exposed a handle")
					}
					typeCoreDiagnostic(t, err, tc.want)
				}
			})
			wantTuples(t, db, "SELECT * FROM t", [][]any{{int64(9), int64(9)}})
		})
	}
}
