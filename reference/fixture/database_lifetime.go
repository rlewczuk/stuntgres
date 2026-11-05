package fixture

import (
	"context"
	"runtime"
	"testing"
)

// DatabaseReferenceOwners observes actual public physical owners, not sql.Conn
// borrowers. In particular, Close on a borrower alone does not prove zero.
// The bounded exit barrier accommodates PostgreSQL processing a closed socket.
func DatabaseReferenceOwners(t *testing.T, p *Pair, observer Executor, recorder *Recorder, database string, want int64) {
	t.Helper()
	ctx, cancel := context.WithTimeout(p.Ctx, Liveness)
	defer cancel()
	const query = "SELECT count(*) AS owners FROM pg_stat_activity WHERE datname=$1"
	for {
		rows, err := observer.QueryContext(ctx, query, database)
		if err != nil {
			t.Fatal("database physical-owner observation", err)
		}
		var got int64
		if !rows.Next() {
			rows.Close()
			t.Fatal("missing physical-owner count", rows.Err())
		}
		if err := rows.Scan(&got); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		if rows.Next() || rows.Err() != nil {
			rows.Close()
			t.Fatal("invalid physical-owner count", rows.Err())
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		if got == want {
			break
		}
		if err := ctx.Err(); err != nil {
			t.Fatalf("database %q physical owners = %d, want %d: %v", database, got, want, err)
		}
		runtime.Gosched()
	}
	RunStep(t, p.Ctx, observer, recorder, Select(query, []string{"owners"}, []string{"INT8"}, [][]any{{want}}, database))
}
