package stuntgres_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"stuntgres"
)

func TestBtreeIndexRestoreRetainedTemporaryCollision(t *testing.T) {
	ctx := context.Background()
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	retained := sequenceConn(t, db)
	// Allocate the retained TEMP namespace/sequence first. Isolated replay's
	// first schema/table reuse those allocator positions, as in the engine fixture.
	sequenceExec(t, retained, "CREATE TEMP SEQUENCE retained CACHE 10")
	sequenceValue(t, retained, "SELECT nextval('retained')", 1)
	const stream = `CREATE SCHEMA portable; CREATE TABLE portable.owner_table(v INT); INSERT INTO portable.owner_table VALUES(7); CREATE INDEX owner_idx ON portable.owner_table(v);`
	if err := stuntgres.Restore(ctx, db, strings.NewReader(stream)); err != nil {
		t.Fatal("indexed Restore with retained TEMP", err)
	}
	sequenceValue(t, retained, "SELECT nextval('retained')", 2)
	execSQL(t, db, "INSERT INTO portable.owner_table VALUES(8)")
	wantTuples(t, db, "SELECT v FROM portable.owner_table ORDER BY v", [][]any{{int64(7)}, {int64(8)}})
	var dump bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &dump); err != nil {
		t.Fatal(err)
	}
	const definition = `CREATE INDEX "owner_idx" ON "portable"."owner_table" USING btree ("v" ASC NULLS LAST);`
	if !strings.Contains(dump.String(), definition) || strings.Contains(dump.String(), `CREATE TEMP`) {
		t.Fatal("wrong remapped definition/TEMP serialization", dump.String())
	}
	execSQL(t, db, "DROP TABLE portable.owner_table")
	wantTuples(t, db, "SELECT relname FROM pg_class WHERE relname='owner_idx'", [][]any{})
	sequenceValue(t, retained, "SELECT nextval('retained')", 3)
}
