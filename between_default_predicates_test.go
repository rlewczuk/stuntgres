package stuntgres_test

import (
	"bytes"
	"context"
	"testing"

	"stuntgres"
)

func TestBetweenPredicateDefaultNullRoundTrip(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE predicates(n BOOL DEFAULT (TRUE BETWEEN (NULL=TRUE) AND TRUE),i BOOL DEFAULT ((NULL IS NULL) BETWEEN FALSE AND TRUE),t BOOL DEFAULT ((TRUE IS TRUE) BETWEEN FALSE AND TRUE),d BOOL DEFAULT ((1 IS DISTINCT FROM 2) BETWEEN FALSE AND TRUE))")
	execSQL(t, db, "INSERT INTO predicates DEFAULT VALUES")
	wantTuples(t, db, "SELECT * FROM predicates", [][]any{{nil, true, true, true}})
	var dump bytes.Buffer
	ctx := context.Background()
	if err := stuntgres.Dump(ctx, db, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	execSQL(t, db, "INSERT INTO predicates DEFAULT VALUES")
	wantTuples(t, db, "SELECT * FROM predicates", [][]any{{nil, true, true, true}, {nil, true, true, true}})
}
