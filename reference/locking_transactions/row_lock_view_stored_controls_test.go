package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRowLockViewStoredStrengthAndControls(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			p := referenceViewFixture(t)
			for _, step := range []fixture.Step{fixture.Exec("CREATE TABLE u(id INT)", 0), fixture.Exec("INSERT INTO u VALUES(1)", 1), fixture.Exec("CREATE VIEW stored AS SELECT id,v FROM t FOR SHARE", 0)} {
				p.Step(t, step)
			}
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					a, b := fixture.LockConnections(t, p.Ctx, backend.DB)
					holder, err := a.BeginTx(p.Ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { holder.Rollback() })
					step := fixture.Select("SELECT stored.v FROM stored CROSS JOIN u WHERE stored.id=1 FOR NO KEY UPDATE", []string{"v"}, []string{"INT4"}, [][]any{{int64(10)}})
					step.Prepared = prepared
					fixture.RunStep(t, p.Ctx, holder, backend.Recorder, step)
					fixture.RunStep(t, p.Ctx, b, backend.Recorder, fixture.Select("SELECT v FROM t WHERE id=1 FOR KEY SHARE", []string{"v"}, []string{"INT4"}, [][]any{{int64(10)}}))
					if err := holder.Rollback(); err != nil {
						t.Fatal(err)
					}
					holder, err = a.BeginTx(p.Ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					step = fixture.Select("SELECT stored.v FROM stored CROSS JOIN u WHERE stored.id=1", []string{"v"}, []string{"INT4"}, [][]any{{int64(10)}})
					step.Prepared = true
					fixture.RunStep(t, p.Ctx, holder, backend.Recorder, step)
					fixture.RowAffected(t, p.Ctx, b, "DELETE FROM u", true, 1)
					if err := holder.Rollback(); err != nil {
						t.Fatal(err)
					}
					referenceViewStoredReplacement(t, p, backend, a, b, prepared)
				})
			}
		})
	}
}

func referenceViewStoredReplacement(t *testing.T, p *fixture.Pair, backend fixture.Backend, a, b *sql.Conn, prepared bool) {
	t.Helper()
	fixture.LockExec(t, p.Ctx, a, "DROP VIEW stored", 0)
	fixture.LockExec(t, p.Ctx, a, "CREATE VIEW stored AS SELECT id,v FROM t FOR UPDATE", 0)
	holder, err := a.BeginTx(p.Ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { holder.Rollback() })
	step := fixture.Select("SELECT v FROM stored WHERE id=1 FOR KEY SHARE", []string{"v"}, []string{"INT4"}, [][]any{{int64(10)}})
	step.Prepared = prepared
	fixture.RunStep(t, p.Ctx, holder, backend.Recorder, step)
	if !backend.PG {
		fixture.ViewNativeBlocked(t, p.Ctx, b, "UPDATE t SET v=99 WHERE id=1", prepared)
	} else {
		ap, bp := fixture.RowPIDs(t, p.Ctx, a, b)
		fixture.LockExec(t, p.Ctx, b, "SET lock_timeout='100ms'", 0)
		await := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
			return fixture.RowExecute(ctx, b, "UPDATE t SET v=99 WHERE id=1", prepared)
		})
		fixture.RowWait(t, p.Ctx, bp, ap)
		out := await()
		if out.Result != nil {
			t.Fatal("failed stored UPDATE returned result")
		}
		referenceRowTimeoutDiagnostic(t, out.Err, true, "55P03", "canceling statement due to lock timeout", "updating")
		fixture.LockExec(t, p.Ctx, b, "RESET lock_timeout", 0)
	}
	fixture.RowAffected(t, p.Ctx, b, "UPDATE t SET v=21 WHERE id=2", prepared, 1)
	if err := holder.Rollback(); err != nil {
		t.Fatal(err)
	}
	if !backend.PG {
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(dump.String(), "FOR UPDATE") {
			t.Fatal("lost stored clause", dump.String())
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
	}
	holder, err = a.BeginTx(p.Ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	step = fixture.Select("SELECT v FROM stored WHERE id=1", []string{"v"}, []string{"INT4"}, [][]any{{int64(10)}})
	step.Prepared = prepared
	fixture.RunStep(t, p.Ctx, holder, backend.Recorder, step)
	if !backend.PG {
		fixture.ViewNativeBlocked(t, p.Ctx, b, "DELETE FROM t WHERE id=1", prepared)
		if err := holder.Rollback(); err != nil {
			t.Fatal(err)
		}
	} else {
		ap, bp := fixture.RowPIDs(t, p.Ctx, a, b)
		await := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
			return fixture.RowExecute(ctx, b, "DELETE FROM t WHERE id=1", prepared)
		})
		fixture.RowWait(t, p.Ctx, bp, ap)
		if err := holder.Rollback(); err != nil {
			t.Fatal(err)
		}
		out := await()
		if out.Err != nil {
			t.Fatal(out.Err)
		}
		fixture.LockAffected(t, out.Result, 1)
	}
}
