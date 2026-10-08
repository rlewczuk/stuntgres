package stuntgres_test

import (
	"bytes"
	"context"
	"stuntgres"
	"testing"
)

func TestCheckSequenceDefinitionRoundTrip(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	db.SetMaxOpenConns(1)
	execSQL(t, db, "CREATE SEQUENCE s START 1")
	execSQL(t, db, "CREATE TABLE t(a INT CONSTRAINT c CHECK(nextval('s')>0),CHECK(random()>=0 AND random()<1))")
	// Neither CREATE nor Dump invokes nextval: the first INSERT consumes 1.
	var empty bytes.Buffer
	if err := stuntgres.Dump(context.Background(), db, &empty); err != nil {
		t.Fatal(err)
	}
	execSQL(t, db, "INSERT INTO t VALUES(1),(2)")
	wantTuples(t, db, "SELECT currval('s')", [][]any{{int64(2)}})
	var dump bytes.Buffer
	if err := stuntgres.Dump(context.Background(), db, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(context.Background(), db, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	var again bytes.Buffer
	if err := stuntgres.Dump(context.Background(), db, &again); err != nil || dump.String() != again.String() {
		t.Fatal("definition/state roundtrip", err, dump.String(), again.String())
	}
	execSQL(t, db, "INSERT INTO t VALUES(3)")
	wantTuples(t, db, "SELECT currval('s')", [][]any{{int64(3)}})
	checkExecError(t, db, false, "DROP SEQUENCE s", stuntgres.Error{Code: "2BP01", Message: "cannot drop sequence s because other objects depend on it", Detail: "constraint c on table t depends on sequence s", Hint: "Use DROP ... CASCADE to drop the dependent objects too."})
	// A rejected drop preserves the provider and CHECK enforcement together.
	execSQL(t, db, "INSERT INTO t VALUES(4)")
	wantTuples(t, db, "SELECT currval('s')", [][]any{{int64(4)}})
	execSQL(t, db, "DROP SEQUENCE s CASCADE")
	execSQL(t, db, "INSERT INTO t VALUES(5)")
	wantTuples(t, db, "SELECT a FROM t ORDER BY a", [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {int64(4)}, {int64(5)}})
}

func TestCheckSequenceDropTransactionalCandidates(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	db.SetMaxOpenConns(1)
	execSQL(t, db, "CREATE SEQUENCE s START 1")
	execSQL(t, db, "CREATE TABLE t(a INT CONSTRAINT c CHECK(nextval('s')>0))")
	execSQL(t, db, "INSERT INTO t VALUES(1)")
	execSQL(t, db, "BEGIN")
	execSQL(t, db, "SAVEPOINT before_drop")
	execSQL(t, db, "DROP SEQUENCE s CASCADE")
	execSQL(t, db, "INSERT INTO t VALUES(2)")
	execSQL(t, db, "ROLLBACK TO before_drop")
	execSQL(t, db, "INSERT INTO t VALUES(3)")
	wantTuples(t, db, "SELECT currval('s')", [][]any{{int64(2)}})
	execSQL(t, db, "ROLLBACK")
	wantTuples(t, db, "SELECT a FROM t", [][]any{{int64(1)}})
	checkExecError(t, db, true, "DROP SEQUENCE s", stuntgres.Error{Code: "2BP01", Message: "cannot drop sequence s because other objects depend on it", Detail: "constraint c on table t depends on sequence s", Hint: "Use DROP ... CASCADE to drop the dependent objects too."})
	execSQL(t, db, "BEGIN")
	execSQL(t, db, "DROP SEQUENCE s CASCADE")
	execSQL(t, db, "INSERT INTO t VALUES(4)")
	execSQL(t, db, "COMMIT")
	execSQL(t, db, "INSERT INTO t VALUES(5)")
	wantTuples(t, db, "SELECT a FROM t ORDER BY a", [][]any{{int64(1)}, {int64(4)}, {int64(5)}})
}
