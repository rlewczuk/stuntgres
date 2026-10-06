package stuntgres_test

import (
	"context"
	"stuntgres"
	"testing"
)

func TestAlterRenameLocks(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	db.SetMaxOpenConns(3)
	execSQL(t, db, "CREATE TABLE t(a INT)")
	a, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	tx, err := a.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.Exec("LOCK TABLE t IN ACCESS EXCLUSIVE MODE"); err != nil {
		t.Fatal(err)
	}
	if _, err = b.ExecContext(context.Background(), "SET lock_timeout='20ms'"); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"ALTER TABLE t RENAME a TO renamed", "ALTER TABLE t RENAME TO newtable"} {
		s, err := b.PrepareContext(context.Background(), q)
		if err != nil {
			t.Fatal("Prepare should not acquire AX", err)
		}
		_, err = s.Exec()
		s.Close()
		alterDropDiagnostic(t, err, "55P03", "canceling statement due to lock timeout", "")
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	wantTuples(t, db, "SELECT * FROM t", [][]any{})
}
