package stuntgres_test

import (
	"bytes"
	"database/sql"
	"stuntgres"
	"testing"
)

func TestAlterTypeTransactionsAndLiveRows(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(id INT,c TEXT)")
	execSQL(t, db, "INSERT INTO t VALUES(1,'oops'),(2,'bad'),(3,'9')")
	execSQL(t, db, "UPDATE t SET c='7' WHERE id=1")
	execSQL(t, db, "DELETE FROM t WHERE id=2")
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for _, q := range []string{"INSERT INTO t VALUES(4,'ownbad')", "UPDATE t SET c='8' WHERE id=4", "INSERT INTO t VALUES(5,'deletedbad')", "DELETE FROM t WHERE id=5", "SAVEPOINT s", "ALTER TABLE t ALTER c TYPE INT USING c::int"} {
		if _, err = tx.Exec(q); err != nil {
			t.Fatal(q, err)
		}
	}
	alterTypePublicRows(t, tx, "SELECT c FROM t ORDER BY id", []string{"c"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(9)}, {int64(8)}})
	for _, q := range []string{"ROLLBACK TO s", "ROLLBACK TO s"} {
		if _, err = tx.Exec(q); err != nil {
			t.Fatal(q, err)
		}
	}
	alterTypePublicRows(t, tx, "SELECT c FROM t ORDER BY id", []string{"c"}, []string{"TEXT"}, [][]any{{"7"}, {"9"}, {"8"}})
	if _, err = tx.Exec("INSERT INTO t VALUES(6,'latebad')"); err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec("ALTER TABLE t ALTER c TYPE INT USING c::int")
	notNullDiagnostic(t, err, stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type integer: "latebad"`})
	_, err = tx.Exec("SELECT * FROM t")
	notNullDiagnostic(t, err, stuntgres.Error{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"})
	if _, err = tx.Exec("ROLLBACK TO s"); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec("ALTER TABLE t ALTER c TYPE INT USING c::int"); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	alterTypePublicRows(t, db, "SELECT c FROM t ORDER BY id", []string{"c"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(9)}, {int64(8)}})
}

func TestAlterTypeOwnerRepeatableReadAndPrivateDump(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	ctx := t.Context()
	execSQL(t, db, "CREATE TABLE t(c TEXT DEFAULT '7')")
	execSQL(t, db, "INSERT INTO t VALUES('oops')")
	execSQL(t, db, "CREATE TABLE u(c TEXT)")
	execSQL(t, db, "INSERT INTO u VALUES('oops')")
	execSQL(t, db, "CREATE TABLE anchor(c INT)")
	execSQL(t, db, "INSERT INTO anchor VALUES(1)")
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	alterTypePublicRows(t, tx, "SELECT c FROM anchor", []string{"c"}, []string{"INT4"}, [][]any{{int64(1)}})
	execSQL(t, db, "UPDATE u SET c='7'")
	if _, err = tx.Exec("SAVEPOINT before_type"); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec("ALTER TABLE u ALTER c TYPE INT USING c::int"); err != nil {
		t.Fatal(err)
	}
	alterTypePublicRows(t, tx, "SELECT c FROM u", []string{"c"}, []string{"INT4"}, [][]any{{int64(7)}})
	var dump bytes.Buffer
	if err = stuntgres.Dump(ctx, db, &dump); err != nil {
		t.Fatal(err)
	}
	// Dump captures committed descriptors/current rows, not the owner's private rewrite.
	restored := openLocal(t, stuntgres.NewDriver(), t.Name()+"-restore")
	if err = stuntgres.Restore(ctx, restored, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	alterTypePublicRows(t, restored, "SELECT c FROM u", []string{"c"}, []string{"TEXT"}, [][]any{{"7"}})
	if _, err = tx.Exec("ROLLBACK TO before_type"); err != nil {
		t.Fatal(err)
	}
	alterTypePublicRows(t, tx, "SELECT c FROM u", []string{"c"}, []string{"TEXT"}, [][]any{{"oops"}})
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	alterTypePublicRows(t, db, "SELECT c FROM u", []string{"c"}, []string{"TEXT"}, [][]any{{"7"}})
}

func TestAlterTypeCommittedOwnUpdateAndDelete(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(id INT,c TEXT)")
	execSQL(t, db, "INSERT INTO t VALUES(1,'bad'),(2,'deletedbad'),(3,'9')")
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for _, q := range []string{"UPDATE t SET c='8' WHERE id=1", "DELETE FROM t WHERE id=2", "ALTER TABLE t ALTER c TYPE INT USING c::int"} {
		if _, err = tx.Exec(q); err != nil {
			t.Fatal(q, err)
		}
	}
	alterTypePublicRows(t, tx, "SELECT id,c FROM t ORDER BY id", []string{"id", "c"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(8)}, {int64(3), int64(9)}})
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	alterTypePublicRows(t, db, "SELECT id,c FROM t ORDER BY id", []string{"id", "c"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(8)}, {int64(3), int64(9)}})
	execSQL(t, db, "UPDATE t SET c=c+1 WHERE id=1")
	execSQL(t, db, "DELETE FROM t WHERE id=3")
	alterTypePublicRows(t, db, "SELECT id,c FROM t", []string{"id", "c"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(9)}})
	execSQL(t, db, "CREATE TABLE u(id INT,c TEXT)")
	execSQL(t, db, "INSERT INTO u VALUES(1,'bad'),(2,'deletedbad'),(3,'9')")
	rollback, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer rollback.Rollback()
	for _, q := range []string{"UPDATE u SET c='7' WHERE id=1", "DELETE FROM u WHERE id=2", "ALTER TABLE u ALTER c TYPE INT USING c::int"} {
		if _, err = rollback.Exec(q); err != nil {
			t.Fatal(q, err)
		}
	}
	alterTypePublicRows(t, rollback, "SELECT id,c FROM u ORDER BY id", []string{"id", "c"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(7)}, {int64(3), int64(9)}})
	if err = rollback.Rollback(); err != nil {
		t.Fatal(err)
	}
	alterTypePublicRows(t, db, "SELECT id,c FROM u ORDER BY id", []string{"id", "c"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "bad"}, {int64(2), "deletedbad"}, {int64(3), "9"}})
}

func TestAlterTypeRepeatableReadBeforeSnapshotAndPostTypeMark(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(c INT,discard INT)")
	execSQL(t, db, "INSERT INTO t VALUES(7,1)")
	execSQL(t, db, "CREATE TABLE anchor(c INT)")
	execSQL(t, db, "INSERT INTO anchor VALUES(1)")
	tx, err := db.BeginTx(t.Context(), &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for _, q := range []string{"ALTER TABLE t ALTER c TYPE BIGINT USING c+1", "SAVEPOINT after_type"} {
		if _, err = tx.Exec(q); err != nil {
			t.Fatal(q, err)
		}
	}
	// First snapshot is established only after the private rewrite.
	alterTypePublicRows(t, tx, "SELECT c FROM anchor", []string{"c"}, []string{"INT4"}, [][]any{{int64(1)}})
	execSQL(t, db, "UPDATE anchor SET c=2")
	for _, q := range []string{"ALTER TABLE t ALTER c TYPE INT USING c+10, DROP discard", "UPDATE t SET c=c+100", "ROLLBACK TO after_type", "ROLLBACK TO after_type"} {
		if _, err = tx.Exec(q); err != nil {
			t.Fatal(q, err)
		}
	}
	alterTypePublicRows(t, tx, "SELECT * FROM t", []string{"c", "discard"}, []string{"INT8", "INT4"}, [][]any{{int64(8), int64(1)}})
	alterTypePublicRows(t, tx, "SELECT c FROM anchor", []string{"c"}, []string{"INT4"}, [][]any{{int64(1)}})
	for _, q := range []string{"ALTER TABLE t ALTER c TYPE INT USING c+1, DROP discard", "ALTER TABLE t ALTER c TYPE BIGINT USING c+1"} {
		if _, err = tx.Exec(q); err != nil {
			t.Fatal(q, err)
		}
	}
	alterTypePublicRows(t, tx, "SELECT * FROM t", []string{"c"}, []string{"INT8"}, [][]any{{int64(10)}})
	alterTypePublicRows(t, tx, "SELECT c FROM anchor", []string{"c"}, []string{"INT4"}, [][]any{{int64(1)}})
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	alterTypePublicRows(t, db, "SELECT * FROM t", []string{"c"}, []string{"INT8"}, [][]any{{int64(10)}})
	alterTypePublicRows(t, db, "SELECT c FROM anchor", []string{"c"}, []string{"INT4"}, [][]any{{int64(2)}})
}
