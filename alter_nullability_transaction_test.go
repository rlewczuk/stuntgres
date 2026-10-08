package stuntgres_test

import (
	"strconv"
	"stuntgres"
	"testing"
	"unicode/utf8"
)

func TestAlterNullabilityNames(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(a INT,b INT)")
	execSQL(t, db, "INSERT INTO t VALUES(7,9)")
	tableCoreExec(t, db, true, "ALTER TABLE t ADD CONSTRAINT nn NOT NULL a, ADD CONSTRAINT other NOT NULL a", 0)
	_, err := db.Exec("ALTER TABLE t DROP CONSTRAINT other")
	notNullDiagnostic(t, err, stuntgres.Error{Code: "42704", Message: `constraint "other" of relation "t" does not exist`})
	tableCoreExec(t, db, false, "ALTER TABLE t DROP CONSTRAINT nn, ADD CONSTRAINT nn NOT NULL a", 0)
	tableCoreExec(t, db, true, "ALTER TABLE t DROP CONSTRAINT nn CASCADE, ADD CONSTRAINT nn NOT NULL b", 0)
	tableCoreExec(t, db, false, "ALTER TABLE t ALTER a SET NOT NULL, ADD CONSTRAINT nn NOT NULL a", 0)
	tableCoreExec(t, db, false, "ALTER TABLE t DROP CONSTRAINT t_a_not_null", 0)
	_, err = db.Exec("ALTER TABLE t ADD CONSTRAINT nn NOT NULL a")
	notNullDiagnostic(t, err, stuntgres.Error{Code: "42710", Message: `constraint "nn" for relation "t" already exists`})
	tableCoreExec(t, db, true, "INSERT INTO t VALUES(NULL,10)", 1)
	tableCoreExec(t, db, false, "ALTER TABLE t DROP CONSTRAINT nn RESTRICT", 0)
}

func TestAlterNullabilityOwnDeletesAndSequences(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(a INT,b INT)")
	execSQL(t, db, "INSERT INTO t VALUES(NULL,1),(7,2)")
	execSQL(t, db, "CREATE SEQUENCE seq START 10")
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	run := func(q string, count int64) {
		t.Helper()
		r, e := tx.Exec(q)
		if e != nil {
			t.Fatal(q, e)
		}
		n, e := r.RowsAffected()
		if e != nil || n != count {
			t.Fatal(q, n, e)
		}
	}
	run("DELETE FROM t WHERE b=1", 1)
	run("ALTER TABLE t ALTER a SET NOT NULL", 0)
	run("ALTER SEQUENCE seq RESTART WITH 20", 0)
	run("SAVEPOINT child", 0)
	run("ALTER TABLE t DROP b", 0)
	run("INSERT INTO t VALUES(8)", 1)
	run("ROLLBACK TO child", 0)
	run("INSERT INTO t VALUES(9,3)", 1)
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	wantTuples(t, db, "SELECT * FROM t ORDER BY a", [][]any{{int64(7), int64(2)}, {int64(9), int64(3)}})
	wantTuples(t, db, "SELECT nextval('seq')", [][]any{{int64(20)}})
	tx, err = db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	run("ALTER TABLE t ALTER a DROP NOT NULL", 0)
	run("INSERT INTO t VALUES(NULL,4)", 1)
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec("INSERT INTO t VALUES(NULL,4)")
	notNullDiagnostic(t, err, stuntgres.Error{Code: "23502", Message: `null value in column "a" of relation "t" violates not-null constraint`, Detail: "Failing row contains (null, 4).", Schema: "public", Table: "t", Column: "a"})
	tx, err = db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	run("ALTER TABLE t ALTER a DROP NOT NULL", 0)
	run("INSERT INTO t VALUES(NULL,4)", 1)
	_, err = tx.Exec("ALTER TABLE t ALTER a SET NOT NULL")
	notNullDiagnostic(t, err, stuntgres.Error{Code: "23502", Message: `column "a" of relation "t" contains null values`, Schema: "public", Table: "t", Column: "a"})
	tx.Rollback()
}

func TestAlterNullabilityDependencyAtomicity(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(a INT,b INT)")
	execSQL(t, db, "INSERT INTO t VALUES(7,9)")
	execSQL(t, db, "CREATE VIEW v AS SELECT b FROM t")
	execSQL(t, db, "CREATE SEQUENCE owned OWNED BY t.b")
	_, restricted := db.Exec("ALTER TABLE t DROP COLUMN b RESTRICT, ALTER a SET NOT NULL")
	notNullDiagnostic(t, restricted, stuntgres.Error{Code: "2BP01", Message: "cannot drop column b of table t because other objects depend on it", Detail: "view v depends on column b of table t", Hint: "Use DROP ... CASCADE to drop the dependent objects too."})
	_, err := db.Exec("ALTER TABLE t DROP COLUMN b CASCADE, ALTER missing SET NOT NULL")
	notNullDiagnostic(t, err, stuntgres.Error{Code: "42703", Message: `column "missing" of relation "t" does not exist`})
	wantTuples(t, db, "SELECT * FROM v", [][]any{{int64(9)}})
	wantTuples(t, db, "SELECT nextval('owned')", [][]any{{int64(1)}})
	tableCoreExec(t, db, true, "ALTER TABLE t DROP COLUMN b CASCADE, ALTER a SET NOT NULL", 0)
	wantTuples(t, db, "SELECT * FROM t", [][]any{{int64(7)}})
	_, err = db.Exec("ALTER SEQUENCE owned RESTART")
	notNullDiagnostic(t, err, stuntgres.Error{Code: "42P01", Message: `relation "owned" does not exist`})
}

func TestAlterNullabilityWholeInput(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(a INT,b INT)")
	execSQL(t, db, "INSERT INTO t VALUES(7,9)")
	for _, q := range []string{"ALTER TABLE t ALTER a SET NOT NULL,", "CREATE TABLE prefix(v INT);INSERT INTO prefix VALUES(1);ALTER TABLE t ALTER a SET NOT NULL,"} {
		for _, prepared := range []bool{false, true} {
			var err error
			if prepared {
				_, err = db.Prepare(q)
			} else {
				_, err = db.Exec(q)
			}
			notNullDiagnostic(t, err, stuntgres.Error{Code: "42601", Message: "syntax error at end of input", Position: strconv.Itoa(utf8.RuneCountInString(q) + 1)})
		}
	}
	wantTuples(t, db, "SELECT * FROM t", [][]any{{int64(7), int64(9)}})
	_, err := db.Query("SELECT * FROM prefix")
	notNullDiagnostic(t, err, stuntgres.Error{Code: "42P01", Message: `relation "prefix" does not exist`, Position: "15"})
}
