package reference_test

import (
	"context"
	"testing"
)

func TestReferenceMergeInsertContention(t *testing.T) {
	for _, table := range []string{"t", "s"} {
		for _, prepared := range []bool{false, true} {
			t.Run(table+map[bool]string{false: "/prepare", true: "/held_execute"}[prepared], func(t *testing.T) {
				p := newReferenceScenario(t)
				for _, b := range p.backends() {
					t.Run(b.name, func(t *testing.T) {
						run := mergeInsertRun(t, p, b, false)
						run(referenceExec("CREATE TABLE t(id INT NOT NULL CONSTRAINT t_pkey UNIQUE)", 0))
						run(referenceExec("CREATE TABLE s(id INT)", 0))
						run(referenceExec("INSERT INTO s VALUES(1)", 1))
						q := "MERGE INTO t USING s ON t.id=s.id WHEN NOT MATCHED THEN INSERT VALUES(s.id)"
						c, err := b.db.Conn(p.ctx)
						if err != nil {
							t.Fatal(err)
						}
						defer c.Close()
						insertSelectRun(t, p.ctx, c, b.recorder, referenceExec("SET lock_timeout='500ms'", 0))
						var call func(context.Context) relationOutcome
						if prepared {
							stmt, err := c.PrepareContext(p.ctx, q)
							if err != nil {
								t.Fatal(err)
							}
							defer stmt.Close()
							call = func(ctx context.Context) relationOutcome {
								r, e := stmt.ExecContext(ctx)
								return relationOutcome{result: r, err: e}
							}
						} else {
							call = func(ctx context.Context) relationOutcome {
								s, e := c.PrepareContext(ctx, q)
								return relationOutcome{stmt: s, err: e}
							}
						}
						holder, err := b.db.BeginTx(p.ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						defer holder.Rollback()
						insertSelectRun(t, p.ctx, holder, b.recorder, referenceExec("LOCK TABLE "+table+" IN ACCESS EXCLUSIVE MODE", 0))
						var pid int
						if b.pg {
							pid = referenceLockPID(t, p.ctx, c)
						}
						join := referenceLockOperation(t, p.ctx, call)
						if b.pg {
							referenceLockWait(t, p.ctx, pid)
						}
						o := join()
						if o.result != nil || o.stmt != nil {
							if o.stmt != nil {
								o.stmt.Close()
							}
							t.Fatal("lock failure exposed handle/result", o)
						}
						d := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "55P03", Message: "canceling statement due to lock timeout"}
						if !prepared {
							d.Position = map[string]string{"t": "12", "s": "20"}[table]
						}
						referenceAssertDiagnostic(t, o.err, &d)
						if err := holder.Rollback(); err != nil {
							t.Fatal(err)
						}
						run(referenceSelect("SELECT * FROM t", []string{"id"}, []string{"INT4"}, [][]any{}))
						run(referencePrepared(referenceExec(q, 1)))
						run(referenceSelect("SELECT * FROM t", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
					})
				}
			})
		}
	}
}
