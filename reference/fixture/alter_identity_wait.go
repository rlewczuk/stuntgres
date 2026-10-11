package fixture

import (
	"context"
	"database/sql"
	"runtime"
	"testing"
)

func AlterIdentityObserveAX(t *testing.T, ctx context.Context, c *sql.Conn, done <-chan error) {
	t.Helper()
	for {
		select {
		case err := <-done:
			t.Fatal("ALTER ended before wait observation", err)
		case <-ctx.Done():
			t.Fatal("AX not observed", ctx.Err())
		default:
		}
		tx, err := c.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		_, err = tx.ExecContext(ctx, `LOCK TABLE i IN ACCESS SHARE MODE NOWAIT`)
		if e := tx.Rollback(); e != nil {
			t.Fatal(e)
		}
		if err != nil {
			want := ControlsReferenceDiagnostic("55P03", `could not obtain lock on relation "i"`)
			AssertDiagnostic(t, err, &want)
			return
		}
		runtime.Gosched()
	}
}
