package stuntgres_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"stuntgres"
	"testing"
)

func TestLikeControlsAndAtomicity(t *testing.T) {
	ctx := context.Background()
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(id INTEGER,v TEXT,c CHAR(3),b BYTEA,p TEXT COLLATE \"C\",CONSTRAINT starts_a CHECK(v ILIKE 'a%' ESCAPE '!'))")
	execSQL(t, db, "INSERT INTO t VALUES(1,'Ab','a','\\xc3a9','a%'),(2,NULL,NULL,NULL,NULL)")
	execSQL(t, db, "CREATE VIEW matched AS SELECT id,v ILIKE p ESCAPE '!' AS matched,c OPERATOR(pg_catalog.~~) 'a__' AS padded,b LIKE '\\x5f5f' AS octets FROM t")
	query := "SELECT matched,padded,octets FROM matched ORDER BY id"
	rows, err := db.Query(query)
	compareOpsRows(t, rows, err, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true}, {nil, nil, nil}})
	stmt, err := db.Prepare(query)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	execSQL(t, db, "ALTER TABLE t RENAME COLUMN v TO value")
	rows, err = stmt.Query()
	compareOpsRows(t, rows, err, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true}, {nil, nil, nil}})
	if _, err := db.Exec("INSERT INTO t VALUES(3,'ab','a','\\x61','a%'),(4,'b','b','\\x62','b%')"); err == nil {
		t.Fatal("late CHECK failure accepted")
	}
	if _, err := db.Exec("UPDATE t SET value='b'"); err == nil {
		t.Fatal("bad UPDATE accepted")
	}
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM t").Scan(&count); err != nil || count != 2 {
		t.Fatal(count, err)
	}
	old, err := db.Query(query)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
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
	compareOpsRows(t, old, nil, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true}, {nil, nil, nil}})
	rows, err = db.Query(query)
	compareOpsRows(t, rows, err, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true}, {nil, nil, nil}})
	var redump bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &redump); err != nil || !bytes.Equal(dump.Bytes(), redump.Bytes()) {
		t.Fatal("unstable replay", err)
	}
	bad := dump.String() + "INSERT INTO t VALUES(3,'b','b','\\x62','b%');"
	if err := stuntgres.Restore(ctx, db, strings.NewReader(bad)); err == nil {
		t.Fatal("failed replay accepted")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := stuntgres.Restore(canceled, db, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	var after bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &after); err != nil || !bytes.Equal(redump.Bytes(), after.Bytes()) {
		t.Fatal("failed replay published", err)
	}
	if _, err := db.QueryContext(canceled, "SELECT value LIKE p FROM t"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(canceled, "UPDATE t SET value='ab'"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
