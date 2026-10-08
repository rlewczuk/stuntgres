package stuntgres_test

import (
	"stuntgres"
	"testing"
)

func TestCheckAlterActionLists(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), t.Name())
			execSQL(t, db, "CREATE TABLE t(p INT,a INT CONSTRAINT c CHECK(a=1),b INT)")
			execSQL(t, db, "INSERT INTO t VALUES(9,1,NULL)")
			checkExecError(t, db, prepared, "ALTER TABLE t DROP CONSTRAINT c,ALTER b SET NOT NULL", stuntgres.Error{Code: "23502", Message: `column "b" of relation "t" contains null values`, Schema: "public", Table: "t", Column: "b"})
			checkExecError(t, db, prepared, "INSERT INTO t VALUES(9,2,NULL)", checkViolation("t", "c", "9, 2, null"))
			wantTuples(t, db, "SELECT * FROM t", [][]any{{int64(9), int64(1), nil}})
			tableCoreExec(t, db, prepared, "ALTER TABLE t DROP p,DROP b", 0)
			checkExecError(t, db, prepared, "INSERT INTO t VALUES(2)", checkViolation("t", "c", "2"))
			tableCoreExec(t, db, prepared, "ALTER TABLE t DROP CONSTRAINT c", 0)
			tableCoreExec(t, db, prepared, "INSERT INTO t VALUES(2)", 1)
		})
	}
}

func TestCheckAlterTransactionalPublication(t *testing.T) {
	driver := stuntgres.NewDriver()
	db := openLocal(t, driver, t.Name())
	peer := openLocal(t, driver, t.Name())
	db.SetMaxOpenConns(1)
	peer.SetMaxOpenConns(1)
	execSQL(t, db, "CREATE TABLE t(a INT CONSTRAINT c CHECK(a=1))")
	execSQL(t, db, "BEGIN")
	execSQL(t, db, "ALTER TABLE t DROP CONSTRAINT c")
	execSQL(t, db, "INSERT INTO t VALUES(2)")
	execSQL(t, db, "ROLLBACK")
	checkExecError(t, peer, false, "INSERT INTO t VALUES(2)", checkViolation("t", "c", "2"))
	wantTuples(t, peer, "SELECT * FROM t", [][]any{})
	execSQL(t, db, "BEGIN")
	execSQL(t, db, "SAVEPOINT keep_check")
	execSQL(t, db, "ALTER TABLE t DROP CONSTRAINT c")
	execSQL(t, db, "INSERT INTO t VALUES(2)")
	execSQL(t, db, "ROLLBACK TO keep_check")
	execSQL(t, db, "SAVEPOINT attempt")
	checkExecError(t, db, true, "INSERT INTO t VALUES($1)", checkViolation("t", "c", "2"), 2)
	execSQL(t, db, "ROLLBACK TO attempt")
	execSQL(t, db, "ALTER TABLE t DROP CONSTRAINT c")
	execSQL(t, db, "INSERT INTO t VALUES(2)")
	execSQL(t, db, "COMMIT")
	execSQL(t, peer, "INSERT INTO t VALUES(2)")
	wantTuples(t, peer, "SELECT * FROM t", [][]any{{int64(2)}, {int64(2)}})
}

func TestCheckAlterOwnedSequenceCandidate(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE SEQUENCE s")
	execSQL(t, db, "CREATE TABLE t(p INT,a INT CONSTRAINT c CHECK(nextval('s')>0))")
	execSQL(t, db, "ALTER SEQUENCE s OWNED BY t.p")
	// The earlier constraint drop must be visible to the later dependency pass.
	execSQL(t, db, "ALTER TABLE t DROP CONSTRAINT c,DROP p")
	execSQL(t, db, "INSERT INTO t VALUES(2)")
	wantTuples(t, db, "SELECT * FROM t", [][]any{{int64(2)}})
}

func TestCheckAlterOwnedSequenceAutoConstraint(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE SEQUENCE s")
	execSQL(t, db, "CREATE TABLE t(p INT,a INT,CONSTRAINT c CHECK(p>0 AND nextval('s')>0))")
	execSQL(t, db, "ALTER SEQUENCE s OWNED BY t.p")
	// Column-dependent CHECK removal precedes its provider dependency check.
	execSQL(t, db, "ALTER TABLE t DROP p")
	execSQL(t, db, "INSERT INTO t VALUES(2)")
	wantTuples(t, db, "SELECT * FROM t", [][]any{{int64(2)}})
}
