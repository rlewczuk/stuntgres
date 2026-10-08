package stuntgres_test

import (
	"bytes"
	"context"
	"fmt"
	"stuntgres"
	"testing"
)

func TestCheckReviewScalarCommentDML(t *testing.T) {
	for _, provider := range []struct{ name, expr, target string }{
		{"object", "obj_description('anchor'::regclass,'pg_class')", "TABLE anchor"},
		{"legacy", "obj_description('anchor'::regclass)", "TABLE anchor"},
		{"column", "col_description('anchor'::regclass,1)", "COLUMN anchor.a"},
		{"shared", "shobj_description(10,'pg_authid')", "ROLE postgres"},
	} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/prepared=%v", provider.name, prepared), func(t *testing.T) {
				db := openLocal(t, stuntgres.NewDriver(), t.Name())
				db.SetMaxOpenConns(1)
				execSQL(t, db, "CREATE TABLE anchor(a INT)")
				execSQL(t, db, "COMMENT ON "+provider.target+" IS 'old'")
				create := "CREATE TABLE t(a TEXT DEFAULT 'old' CONSTRAINT c CHECK(" + provider.expr + " IS NOT DISTINCT FROM a))"
				if prepared {
					stmt, err := db.Prepare(create)
					if err != nil {
						t.Fatal(err)
					}
					result, err := stmt.Exec()
					stmt.Close()
					if err != nil {
						t.Fatal(err)
					}
					if n, err := result.RowsAffected(); err != nil || n != 0 {
						t.Fatal(n, err)
					}
				} else {
					execSQL(t, db, create)
				}
				insert, err := db.Prepare("INSERT INTO t VALUES($1)")
				if err != nil {
					t.Fatal(err)
				}
				defer insert.Close()
				update, err := db.Prepare("UPDATE t SET a=$1")
				if err != nil {
					t.Fatal(err)
				}
				defer update.Close()
				execSQL(t, db, "INSERT INTO t VALUES('old')")
				if r, err := insert.Exec("old"); err != nil {
					t.Fatal(err)
				} else if n, _ := r.RowsAffected(); n != 1 {
					t.Fatal(n)
				}
				execSQL(t, db, "COMMENT ON "+provider.target+" IS 'new'")
				checkExecError(t, db, false, "INSERT INTO t VALUES('old')", checkViolation("t", "c", "old"))
				checkExecError(t, db, true, "INSERT INTO t VALUES($1)", checkViolation("t", "c", "old"), "old")
				if r, err := insert.Exec("new"); err != nil {
					t.Fatal(err)
				} else if n, _ := r.RowsAffected(); n != 1 {
					t.Fatal(n)
				}
				execSQL(t, db, "UPDATE t SET a='new'")
				checkExecError(t, db, false, "UPDATE t SET a='old'", checkViolation("t", "c", "old"))
				checkExecError(t, db, true, "UPDATE t SET a=$1", checkViolation("t", "c", "old"), "old")
				if r, err := update.Exec("new"); err != nil {
					t.Fatal(err)
				} else if n, _ := r.RowsAffected(); n != 3 {
					t.Fatal(n)
				}
				execSQL(t, db, "INSERT INTO t SELECT 'new'")
				execSQL(t, db, "INSERT INTO t VALUES('new') ORDER BY 1")
				checkExecError(t, db, false, "INSERT INTO t SELECT 'old'", checkViolation("t", "c", "old"))
				checkExecError(t, db, true, "INSERT INTO t VALUES($1) ORDER BY 1", checkViolation("t", "c", "old"), "old")
				wantTuples(t, db, "SELECT a FROM t", [][]any{{"new"}, {"new"}, {"new"}, {"new"}, {"new"}})
				execSQL(t, db, "COMMENT ON "+provider.target+" IS 'old'")
				if r, err := insert.Exec("old"); err != nil {
					t.Fatal(err)
				} else if n, _ := r.RowsAffected(); n != 1 {
					t.Fatal(n)
				}
				if r, err := update.Exec("old"); err != nil {
					t.Fatal(err)
				} else if n, _ := r.RowsAffected(); n != 6 {
					t.Fatal(n)
				}
				execSQL(t, db, "INSERT INTO t DEFAULT VALUES")
				wantTuples(t, db, "SELECT a FROM t", [][]any{{"old"}, {"old"}, {"old"}, {"old"}, {"old"}, {"old"}, {"old"}})
			})
		}
	}
}

func TestCheckReviewScalarInquiryRoundTrip(t *testing.T) {
	for _, expr := range []string{"obj_description(0,'pg_class')", "obj_description(0)", "col_description(0,1)", "shobj_description(0,'pg_authid')"} {
		t.Run(expr, func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), t.Name())
			execSQL(t, db, "CREATE TABLE t(a INT DEFAULT 1 CONSTRAINT c CHECK("+expr+" IS NULL AND a=1))")
			execSQL(t, db, "INSERT INTO t DEFAULT VALUES")
			var dump, again bytes.Buffer
			if err := stuntgres.Dump(context.Background(), db, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(context.Background(), db, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Dump(context.Background(), db, &again); err != nil || !bytes.Equal(dump.Bytes(), again.Bytes()) {
				t.Fatal("nondeterministic inquiry roundtrip", err)
			}
			wantTuples(t, db, "SELECT a FROM t", [][]any{{int64(1)}})
			checkExecError(t, db, false, "INSERT INTO t VALUES(2)", checkViolation("t", "c", "2"))
		})
	}
}

func TestCheckReviewScalarPrivateCommentBoundary(t *testing.T) {
	for _, target := range []string{"TABLE anchor", "COLUMN anchor.a"} {
		t.Run(target, func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), t.Name())
			db.SetMaxOpenConns(1)
			execSQL(t, db, "CREATE TABLE anchor(a INT)")
			execSQL(t, db, "COMMENT ON "+target+" IS 'old'")
			expr := "obj_description('anchor'::regclass,'pg_class')"
			if target == "COLUMN anchor.a" {
				expr = "col_description('anchor'::regclass,1)"
			}
			execSQL(t, db, "CREATE TABLE t(a TEXT CONSTRAINT c CHECK("+expr+" IS NOT DISTINCT FROM a))")
			insert, err := db.Prepare("INSERT INTO t VALUES($1)")
			if err != nil {
				t.Fatal(err)
			}
			defer insert.Close()
			execSQL(t, db, "BEGIN")
			checkExecError(t, db, false, "COMMENT ON "+target+" IS 'new'", stuntgres.Error{Code: "0A000", Message: "statement is unsupported in an explicit transaction"})
			execSQL(t, db, "ROLLBACK")
			checkExecError(t, db, false, "INSERT INTO t VALUES('new')", checkViolation("t", "c", "new"))
			if r, err := insert.Exec("old"); err != nil {
				t.Fatal(err)
			} else if n, _ := r.RowsAffected(); n != 1 {
				t.Fatal(n)
			}
			wantTuples(t, db, "SELECT a FROM t", [][]any{{"old"}})
		})
	}
}
