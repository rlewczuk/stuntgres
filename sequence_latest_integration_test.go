package stuntgres_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"stuntgres"
	"testing"
)

func TestSequenceLatestIntegrationUpdate(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "Direct", true: "Prepared"}[prepared], func(t *testing.T) {
			ctx := context.Background()
			db := openLocal(t, stuntgres.NewDriver(), t.Name())
			a, b := sequenceConn(t, db), sequenceConn(t, db)
			sequenceExec(t, a, "CREATE SEQUENCE s")
			sequenceExec(t, a, "CREATE TABLE t(id INT,n BIGINT,c CHAR(4),v VARCHAR(3) DEFAULT 'ok')")
			transactionAffected(t, ctx, a, "INSERT INTO t(id,n,c) VALUES(1,0,'é'),(2,0,'x')", false, 2)
			q := "UPDATE t SET n=nextval('s'),c=CAST('é🙂xyz' AS CHAR(4)),v=CAST(CAST(nextval('s') AS TEXT) AS VARCHAR(3)) WHERE id=1"
			transactionAffected(t, ctx, a, q, prepared, 1)
			wantTuples(t, db, "SELECT id,n,c,v FROM t ORDER BY id", [][]any{{int64(1), int64(1), "é🙂xy", "2"}, {int64(2), int64(0), "x   ", "ok"}})
			sequenceValue(t, a, "SELECT nextval('s')", 3)
			for _, commit := range []bool{false, true} {
				tx, err := a.BeginTx(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				transactionAffected(t, ctx, tx, "ALTER SEQUENCE s RESTART 100", prepared, 0)
				transactionAffected(t, ctx, tx, "UPDATE t SET n=nextval('s') WHERE id=2", prepared, 1)
				var n int64
				if err = tx.QueryRowContext(ctx, "SELECT n FROM t WHERE id=2").Scan(&n); err != nil || n != 100 {
					t.Fatal(n, err)
				}
				sequenceValue(t, b, "SELECT n FROM t WHERE id=2", 0)
				if commit {
					err = tx.Commit()
				} else {
					err = tx.Rollback()
				}
				if err != nil {
					t.Fatal(err)
				}
				if commit {
					sequenceValue(t, b, "SELECT n FROM t WHERE id=2", 100)
					sequenceValue(t, b, "SELECT nextval('s')", 101)
				} else {
					sequenceValue(t, b, "SELECT n FROM t WHERE id=2", 0)
					sequenceValue(t, b, "SELECT nextval('s')", 4)
				}
			}
			sequenceExec(t, a, "CREATE TABLE copied(id INT,n BIGINT,c CHAR(4),v VARCHAR(3))")
			transactionAffected(t, ctx, a, "INSERT INTO copied SELECT id,nextval('s'),c,v FROM t ORDER BY id", prepared, 2)
			want := [][]any{{int64(1), int64(102), "é🙂xy", "2"}, {int64(2), int64(103), "x   ", "ok"}}
			wantTuples(t, db, "SELECT * FROM copied ORDER BY id", want)
			characterMetadata(t, db, "SELECT c,v FROM copied", []string{"BPCHAR", "VARCHAR"}, []int64{4, 3})
			var dump bytes.Buffer
			if err := stuntgres.Dump(ctx, db, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(ctx, db); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			wantTuples(t, db, "SELECT * FROM copied ORDER BY id", want)
			characterMetadata(t, db, "SELECT c,v FROM copied", []string{"BPCHAR", "VARCHAR"}, []int64{4, 3})
			sequenceValue(t, a, "SELECT nextval('s')", 104)
		})
	}
}

func TestSequenceLatestIntegrationIsolation(t *testing.T) {
	ctx := context.Background()
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	a, b := sequenceConn(t, db), sequenceConn(t, db)
	sequenceExec(t, a, "CREATE TABLE t(v INT)")
	transactionAffected(t, ctx, a, "INSERT INTO t VALUES(1)", false, 1)
	sequenceExec(t, a, "CREATE SEQUENCE s")
	tx, err := a.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var n int64
	if err = tx.QueryRowContext(ctx, "SELECT v FROM t").Scan(&n); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	transactionAffected(t, ctx, b, "UPDATE t SET v=2", false, 1)
	sequenceValue(t, b, "SELECT nextval('s')", 1)
	if err = tx.QueryRowContext(ctx, "SELECT nextval('s')").Scan(&n); err != nil || n != 2 {
		t.Fatal(n, err)
	}
	if err = tx.QueryRowContext(ctx, "SELECT v FROM t").Scan(&n); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	sequenceValue(t, b, "SELECT v FROM t", 2)
	sequenceValue(t, b, "SELECT nextval('s')", 3)
	sequenceExec(t, a, "CREATE SEQUENCE cached CACHE 5")
	sequenceValue(t, a, "SELECT nextval('cached')", 1)
	sequenceExec(t, a, "CREATE TEMP SEQUENCE local_s START 7")
	for _, q := range []string{"SELECT nextval('cached')", "SELECT setval('cached',0)"} {
		ro, err := a.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
		if err != nil {
			t.Fatal(err)
		}
		err = ro.QueryRowContext(ctx, q).Scan(&n)
		name := "nextval"
		if strings.Contains(q, "setval") {
			name = "setval"
		}
		typeCoreDiagnostic(t, err, stuntgres.Error{Code: "25006", Message: "cannot execute " + name + "() in a read-only transaction"})
		if err = ro.Rollback(); err != nil {
			t.Fatal(err)
		}
	}
	sequenceValue(t, a, "SELECT currval('cached')", 1)
	sequenceValue(t, a, "SELECT last_value FROM cached", 5)
	sequenceValue(t, a, "SELECT nextval('cached')", 2)
	ro, err := a.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Rollback()
	if err = ro.QueryRowContext(ctx, "SELECT nextval('local_s')").Scan(&n); err != nil || n != 7 {
		t.Fatal(n, err)
	}
	if err = ro.QueryRowContext(ctx, "SELECT setval('local_s',9,false)").Scan(&n); err != nil || n != 9 {
		t.Fatal(n, err)
	}
	if err = ro.QueryRowContext(ctx, "SELECT nextval('local_s')").Scan(&n); err != nil || n != 9 {
		t.Fatal(n, err)
	}
	if err = ro.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestSequenceLatestIntegrationDefaultBoundary(t *testing.T) {
	ctx := context.Background()
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	a := sequenceConn(t, db)
	sequenceExec(t, a, "CREATE SEQUENCE s START 17")
	for _, expression := range []string{"nextval(NULL)", "setval('s',nextval('s'),NULL)", "nextval(NULL::regclass)", "lastval()"} {
		q := "CREATE TABLE bad(v BIGINT DEFAULT " + expression + ")"
		_, err := a.ExecContext(ctx, q)
		var state interface{ SQLState() string }
		if !errors.As(err, &state) || state.SQLState() != "0A000" {
			t.Fatalf("excluded DEFAULT succeeded or returned non-capability error: %v", err)
		}
		var n int64
		err = a.QueryRowContext(ctx, "SELECT v FROM bad").Scan(&n)
		typeCoreDiagnostic(t, err, stuntgres.Error{Code: "42P01", Message: `relation "bad" does not exist`, Position: "15"})
	}
	for _, expression := range []string{"NULL::regclass IS NULL", "'s'::regclass IS NULL"} {
		q := "CREATE TABLE bad(v BOOL DEFAULT (" + expression + "))"
		_, err := a.ExecContext(ctx, q)
		var state interface{ SQLState() string }
		if !errors.As(err, &state) || state.SQLState() != "0A000" {
			t.Fatalf("standalone regclass DEFAULT succeeded or returned non-capability error: %v", err)
		}
	}
	sequenceValue(t, a, "SELECT nextval('s')", 17)
}
