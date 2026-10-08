package stuntgres_test

import (
	"bytes"
	"context"
	"strings"
	"stuntgres"
	"testing"
)

func TestAlterNullabilityControls(t *testing.T) {
	ctx := context.Background()
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	db.SetMaxOpenConns(4)
	execSQL(t, db, "CREATE TABLE t(a INT,b TEXT DEFAULT 'default')")
	execSQL(t, db, "INSERT INTO t VALUES(7,'seed')")
	execSQL(t, db, "COMMENT ON COLUMN t.a IS 'keep'")
	dump := func() string {
		t.Helper()
		var b bytes.Buffer
		if err := stuntgres.Dump(ctx, db, &b); err != nil {
			t.Fatal(err)
		}
		return b.String()
	}
	original := dump()
	held, err := db.Query("SELECT * FROM t")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`ALTER TABLE t ADD CONSTRAINT "n""é" NOT NULL a, ALTER b SET NOT NULL`); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec("INSERT INTO t VALUES(8,'private')"); err != nil {
		t.Fatal(err)
	}
	if got := dump(); got != original {
		t.Fatal("private descriptor/rows leaked", got)
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if got := dump(); got != original {
		t.Fatal("rollback changed dump", got)
	}
	tx, err = db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(`ALTER TABLE t ADD CONSTRAINT "n""é" NOT NULL a, ALTER b SET NOT NULL`); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	committed := dump()
	if committed == original {
		t.Fatal("committed metadata absent")
	}
	if err = stuntgres.Reset(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err = stuntgres.Restore(ctx, db, strings.NewReader(committed)); err != nil {
		t.Fatal(err)
	}
	if got := dump(); got != committed {
		t.Fatal("logical roundtrip changed", got)
	}
	if !held.Next() {
		t.Fatal(held.Err())
	}
	var a int64
	var b string
	if err = held.Scan(&a, &b); err != nil || a != 7 || b != "seed" {
		t.Fatal(a, b, err)
	}
	held.Close()
	tableCoreExec(t, db, true, `ALTER TABLE t DROP CONSTRAINT "n""é", DROP CONSTRAINT t_b_not_null`, 0)
	tableCoreExec(t, db, false, "INSERT INTO t VALUES(NULL,NULL)", 1)
	sentinel := dump()
	err = stuntgres.Restore(ctx, db, strings.NewReader("CREATE TABLE replacement(a INT NOT NULL);INSERT INTO replacement VALUES(NULL)"))
	notNullDiagnostic(t, err, stuntgres.Error{Code: "23502", Message: `null value in column "a" of relation "replacement" violates not-null constraint`, Detail: "Failing row contains (null).", Schema: "public", Table: "replacement", Column: "a"})
	if got := dump(); got != sentinel {
		t.Fatal("failed restore changed sentinel")
	}
	tx, err = db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec("DELETE FROM t WHERE a IS NULL"); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec("ALTER TABLE t ALTER a SET NOT NULL"); err != nil {
		t.Fatal(err)
	}
	if err = stuntgres.Reset(ctx, db); err != nil {
		t.Fatal(err)
	}
	transactionOrdinary(t, tx.Commit(), "stuntgres: catalog replaced before publication")
}
