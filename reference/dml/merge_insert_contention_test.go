package reference_test

import (
	"context"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceMergeInsertContention(t *testing.T) {
	for _, table := range []string{"t", "s"} {
		for _, prepared := range []bool{false, true} {
			t.Run(table+map[bool]string{false: "/prepare", true: "/held_execute"}[prepared], func(t *testing.T) {
				p := fixture.NewScenario(t)
				for _, b := range p.Backends() {
					t.Run(b.Name, func(t *testing.T) {
						run := fixture.MergeInsertRun(t, p, b, false)
						run(fixture.Exec("CREATE TABLE t(id INT NOT NULL CONSTRAINT t_pkey UNIQUE)", 0))
						run(fixture.Exec("CREATE TABLE s(id INT)", 0))
						run(fixture.Exec("INSERT INTO s VALUES(1)", 1))
						q := "MERGE INTO t USING s ON t.id=s.id WHEN NOT MATCHED THEN INSERT VALUES(s.id)"
						c, err := b.DB.Conn(p.Ctx)
						if err != nil {
							t.Fatal(err)
						}
						defer c.Close()
						fixture.InsertSelectRun(t, p.Ctx, c, b.Recorder, fixture.Exec("SET lock_timeout='500ms'", 0))
						var call func(context.Context) fixture.RelationOutcome
						if prepared {
							stmt, err := c.PrepareContext(p.Ctx, q)
							if err != nil {
								t.Fatal(err)
							}
							defer stmt.Close()
							call = func(ctx context.Context) fixture.RelationOutcome {
								r, e := stmt.ExecContext(ctx)
								return fixture.RelationOutcome{Result: r, Err: e}
							}
						} else {
							call = func(ctx context.Context) fixture.RelationOutcome {
								s, e := c.PrepareContext(ctx, q)
								return fixture.RelationOutcome{Stmt: s, Err: e}
							}
						}
						holder, err := b.DB.BeginTx(p.Ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						defer holder.Rollback()
						fixture.InsertSelectRun(t, p.Ctx, holder, b.Recorder, fixture.Exec("LOCK TABLE "+table+" IN ACCESS EXCLUSIVE MODE", 0))
						var pid int
						if b.PG {
							pid = fixture.LockPID(t, p.Ctx, c)
						}
						join := fixture.LockOperation(t, p.Ctx, call)
						if b.PG {
							fixture.LockWait(t, p.Ctx, pid)
						}
						o := join()
						if o.Result != nil || o.Stmt != nil {
							if o.Stmt != nil {
								o.Stmt.Close()
							}
							t.Fatal("lock failure exposed handle/result", o)
						}
						d := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "55P03", Message: "canceling statement due to lock timeout"}
						if !prepared {
							d.Position = map[string]string{"t": "12", "s": "20"}[table]
						}
						fixture.AssertDiagnostic(t, o.Err, &d)
						if err := holder.Rollback(); err != nil {
							t.Fatal(err)
						}
						run(fixture.Select("SELECT * FROM t", []string{"id"}, []string{"INT4"}, [][]any{}))
						run(fixture.Prepared(fixture.Exec(q, 1)))
						run(fixture.Select("SELECT * FROM t", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
					})
				}
			})
		}
	}
}
