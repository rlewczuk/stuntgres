package reference_test

import (
	"context"
	"database/sql"
	"testing"
)

func sequenceProbeExec(t *testing.T, ctx context.Context, c *sql.Conn, q string) {
	t.Helper()
	r, err := c.ExecContext(ctx, q)
	if err != nil {
		t.Fatal(q, err)
	}
	n, err := r.RowsAffected()
	if err != nil || n != 0 {
		t.Fatal(q, n, err)
	}
}

func sequenceProbeValue(t *testing.T, ctx context.Context, c *sql.Conn, q string, want int64) {
	t.Helper()
	var got int64
	if err := c.QueryRowContext(ctx, q).Scan(&got); err != nil || got != want {
		t.Fatalf("%s got=%d want=%d err=%v", q, got, want, err)
	}
}

func sequenceProbeError(t *testing.T, ctx context.Context, c *sql.Conn, q string, want *referenceDiagnostic) {
	t.Helper()
	_, err := c.ExecContext(ctx, q)
	if err == nil {
		t.Fatal("expected error", q)
	}
	if want == nil {
		expected, ok := sequenceExpectedDiagnostic(q)
		if !ok {
			t.Fatal("missing independent diagnostic", q)
		}
		want = &expected
	}
	referenceAssertDiagnostic(t, err, want)
}
