package reference_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

// Retained pg_locks bags and held-leaf admission are separate observations.
// The PostgreSQL observer is supplemental: native exact-resource admission is
// proved against the real private manager, never a fictitious public catalog.
func partitionAttachProofLeafWait(t *testing.T, c partitionAttachProofCase) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		if !b.PG {
			continue
		} // PG-only physical lock observation; not native SQL support.
		t.Run(b.Name, func(t *testing.T) {
			conn, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			q, leaves := partitionAttachProofSetup(t, p, b, c, conn)
			reader, err := b.DB.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Rollback()
			value := int64(1)
			if c.defaultBranch {
				value = 4
			}
			fixture.RunStep(t, p.Ctx, reader, b.Recorder, fixture.Select("SELECT k,j FROM "+leaves[0], []string{"k", "j"}, []string{"INT4", "INT4"}, [][]any{{value, int64(9)}}))
			pid := fixture.LockPID(t, p.Ctx, conn)
			tx, err := conn.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			ctx, cancel := context.WithTimeout(p.Ctx, 5*time.Second)
			defer cancel()
			type outcome struct {
				result sql.Result
				err    error
			}
			done := make(chan outcome, 1)
			joined := false
			defer func() {
				cancel()
				reader.Rollback()
				if !joined {
					select {
					case <-done:
					case <-time.After(5 * time.Second):
						t.Error("leaf ATTACH worker did not join")
					}
				}
			}()
			go func() { r, e := tx.ExecContext(ctx, q); done <- outcome{r, e} }()
			blocked := !c.defaultBranch || c.wantScan
			if blocked {
				// Observe the real blocker identity on the retained reader.
				var holder int
				if err := reader.QueryRowContext(ctx, "SELECT pg_backend_pid()").Scan(&holder); err != nil {
					t.Fatal(err)
				}
				fixture.LockWaitBehind(t, ctx, pid, holder, fixture.LockOID(t, ctx, leaves[0]), "AccessExclusiveLock")
				var mode string
				var granted bool
				if err := fixture.Admin.QueryRowContext(ctx, "SELECT mode,granted FROM pg_locks WHERE pid=$1 AND relation=$2::regclass AND locktype='relation' AND NOT granted", pid, leaves[0]).Scan(&mode, &granted); err != nil {
					t.Fatal(err)
				}
				if mode != "AccessExclusiveLock" || granted {
					t.Errorf("descendant wait got %s granted=%t", mode, granted)
				}
				if err := reader.Rollback(); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case got := <-done:
				joined = true
				if got.err != nil {
					t.Fatal(got.err)
				}
				if got.result == nil {
					t.Fatal("successful ATTACH lost result")
				}
				count, err := got.result.RowsAffected()
				if err != nil || count != 0 {
					t.Fatalf("ATTACH affected %d: %v", count, err)
				}
			case <-ctx.Done():
				t.Fatalf("ATTACH did not complete with leaf reader retained=%t: %v", !blocked, ctx.Err())
			}
			// Completed proven DEFAULT attachment must not have demanded the
			// held descendant's AX. Candidate proof, in contrast, still does.
			var count int64
			if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT COUNT(*) FROM pg_locks WHERE pid=$1 AND relation=$2::regclass AND locktype='relation' AND mode='AccessExclusiveLock'", pid, leaves[0]).Scan(&count); err != nil {
				t.Fatal(err)
			}
			want := int64(0)
			if blocked {
				want = 1
			}
			if count != want {
				t.Errorf("retained descendant AX count %d want %d", count, want)
			}
			if err := reader.Rollback(); err != nil && !blocked {
				t.Fatal(err)
			}
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
