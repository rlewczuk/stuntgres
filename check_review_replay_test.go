package stuntgres_test

import (
	"bytes"
	"context"
	"strings"
	"stuntgres"
	"testing"
)

func TestCheckReviewReplayOwnedSequenceRoundTrip(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	db.SetMaxOpenConns(1)
	ctx := context.Background()
	execSQL(t, db, "CREATE SEQUENCE s MINVALUE -10 START 1")
	execSQL(t, db, "CREATE TABLE t(a INT CONSTRAINT c CHECK(nextval('s')>0))")
	execSQL(t, db, "ALTER SEQUENCE s OWNED BY t.a")
	execSQL(t, db, "INSERT INTO t VALUES(11),(22)")
	wantTuples(t, db, "SELECT currval('s')", [][]any{{int64(2)}})
	var dump, again bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &dump); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dump.String(), `OWNED BY "public"."t"."a"`) {
		t.Fatal("missing ownership", dump.String())
	}
	if err := stuntgres.Restore(ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	wantTuples(t, db, "SELECT a FROM t ORDER BY a", [][]any{{int64(11)}, {int64(22)}})
	// Redump before any nextval/setval changes the restored generator.
	if err := stuntgres.Dump(ctx, db, &again); err != nil || !bytes.Equal(dump.Bytes(), again.Bytes()) {
		t.Fatal("definition/data/state/ownership changed on replay", err, dump.String(), again.String())
	}
	wantTuples(t, db, "SELECT nextval('s')", [][]any{{int64(3)}})
	wantTuples(t, db, "SELECT setval('s',-1)", [][]any{{int64(-1)}})
	checkExecError(t, db, true, "INSERT INTO t VALUES($1)", checkViolation("t", "c", "33"), 33)
	wantTuples(t, db, "SELECT currval('s')", [][]any{{int64(0)}})
	wantTuples(t, db, "SELECT a FROM t ORDER BY a", [][]any{{int64(11)}, {int64(22)}})
	execSQL(t, db, "DROP TABLE t")
	_, err := db.Query("SELECT nextval('s')")
	sqlState(t, err, "42P01")
}

func TestCheckReviewReplayHistoryPublicRestore(t *testing.T) {
	ctx := context.Background()
	source := openLocal(t, stuntgres.NewDriver(), t.Name()+"source")
	source.SetMaxOpenConns(1)
	execSQL(t, source, "CREATE SEQUENCE s START 1")
	execSQL(t, source, "CREATE TABLE t(a INT CONSTRAINT c CHECK(nextval('s')=2))")
	wantTuples(t, source, "SELECT nextval('s')", [][]any{{int64(1)}})
	execSQL(t, source, "INSERT INTO t VALUES(9)")
	var dump bytes.Buffer
	if err := stuntgres.Dump(ctx, source, &dump); err != nil {
		t.Fatal(err)
	}
	wantTuples(t, source, "SELECT currval('s')", [][]any{{int64(2)}})
	wantTuples(t, source, "SELECT nextval('s')", [][]any{{int64(3)}})
	target := openLocal(t, stuntgres.NewDriver(), t.Name()+"target")
	execSQL(t, target, "CREATE TABLE sentinel(a INT)")
	execSQL(t, target, "INSERT INTO sentinel VALUES(7)")
	saved, err := target.Query("SELECT a FROM sentinel")
	if err != nil {
		t.Fatal(err)
	}
	defer saved.Close()
	types, err := saved.ColumnTypes()
	if err != nil || len(types) != 1 || types[0].Name() != "a" || types[0].DatabaseTypeName() != "INT4" {
		t.Fatal(types, err)
	}
	if nullable, known := types[0].Nullable(); !nullable || !known {
		t.Fatal("lost sentinel nullability")
	}
	var before, after bytes.Buffer
	if err := stuntgres.Dump(ctx, target, &before); err != nil {
		t.Fatal(err)
	}
	err = stuntgres.Restore(ctx, target, bytes.NewReader(dump.Bytes()))
	notNullDiagnostic(t, err, checkViolation("t", "c", "9"))
	if err := stuntgres.Dump(ctx, target, &after); err != nil || !bytes.Equal(before.Bytes(), after.Bytes()) {
		t.Fatal("failed history replay changed destination", err)
	}
	wantTuples(t, target, "SELECT a FROM sentinel", [][]any{{int64(7)}})
	if !saved.Next() {
		t.Fatal("lost retained sentinel cursor", saved.Err())
	}
	var value int64
	if err := saved.Scan(&value); err != nil || value != 7 {
		t.Fatal(value, err)
	}
	if saved.Next() || saved.Err() != nil {
		t.Fatal("changed retained cursor", saved.Err())
	}
}
