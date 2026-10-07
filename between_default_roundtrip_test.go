package stuntgres_test

import (
	"bytes"
	"context"
	"testing"

	"stuntgres"
)

func TestBetweenGroupedDefaultRoundTrip(t *testing.T) {
	ctx := context.Background()
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE sentinel(i INTEGER)")
	execSQL(t, db, "INSERT INTO sentinel VALUES(7)")
	execSQL(t, db, "CREATE TABLE grouped_defaults(b BOOL DEFAULT (TRUE BETWEEN (1<2) AND (2<3)),a BOOL DEFAULT ((TRUE OR FALSE) BETWEEN FALSE AND TRUE),s BOOL DEFAULT (TRUE BETWEEN SYMMETRIC TRUE AND (2 BETWEEN 1 AND 3)),n BOOL DEFAULT (TRUE NOT BETWEEN SYMMETRIC (FALSE OR TRUE) AND (1 BETWEEN 0 AND 2)),o BOOL DEFAULT (TRUE NOT BETWEEN (1 BETWEEN 0 AND 2) AND TRUE))")
	execSQL(t, db, "CREATE TABLE deferred(b BOOL DEFAULT (TRUE BETWEEN (1/0=0) AND TRUE))")
	execSQL(t, db, "INSERT INTO grouped_defaults DEFAULT VALUES")
	want := [][]any{{true, true, true, false, false}}
	wantTuples(t, db, "SELECT * FROM grouped_defaults", want)
	var dump bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(ctx, db); err != nil {
		t.Fatal(err)
	}
	execSQL(t, db, "CREATE TABLE sentinel(i INTEGER)")
	execSQL(t, db, "INSERT INTO sentinel VALUES(7)")
	if err := stuntgres.Restore(ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
		wantTuples(t, db, "SELECT i FROM sentinel", [][]any{{int64(7)}})
		t.Fatal("grouped BETWEEN dump cannot be restored", err)
	}
	wantTuples(t, db, "SELECT * FROM grouped_defaults", want)
	execSQL(t, db, "INSERT INTO grouped_defaults DEFAULT VALUES")
	wantTuples(t, db, "SELECT * FROM grouped_defaults", [][]any{{true, true, true, false, false}, {true, true, true, false, false}})
	wantTuples(t, db, "SELECT i FROM sentinel", [][]any{{int64(7)}})
	_, err := db.Exec("INSERT INTO deferred DEFAULT VALUES")
	typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22012", Message: "division by zero"})
	wantTuples(t, db, "SELECT * FROM deferred", [][]any{})
	wantTuples(t, db, "SELECT i FROM sentinel", [][]any{{int64(7)}})
}
