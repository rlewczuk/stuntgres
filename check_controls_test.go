package stuntgres_test

import (
	"bytes"
	"context"
	"strings"
	"stuntgres"
	"testing"
)

func TestCheckControlsAndLifecycle(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	ctx := context.Background()
	execSQL(t, db, "CREATE TABLE t(prefix TEXT,a INT DEFAULT 1 CONSTRAINT c CHECK(a=1) NO INHERIT,b INT CONSTRAINT keep CHECK(b>0))")
	execSQL(t, db, "INSERT INTO t VALUES('old',1,2),('null',NULL,3)")
	saved, err := db.Query("SELECT a FROM t ORDER BY a NULLS FIRST")
	if err != nil {
		t.Fatal(err)
	}
	defer saved.Close()
	var dump bytes.Buffer
	if err = stuntgres.Dump(ctx, db, &dump); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dump.String(), `CONSTRAINT "c" CHECK`) || !strings.Contains(dump.String(), "NO INHERIT") {
		t.Fatal(dump.String())
	}
	if err = stuntgres.Reset(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err = stuntgres.Restore(ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	var again bytes.Buffer
	if err = stuntgres.Dump(ctx, db, &again); err != nil || dump.String() != again.String() {
		t.Fatal(err, again.String())
	}
	for _, script := range []string{"CREATE TABLE replacement(a INT CHECK(a))", "CREATE TABLE replacement(a INT CONSTRAINT c CHECK(a=1));INSERT INTO replacement VALUES(2)"} {
		err = stuntgres.Restore(ctx, db, strings.NewReader(script))
		if err == nil {
			t.Fatal("invalid restore succeeded")
		}
		var after bytes.Buffer
		if err = stuntgres.Dump(ctx, db, &after); err != nil || after.String() != dump.String() {
			t.Fatal("failed restore changed target", err)
		}
	}
	if !saved.Next() {
		t.Fatal(saved.Err())
	}
	var v any
	if err = saved.Scan(&v); err != nil || v != nil {
		t.Fatal(v, err)
	}
	if !saved.Next() {
		t.Fatal(saved.Err())
	}
	if err = saved.Scan(&v); err != nil || v != int64(1) {
		t.Fatal(v, err)
	}
	saved.Close()
	execSQL(t, db, "ALTER TABLE t RENAME TO u")
	execSQL(t, db, "ALTER TABLE u RENAME a TO renamed")
	execSQL(t, db, "ALTER TABLE u DROP prefix")
	checkExecError(t, db, false, "INSERT INTO u VALUES(2,2)", checkViolation("u", "c", "2, 2"))
	execSQL(t, db, "ALTER TABLE u DROP renamed")
	checkExecError(t, db, false, "INSERT INTO u VALUES(0)", checkViolation("u", "keep", "0"))
	execSQL(t, db, "ALTER TABLE u DROP CONSTRAINT keep")
	execSQL(t, db, "INSERT INTO u VALUES(0)")
}
func TestCheckLikeAndConstraintCollisions(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(a INT CONSTRAINT c CHECK(a=1))")
	execSQL(t, db, "CREATE TABLE clone(LIKE t)")
	execSQL(t, db, "INSERT INTO clone VALUES(2)")
	checkExecError(t, db, false, "ALTER TABLE t ADD CONSTRAINT c NOT NULL a", stuntgres.Error{Code: "42710", Message: `constraint "c" for relation "t" already exists`})
	execSQL(t, db, "ALTER TABLE t DROP CONSTRAINT c")
	execSQL(t, db, "INSERT INTO t VALUES(2)")
}
func TestCheckExplicitTransactionAndSavepoint(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	db.SetMaxOpenConns(1)
	execSQL(t, db, "CREATE TABLE t(a INT CONSTRAINT c CHECK(a=1))")
	execSQL(t, db, "INSERT INTO t VALUES(1)")
	execSQL(t, db, "BEGIN")
	execSQL(t, db, "INSERT INTO t VALUES(1)")
	checkExecError(t, db, false, "INSERT INTO t VALUES(2)", checkViolation("t", "c", "2"))
	_, err := db.Exec("SELECT * FROM t")
	sqlState(t, err, "25P02")
	execSQL(t, db, "ROLLBACK")
	wantTuples(t, db, "SELECT * FROM t", [][]any{{int64(1)}})
	execSQL(t, db, "BEGIN")
	execSQL(t, db, "SAVEPOINT recover")
	checkExecError(t, db, true, "INSERT INTO t VALUES($1)", checkViolation("t", "c", "2"), 2)
	execSQL(t, db, "ROLLBACK TO recover")
	execSQL(t, db, "INSERT INTO t VALUES(1)")
	execSQL(t, db, "COMMIT")
	wantTuples(t, db, "SELECT * FROM t", [][]any{{int64(1)}, {int64(1)}})
}
