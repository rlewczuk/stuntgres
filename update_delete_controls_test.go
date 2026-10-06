package stuntgres_test

import (
	"bytes"
	"context"
	"strings"
	"stuntgres"
	"testing"
	"time"
)

func TestUpdateCoreDeleteControls(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	transactionAffected(t, ctx, db, "CREATE TABLE t(id INT,v INT)", false, 0)
	transactionAffected(t, ctx, db, "INSERT INTO t VALUES(1,10),(2,20),(2,20)", false, 3)
	retained, err := db.QueryContext(ctx, "SELECT id,v FROM t")
	if err != nil {
		t.Fatal(err)
	}
	defer retained.Close()
	stmt, err := db.PrepareContext(ctx, "UPDATE t SET v=$1 WHERE id=$2")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	transactionAffected(t, ctx, tx, "UPDATE t SET v=30 WHERE id=1", false, 1)
	transactionAffected(t, ctx, tx, "DELETE FROM t WHERE v=20", true, 2)
	transactionAffected(t, ctx, tx, "INSERT INTO t VALUES(3,30)", false, 1)
	transactionAffected(t, ctx, tx, "UPDATE t SET v=40 WHERE id=3", false, 1)
	transactionAffected(t, ctx, tx, "DELETE FROM t WHERE id=3", true, 1)
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	live := [][]any{{int64(1), int64(30)}}
	updateDeleteRows(t, ctx, db, "SELECT id,v FROM t", live)
	var dump bytes.Buffer
	if err = stuntgres.Dump(ctx, db, &dump); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(dump.String(), "DELETE") || strings.Contains(dump.String(), "UPDATE") || strings.Contains(dump.String(), "(2, 20)") || strings.Contains(dump.String(), "(3,") {
		t.Fatal("dump contains ended/private history", dump.String())
	}
	if err = stuntgres.Restore(ctx, db, strings.NewReader("CREATE TABLE broken(v INT);DELETE FROM broken;")); err == nil {
		t.Fatal("Restore admitted DML history")
	}
	updateDeleteRows(t, ctx, db, "SELECT id,v FROM t", live)
	if err = stuntgres.Reset(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err = stuntgres.Restore(ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	updateDeleteRows(t, ctx, db, "SELECT id,v FROM t", live)
	var again bytes.Buffer
	if err = stuntgres.Dump(ctx, db, &again); err != nil || again.String() != dump.String() {
		t.Fatal("live-only canonical round trip", again.String(), err)
	}
	queryCoreRows(t, retained, nil, []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}, {int64(2), int64(20)}}, true)
	result, err := stmt.ExecContext(ctx, 31, 1)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	updateDeleteRows(t, ctx, db, "SELECT id,v FROM t", [][]any{{int64(1), int64(31)}})
}
