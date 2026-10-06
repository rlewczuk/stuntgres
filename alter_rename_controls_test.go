package stuntgres_test

import (
	"bytes"
	"context"
	"strings"
	"stuntgres"
	"testing"
)

func TestAlterRenameControlsOwner(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	db.SetMaxOpenConns(1)
	for _, q := range []string{"CREATE USER rename_control_owner", "CREATE SCHEMA s AUTHORIZATION rename_control_owner", "SET SESSION AUTHORIZATION rename_control_owner", "CREATE TABLE s.t(a INT CONSTRAINT nn NOT NULL,b TEXT)", "INSERT INTO s.t VALUES(7,'O''Brien'),(8,NULL)", "COMMENT ON TABLE s.t IS 'table comment'", "COMMENT ON COLUMN s.t.a IS 'column comment'", "ALTER TABLE s.t RENAME a TO renamed", "ALTER TABLE s.t RENAME TO newtable", "RESET SESSION AUTHORIZATION"} {
		execSQL(t, db, q)
	}
	var before bytes.Buffer
	if err := stuntgres.Dump(context.Background(), db, &before); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(before.String(), `CREATE SCHEMA "s" AUTHORIZATION "rename_control_owner";`) || !strings.Contains(before.String(), `SET SESSION AUTHORIZATION 'rename_control_owner';`) {
		t.Fatal(before.String())
	}
	if err := stuntgres.Reset(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(context.Background(), db, bytes.NewReader(before.Bytes())); err != nil {
		t.Fatal(err)
	}
	execSQL(t, db, "SET SESSION AUTHORIZATION rename_control_owner")
	wantTuples(t, db, "SELECT * FROM s.newtable ORDER BY renamed", [][]any{{int64(7), "O'Brien"}, {int64(8), nil}})
	execSQL(t, db, "COMMENT ON COLUMN s.newtable.renamed IS 'column comment'")
	execSQL(t, db, "RESET SESSION AUTHORIZATION")
	var after bytes.Buffer
	if err := stuntgres.Dump(context.Background(), db, &after); err != nil || after.String() != before.String() {
		t.Fatal(err, after.String())
	}
}
