package stuntgres_test

import (
	"bytes"
	"context"
	"errors"
	"stuntgres"
	"testing"
)

func TestAlterTypeNumericControlsAndCanceledReplacement(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(c NUMERIC(8,3))")
	execSQL(t, db, "INSERT INTO t VALUES(1.235)")
	execSQL(t, db, "ALTER TABLE t ALTER c TYPE NUMERIC(5,2)")
	var dump bytes.Buffer
	if err := stuntgres.Dump(t.Context(), db, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(t.Context(), db, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	alterTypePublicRows(t, db, "SELECT c FROM t", []string{"c"}, []string{"NUMERIC"}, [][]any{{"1.24"}})
	held, err := db.Query("SELECT c FROM t")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	metadata, err := held.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if p, s, ok := metadata[0].DecimalSize(); !ok || p != 5 || s != 2 {
		t.Fatal(p, s, ok)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	if err = stuntgres.Restore(ctx, db, &cancelReader{cancel: cancel}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	alterTypePublicRows(t, db, "SELECT c FROM t", []string{"c"}, []string{"NUMERIC"}, [][]any{{"1.24"}})
	if !held.Next() {
		t.Fatal("missing eager row", held.Err())
	}
	var value string
	if err = held.Scan(&value); err != nil || value != "1.24" {
		t.Fatal(value, err)
	}
	if held.Next() || held.Err() != nil {
		t.Fatal("changed eager result", held.Err())
	}
	var again bytes.Buffer
	if err = stuntgres.Dump(t.Context(), db, &again); err != nil || again.String() != dump.String() {
		t.Fatal(err, again.String(), dump.String())
	}
	execSQL(t, db, "INSERT INTO t VALUES(1.235)")
	alterTypePublicRows(t, db, "SELECT c FROM t", []string{"c"}, []string{"NUMERIC"}, [][]any{{"1.24"}, {"1.24"}})
}
