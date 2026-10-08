package smoke_test

import (
	"bytes"
	"context"
	"strings"
	"stuntgres"
	"testing"
)

func TestAlterTypeSmoke(t *testing.T) {
	var notices []stuntgres.Error
	db := alterDropSmokePool(t, &notices)
	alterDropSmokeExec(t, db, "CREATE TABLE t(id INT,c TEXT)")
	alterDropSmokeExec(t, db, "INSERT INTO t VALUES(1,'41'),(2,NULL),(3,'7')")
	held, err := db.Query("SELECT c FROM t ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	old, err := db.Prepare("SELECT c FROM t ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	utility, err := db.Prepare("ALTER TABLE t ALTER c TYPE INT USING c::integer")
	if err != nil {
		t.Fatal(err)
	}
	result, err := utility.Exec()
	utility.Close()
	if err != nil {
		t.Fatal(err)
	}
	if n, e := result.RowsAffected(); e != nil || n != 0 {
		t.Fatal(n, e)
	}
	rows, err := db.Query("SELECT c FROM t ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	alterDropSmokeRows(t, rows, []string{"c"}, []string{"INT4"}, [][]any{{int64(41)}, {nil}, {int64(7)}})
	_, err = old.Query()
	alterDropSmokeError(t, err, "0A000", "cached plan must not change result type", "")
	alterDropSmokeRows(t, held, []string{"c"}, []string{"TEXT"}, [][]any{{"41"}, {nil}, {"7"}})
	alterDropSmokeExec(t, db, "CREATE TABLE bad(c TEXT)")
	alterDropSmokeExec(t, db, "INSERT INTO bad VALUES('41'),('oops')")
	_, err = db.Exec("ALTER TABLE bad ALTER c TYPE INT USING c::int")
	alterDropSmokeError(t, err, "22P02", `invalid input syntax for type integer: "oops"`, "")
	rows, err = db.Query("SELECT c FROM bad ORDER BY c")
	if err != nil {
		t.Fatal(err)
	}
	alterDropSmokeRows(t, rows, []string{"c"}, []string{"TEXT"}, [][]any{{"41"}, {"oops"}})
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	retainedInsert, err := tx.Prepare("INSERT INTO t VALUES(4,$1)")
	if err != nil {
		t.Fatal(err)
	}
	defer retainedInsert.Close()
	if _, err = tx.Exec("ALTER TABLE t ALTER c TYPE BIGINT"); err != nil {
		t.Fatal(err)
	}
	rows, err = tx.Query("SELECT c FROM t ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	alterDropSmokeRows(t, rows, []string{"c"}, []string{"INT8"}, [][]any{{int64(41)}, {nil}, {int64(7)}})
	if _, err = retainedInsert.Exec(int64(11)); err != nil {
		t.Fatal("retained input contract", err)
	}
	rows, err = tx.Query("SELECT c FROM t WHERE id=4")
	if err != nil {
		t.Fatal(err)
	}
	alterDropSmokeRows(t, rows, []string{"c"}, []string{"INT8"}, [][]any{{int64(11)}})
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	rows, err = db.Query("SELECT c FROM t ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	alterDropSmokeRows(t, rows, []string{"c"}, []string{"INT4"}, [][]any{{int64(41)}, {nil}, {int64(7)}})
	alterDropSmokeExec(t, db, "CREATE TABLE original(a INT,b INT,c INT)")
	alterDropSmokeExec(t, db, "INSERT INTO original VALUES(2,7,9)")
	alterDropSmokeExec(t, db, "ALTER TABLE original ALTER a TYPE TEXT USING b, ALTER b TYPE BIGINT USING a*10, DROP c")
	rows, err = db.Query("SELECT * FROM original")
	if err != nil {
		t.Fatal(err)
	}
	alterDropSmokeRows(t, rows, []string{"a", "b"}, []string{"TEXT", "INT8"}, [][]any{{"7", int64(20)}})
}

func TestAlterTypeControlsSmoke(t *testing.T) {
	var notices []stuntgres.Error
	db := alterDropSmokePool(t, &notices)
	ctx := context.Background()
	alterDropSmokeExec(t, db, "CREATE TABLE t(id INT,c TEXT,n INT DEFAULT 7 CONSTRAINT n_nn NOT NULL)")
	alterDropSmokeExec(t, db, "INSERT INTO t VALUES(1,'a',7),(2,'Z',8)")
	alterDropSmokeExec(t, db, "COMMENT ON COLUMN t.n IS 'retained'")
	alterDropSmokeExec(t, db, `ALTER TABLE t ALTER c TYPE TEXT COLLATE pg_catalog."POSIX", ALTER n TYPE BIGINT USING n+1`)
	alterDropSmokeExec(t, db, "INSERT INTO t(id,c) VALUES(3,'b')")
	var dump bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &dump); err != nil {
		t.Fatal(err)
	}
	for _, literal := range []string{"BIGINT", `COLLATE "pg_catalog"."POSIX"`, "n_nn", "retained"} {
		if !strings.Contains(dump.String(), literal) {
			t.Fatal(literal, dump.String())
		}
	}
	if strings.Contains(dump.String(), "ALTER TABLE") {
		t.Fatal("dumped action history", dump.String())
	}
	held, err := db.Query("SELECT n FROM t ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	if err = stuntgres.Reset(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err = stuntgres.Restore(ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	var again bytes.Buffer
	if err = stuntgres.Dump(ctx, db, &again); err != nil || again.String() != dump.String() {
		t.Fatal(err, again.String(), dump.String())
	}
	rows, err := db.Query("SELECT c,n FROM t ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	alterDropSmokeRows(t, rows, []string{"c", "n"}, []string{"TEXT", "INT8"}, [][]any{{"a", int64(8)}, {"Z", int64(9)}, {"b", int64(7)}})
	alterDropSmokeRows(t, held, []string{"n"}, []string{"INT8"}, [][]any{{int64(8)}, {int64(9)}, {int64(7)}})
	alterDropSmokeExec(t, db, "INSERT INTO t(id,c) VALUES(4,'default')")
	var n int64
	if err = db.QueryRow("SELECT n FROM t WHERE id=4").Scan(&n); err != nil || n != 7 {
		t.Fatal(n, err)
	}
	if _, err = db.Exec("INSERT INTO t VALUES(5,'bad',NULL)"); err == nil {
		t.Fatal("lost NOT NULL")
	}
	if err = stuntgres.Restore(ctx, db, strings.NewReader("CREATE TABLE broken(")); err == nil {
		t.Fatal("accepted invalid Restore")
	}
	rows, err = db.Query("SELECT n FROM t ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	alterDropSmokeRows(t, rows, []string{"n"}, []string{"INT8"}, [][]any{{int64(8)}, {int64(9)}, {int64(7)}, {int64(7)}})
}
