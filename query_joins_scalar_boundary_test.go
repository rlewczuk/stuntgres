package stuntgres_test

import (
	"database/sql"
	"testing"

	"stuntgres"
)

// The shared query scalar grammar must not broaden existing nonquery entry
// points or change utility Prepare's metadata-only admission contract.
func TestQueryJoinsRetainsScalarInputBoundaries(t *testing.T) {
	db := queryJoinsPublicFixture(t)
	for _, tc := range []struct {
		q    string
		want stuntgres.Error
	}{
		{"CREATE TABLE bad(v INT DEFAULT a.k)", stuntgres.Error{Code: "42601", Message: `syntax error at or near "."`, Position: "33"}},
		{"INSERT INTO a(k) VALUES(a.k)", stuntgres.Error{Code: "42601", Message: `syntax error at or near "."`, Position: "26"}},
		{"SELECT current_setting(a.k)", stuntgres.Error{Code: "42601", Message: `syntax error at or near "."`, Position: "25"}},
		{"INSERT INTO a(k) VALUES(COALESCE(a.k,1))", stuntgres.Error{Code: "0A000", Message: "column references are unsupported in scalar inputs", Position: "34"}},
	} {
		t.Run(tc.q, func(t *testing.T) {
			result, err := db.Exec(tc.q)
			if result != nil {
				t.Fatal("invalid scalar input exposed a result", result)
			}
			notNullDiagnostic(t, err, tc.want)
			stmt, err := db.Prepare(tc.q)
			if stmt != nil {
				stmt.Close()
				t.Fatal("invalid raw scalar input prepared successfully")
			}
			notNullDiagnostic(t, err, tc.want)
		})
	}
	q := "CREATE TABLE bad(v INT DEFAULT COALESCE(a.k,1))"
	want := stuntgres.Error{Code: "0A000", Message: "cannot use column reference in DEFAULT expression", Position: "41"}
	result, err := db.Exec(q)
	if result != nil {
		t.Fatal("invalid DEFAULT exposed a result", result)
	}
	notNullDiagnostic(t, err, want)
	stmt, err := db.Prepare(q)
	if err != nil {
		t.Fatal("conditional DEFAULT lost metadata-only Prepare admission", err)
	}
	result, err = stmt.Exec()
	if result != nil {
		t.Fatal("invalid prepared DEFAULT exposed a result", result)
	}
	notNullDiagnostic(t, err, want)
	stmt.Close()
	rows, err := db.Query("SELECT * FROM bad")
	if rows != nil {
		rows.Close()
		t.Fatal("failed DEFAULT published its table")
	}
	notNullDiagnostic(t, err, stuntgres.Error{Code: "42P01", Message: `relation "bad" does not exist`, Position: "15"})
	rows, err = db.Query("SELECT count(*) FROM a")
	queryCoreRows(t, rows, err, []string{"count"}, []string{"INT8"}, [][]any{{int64(4)}}, false)
}

func TestQueryJoinsRetainsNoFromBoundaries(t *testing.T) {
	db := queryJoinsPublicFixture(t)
	for _, tc := range []struct {
		q    string
		want stuntgres.Error
	}{
		{"SELECT a.k", stuntgres.Error{Code: "0A000", Message: "qualified column references are unsupported", Position: "9"}},
		{"SELECT a.*", stuntgres.Error{Code: "0A000", Message: "qualified column references are unsupported", Position: "9"}},
		{"INSERT INTO a(k) SELECT a.k", stuntgres.Error{Code: "0A000", Message: "qualified column references are unsupported", Position: "26"}},
		{"INSERT INTO a(k) SELECT a.*", stuntgres.Error{Code: "0A000", Message: "qualified column references are unsupported", Position: "26"}},
		{"SELECT (a.k)", stuntgres.Error{Code: "42601", Message: `syntax error at or near "."`, Position: "10"}},
		{"INSERT INTO a(k) SELECT (a.k)", stuntgres.Error{Code: "42601", Message: `syntax error at or near "."`, Position: "27"}},
		{"SELECT num_nulls(a.k)", stuntgres.Error{Code: "42601", Message: `syntax error at or near "."`, Position: "19"}},
		{"SELECT COALESCE(a.k,1)", stuntgres.Error{Code: "0A000", Message: "SELECT without FROM is unsupported", Position: "1"}},
		{"INSERT INTO a(k) SELECT COALESCE(a.k,1)", stuntgres.Error{Code: "0A000", Message: "qualified column references are unsupported", Position: "34"}},
	} {
		t.Run(tc.q, func(t *testing.T) {
			result, err := db.Exec(tc.q)
			if result != nil {
				t.Fatal("invalid no-FROM input exposed a result", result)
			}
			notNullDiagnostic(t, err, tc.want)
			stmt, err := db.Prepare(tc.q)
			if stmt != nil {
				stmt.Close()
				t.Fatal("invalid no-FROM input prepared")
			}
			notNullDiagnostic(t, err, tc.want)
		})
	}
	execSQL(t, db, "CREATE TABLE singleton(k INT)")
	execSQL(t, db, "CREATE TABLE zero()")
	for _, prepared := range []bool{false, true} {
		q := "SELECT num_nulls(NULL,1)"
		if prepared {
			stmt, err := db.Prepare(q)
			if err != nil {
				t.Fatal(err)
			}
			rows, err := stmt.Query()
			queryCoreRows(t, rows, err, []string{"num_nulls"}, []string{"INT4"}, [][]any{{int64(1)}}, false)
			stmt.Close()
		} else {
			rows, err := db.Query(q)
			queryCoreRows(t, rows, err, []string{"num_nulls"}, []string{"INT4"}, [][]any{{int64(1)}}, false)
		}
		for _, tc := range []struct {
			q    string
			args []any
		}{{"INSERT INTO singleton(k) SELECT COALESCE($1,7)", []any{nil}}, {"INSERT INTO zero SELECT", nil}} {
			q, args := tc.q, tc.args
			var result sql.Result
			var err error
			if prepared {
				stmt, prepareErr := db.Prepare(q)
				if prepareErr != nil {
					t.Fatal(prepareErr)
				}
				result, err = stmt.Exec(args...)
				stmt.Close()
			} else {
				result, err = db.Exec(q, args...)
			}
			if err != nil {
				t.Fatal(q, err)
			}
			n, err := result.RowsAffected()
			if err != nil || n != 1 {
				t.Fatal(q, n, err)
			}
		}
	}
	rows, err := db.Query("SELECT k FROM singleton")
	queryCoreRows(t, rows, err, []string{"k"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(7)}}, true)
	rows, err = db.Query("SELECT count(*) FROM zero")
	queryCoreRows(t, rows, err, []string{"count"}, []string{"INT8"}, [][]any{{int64(2)}}, false)
	rows, err = db.Query("SELECT count(*) FROM a")
	queryCoreRows(t, rows, err, []string{"count"}, []string{"INT8"}, [][]any{{int64(4)}}, false)
}
