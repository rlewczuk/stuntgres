package stuntgres_test

import (
	"context"
	"stuntgres"
	"testing"
)

func TestSequenceSignaturesAndReplacementEpoch(t *testing.T) {
	ctx := context.Background()
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	c := sequenceConn(t, db)
	sequenceExec(t, c, "CREATE SEQUENCE s")
	for _, tc := range []struct{ q, signature string }{{"SELECT nextval()", "nextval()"}, {"SELECT lastval(1)", "lastval(integer)"}, {"SELECT setval('s')", "setval(unknown)"}, {"SELECT nextval(true)", "nextval(boolean)"}, {"SELECT setval('s',true)", "setval(unknown, boolean)"}} {
		want := stuntgres.Error{Code: "42883", Message: "function " + tc.signature + " does not exist", Hint: "No function matches the given name and argument types. You might need to add explicit type casts.", Position: "8"}
		_, err := c.ExecContext(ctx, tc.q)
		typeCoreDiagnostic(t, err, want)
		stmt, err := c.PrepareContext(ctx, tc.q)
		if stmt != nil {
			stmt.Close()
		}
		typeCoreDiagnostic(t, err, want)
	}
	held, err := c.PrepareContext(ctx, "SELECT nextval('s')")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	if err = stuntgres.Reset(ctx, db); err != nil {
		t.Fatal(err)
	}
	sequenceExec(t, c, "CREATE SEQUENCE s START 50")
	var v int64
	if err = held.QueryRowContext(ctx).Scan(&v); err == nil {
		t.Fatal("old prepared identity crossed replacement epoch", v)
	}
	sequenceValue(t, c, "SELECT nextval('s')", 50)
}
