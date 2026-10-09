//go:build reference

package reference_test

import "testing"

func TestReferenceBtreeIndexOrdinaryShareBuild(t *testing.T) {
	for _, commit := range []bool{false, true} {
		t.Run(map[bool]string{false: "rollback", true: "commit"}[commit], func(t *testing.T) {
			p := newReferencePair(t)
			a, b := referenceLockConnections(t, p.ctx, p.postgres)
			referenceLockExec(t, p.ctx, a, "CREATE TABLE t(n INT)", 0)
			referenceLockExec(t, p.ctx, a, "INSERT INTO t VALUES(1)", 1)
			tx, err := a.BeginTx(p.ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			referenceLockExec(t, p.ctx, tx, "INSERT INTO t VALUES(2)", 1)
			var pid int
			if err := b.QueryRowContext(p.ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
				t.Fatal(err)
			}
			stmt, err := b.PrepareContext(p.ctx, "CREATE INDEX idx ON t(n)")
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			done := make(chan relationOutcome, 1)
			go func() { result, err := stmt.ExecContext(p.ctx); done <- relationOutcome{result: result, err: err} }()
			schemaShadowWait(t, p.ctx, pid, "t")
			var mode string
			if err := referenceAdmin.QueryRowContext(p.ctx, "SELECT mode FROM pg_locks WHERE pid=$1 AND relation='t'::regclass AND NOT granted", pid).Scan(&mode); err != nil || mode != "ShareLock" {
				t.Fatal(mode, err)
			}
			if commit {
				err = tx.Commit()
			} else {
				err = tx.Rollback()
			}
			if err != nil {
				t.Fatal(err)
			}
			out := referenceLockResult(t, done)
			if out.err != nil {
				t.Fatal(out.err)
			}
			referenceLockAffected(t, out.result, 0)
			want := [][]any{{int64(1)}}
			if commit {
				want = append(want, []any{int64(2)})
			}
			referenceRunStep(t, p.ctx, b, p.postgresRecord, referenceSelect("SELECT n FROM t ORDER BY n", []string{"n"}, []string{"INT4"}, want))
		})
	}
}
