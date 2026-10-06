package smoke_test

import (
	"context"
	"testing"
	"time"

	"stuntgres"
)

func TestRelationLockWaitsSmokeTimeoutPosition(t *testing.T) {
	a, b := lockWaitsSmokeConnections(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := b.ExecContext(ctx, "SET lock_timeout='40ms'"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ query, position string }{
		{"SELECT v FROM t", "15"},
		{"/*é*/ SELECT v FROM public.t", "21"},
		{"INSERT INTO t VALUES(2)", "13"},
	} {
		cached, err := b.PrepareContext(ctx, tc.query)
		if err != nil {
			t.Fatal(err)
		}
		defer cached.Close()
		holder, err := a.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer holder.Rollback()
		if _, err = holder.ExecContext(ctx, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE"); err != nil {
			t.Fatal(err)
		}
		want := stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "55P03", Message: "canceling statement due to lock timeout", Position: tc.position}
		_, err = b.ExecContext(ctx, tc.query)
		if mismatch := transactionDiagnostic(err, want); mismatch != nil {
			t.Fatal(mismatch)
		}
		stmt, err := b.PrepareContext(ctx, tc.query)
		if stmt != nil {
			stmt.Close()
		}
		if mismatch := transactionDiagnostic(err, want); mismatch != nil {
			t.Fatal(mismatch)
		}
		rows, err := cached.QueryContext(ctx)
		if rows != nil {
			rows.Close()
		}
		want.Position = ""
		if mismatch := transactionDiagnostic(err, want); mismatch != nil {
			t.Fatal(mismatch)
		}
		if err = holder.Rollback(); err != nil {
			t.Fatal(err)
		}
	}
}
