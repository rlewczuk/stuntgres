package stuntgres_test

import (
	"database/sql"
	"stuntgres"
	"testing"
)

func checkViolation(table, name, detail string) stuntgres.Error {
	return stuntgres.Error{Code: "23514", Message: `new row for relation "` + table + `" violates check constraint "` + name + `"`, Detail: "Failing row contains (" + detail + ").", Schema: "public", Table: table, Constraint: name}
}
func checkExecError(t *testing.T, db *sql.DB, prepared bool, q string, want stuntgres.Error, args ...any) {
	t.Helper()
	var result sql.Result
	var err error
	if prepared {
		s, e := db.Prepare(q)
		if e != nil {
			t.Fatal("Prepare", q, e)
		}
		defer s.Close()
		result, err = s.Exec(args...)
	} else {
		result, err = db.Exec(q, args...)
	}
	if result != nil {
		t.Fatal("failed statement exposed result", result)
	}
	notNullDiagnostic(t, err, want)
}
func TestCheckCorePreparedFrames(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	tableCoreExec(t, db, false, "CREATE TABLE t(a INT CONSTRAINT only_one CHECK(a=1) NO INHERIT)", 0)
	s, err := db.Prepare("INSERT INTO t VALUES($1)")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, v := range []any{1, 2, nil, 1} {
		r, e := s.Exec(v)
		if v == 2 {
			if r != nil {
				t.Fatal(r)
			}
			notNullDiagnostic(t, e, checkViolation("t", "only_one", "2"))
		} else {
			if e != nil {
				t.Fatal(e)
			}
			n, e := r.RowsAffected()
			if e != nil || n != 1 {
				t.Fatal(n, e)
			}
		}
	}
	alterDropMetadata(t, db, true, "SELECT a FROM t ORDER BY a NULLS FIRST", []string{"a"}, []string{"INT4"}, []bool{true}, 3)
	wantTuples(t, db, "SELECT a FROM t ORDER BY a NULLS FIRST", [][]any{{nil}, {int64(1)}, {int64(1)}})
	u, err := db.Prepare("UPDATE t SET a=$1")
	if err != nil {
		t.Fatal(err)
	}
	defer u.Close()
	for _, v := range []any{1, 2, nil, 1} {
		r, e := u.Exec(v)
		if v == 2 {
			if r != nil {
				t.Fatal(r)
			}
			notNullDiagnostic(t, e, checkViolation("t", "only_one", "2"))
		} else {
			if e != nil {
				t.Fatal(e)
			}
			n, e := r.RowsAffected()
			if e != nil || n != 3 {
				t.Fatal(n, e)
			}
		}
	}
}
func TestCheckCompleteRowAndAtomicity(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), t.Name())
			tableCoreExec(t, db, prepared, "CREATE TABLE t(CONSTRAINT c CHECK(public.t.a<t.b),a INT DEFAULT 1,b INT)", 0)
			tableCoreExec(t, db, prepared, "INSERT INTO t(b) VALUES(2)", 1)
			tableCoreExec(t, db, prepared, "INSERT INTO t VALUES(DEFAULT,2)", 1)
			for _, q := range []string{"INSERT INTO t VALUES(1,2),(3,2),(1,2)", "INSERT INTO t SELECT a,b FROM src", "INSERT INTO t VALUES(1,2),(3,2),(1,2) ORDER BY 1", "UPDATE t SET a=b,b=a"} {
				if q == "INSERT INTO t SELECT a,b FROM src" {
					execSQL(t, db, "CREATE TABLE src(a INT,b INT)")
					execSQL(t, db, "INSERT INTO src VALUES(1,2),(3,2),(1,2)")
				}
				detail := "3, 2"
				if q == "UPDATE t SET a=b,b=a" {
					detail = "2, 1"
				}
				checkExecError(t, db, prepared, q, checkViolation("t", "c", detail))
				wantTuples(t, db, "SELECT * FROM t ORDER BY a", [][]any{{int64(1), int64(2)}, {int64(1), int64(2)}})
			}
			tableCoreExec(t, db, prepared, "UPDATE t SET a=2,b=3", 2)
			execSQL(t, db, "CREATE TABLE u(a INT CONSTRAINT c CHECK(a<3))")
			execSQL(t, db, "INSERT INTO u VALUES(1),(2)")
			checkExecError(t, db, prepared, "UPDATE u SET a=a+1", checkViolation("u", "c", "3"))
			wantTuples(t, db, "SELECT a FROM u ORDER BY a", [][]any{{int64(1)}, {int64(2)}})
		})
	}
}
