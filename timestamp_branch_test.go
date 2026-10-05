package stuntgres_test

import (
	"bytes"
	"context"
	"strings"
	"stuntgres"
	"testing"
	"time"
)

func TestTimestampBranchErrorsAbortTransaction(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(v TIMESTAMP)")
	execSQL(t, db, "INSERT INTO t VALUES('epoch')")
	for _, tc := range timestampBadInputs {
		t.Run(tc.input, func(t *testing.T) {
			tx, err := db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			_, err = tx.Exec("INSERT INTO t VALUES('2000-01-01'),($1)", tc.input)
			typeCoreDiagnostic(t, err, stuntgres.Error{Code: tc.code, Message: tc.message, Hint: tc.hint})
			for _, q := range []string{"SELECT v FROM t", "INSERT INTO t VALUES('epoch')"} {
				_, err = tx.Exec(q)
				typeCoreDiagnostic(t, err, stuntgres.Error{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"})
			}
			if err = tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			timestampQuery(t, db, "SELECT v FROM t", []string{"v"}, []string{"TIMESTAMP"}, [][]any{{time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)}})
		})
	}
}

func TestTimestampBranchCanonicalControlsAndComparison(t *testing.T) {
	ctx := context.Background()
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(id INTEGER,v TIMESTAMP(6))")
	execSQL(t, db, "INSERT INTO t VALUES(1,'on - InFiNiTy at'),(2,'on epoch at'),(3,'1999-01-08 250000'),(4,'January 8 04:05:06 120000 PST'),(5,'+ InFiNiTy at'),(6,NULL)")
	want := [][]any{{int64(1), []byte("-infinity")}, {int64(2), time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)}, {int64(3), time.Date(1999, 1, 9, 1, 0, 0, 0, time.UTC)}, {int64(4), time.Date(120000, 1, 8, 4, 5, 6, 0, time.UTC)}, {int64(5), []byte("infinity")}, {int64(6), nil}}
	check := func() {
		timestampQuery(t, db, "SELECT id,v FROM t ORDER BY v", []string{"id", "v"}, []string{"INT4", "TIMESTAMP"}, want)
		timestampQuery(t, db, "SELECT id FROM t WHERE v='120000-01-08 04:05:06'", []string{"id"}, []string{"INT4"}, [][]any{{int64(4)}})
		timestampQuery(t, db, "SELECT id FROM t WHERE v=$1", []string{"id"}, []string{"INT4"}, nil, nil)
	}
	check()
	var dump, again bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &dump); err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{"'120000-01-08 04:05:06'", "'1999-01-09 01:00:00'", "'infinity'", "'-infinity'", "TIMESTAMP(6)"} {
		if !strings.Contains(dump.String(), fragment) {
			t.Fatal(fragment, dump.String())
		}
	}
	if err := stuntgres.Reset(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Dump(ctx, db, &again); err != nil || again.String() != dump.String() {
		t.Fatal(again.String(), err)
	}
	check()
	var infinity []byte
	if err := db.QueryRowContext(ctx, "SELECT TIMESTAMP 'on infinity at' FROM t WHERE id=1").Scan(&infinity); err != nil {
		t.Fatal(err)
	}
	infinity[0] = 'X'
	var original []byte
	if err := db.QueryRowContext(ctx, "SELECT TIMESTAMP 'on infinity at' FROM t WHERE id=1").Scan(&original); err != nil || string(original) != "infinity" {
		t.Fatal(string(original), err)
	}
}

func TestTimestampNoisyNowPhysicalPrepare(t *testing.T) {
	ctx := context.Background()
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(v TIMESTAMP)")
	execSQL(t, db, "INSERT INTO t VALUES('epoch')")
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	stmt, err := conn.PrepareContext(ctx, "SELECT TIMESTAMP 'on NoW at' FROM t")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	var first, held time.Time
	if err = stmt.QueryRowContext(ctx).Scan(&first); err != nil {
		t.Fatal(err)
	}
	time.Sleep(3 * time.Millisecond)
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err = stmt.QueryRowContext(ctx).Scan(&held); err != nil || held != first {
		t.Fatal(first, held, err)
	}
	var ordinary, noisy time.Time
	if err = tx.QueryRowContext(ctx, "SELECT TIMESTAMP 'now',TIMESTAMP 'on NoW at' FROM t").Scan(&ordinary, &noisy); err != nil || ordinary != noisy || !ordinary.After(first) {
		t.Fatal(first, ordinary, noisy, err)
	}
	fresh := tx.StmtContext(ctx, stmt)
	defer fresh.Close()
	if err = fresh.QueryRowContext(ctx).Scan(&held); err != nil || held != ordinary {
		t.Fatal(held, ordinary, err)
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err = stmt.QueryRowContext(ctx).Scan(&held); err != nil || held != first {
		t.Fatal(held, first, err)
	}
}
