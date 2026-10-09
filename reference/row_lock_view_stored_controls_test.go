package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
)

func TestReferenceRowLockViewStoredStrengthAndControls(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			p := referenceViewFixture(t)
			for _, step := range []referenceStep{referenceExec("CREATE TABLE u(id INT)", 0), referenceExec("INSERT INTO u VALUES(1)", 1), referenceExec("CREATE VIEW stored AS SELECT id,v FROM t FOR SHARE", 0)} {
				p.step(t, step)
			}
			for _, backend := range p.backends() {
				t.Run(backend.name, func(t *testing.T) {
					a, b := referenceLockConnections(t, p.ctx, backend.db)
					holder, err := a.BeginTx(p.ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { holder.Rollback() })
					step := referenceSelect("SELECT stored.v FROM stored CROSS JOIN u WHERE stored.id=1 FOR NO KEY UPDATE", []string{"v"}, []string{"INT4"}, [][]any{{int64(10)}})
					step.prepared = prepared
					referenceRunStep(t, p.ctx, holder, backend.recorder, step)
					referenceRunStep(t, p.ctx, b, backend.recorder, referenceSelect("SELECT v FROM t WHERE id=1 FOR KEY SHARE", []string{"v"}, []string{"INT4"}, [][]any{{int64(10)}}))
					if err := holder.Rollback(); err != nil {
						t.Fatal(err)
					}
					holder, err = a.BeginTx(p.ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					step = referenceSelect("SELECT stored.v FROM stored CROSS JOIN u WHERE stored.id=1", []string{"v"}, []string{"INT4"}, [][]any{{int64(10)}})
					step.prepared = true
					referenceRunStep(t, p.ctx, holder, backend.recorder, step)
					referenceRowAffected(t, p.ctx, b, "DELETE FROM u", true, 1)
					if err := holder.Rollback(); err != nil {
						t.Fatal(err)
					}
					referenceViewStoredReplacement(t, p, backend, a, b, prepared)
				})
			}
		})
	}
}

func referenceViewStoredReplacement(t *testing.T, p *referencePair, backend referenceBackend, a, b *sql.Conn, prepared bool) {
	t.Helper()
	referenceLockExec(t, p.ctx, a, "DROP VIEW stored", 0)
	referenceLockExec(t, p.ctx, a, "CREATE VIEW stored AS SELECT id,v FROM t FOR UPDATE", 0)
	holder, err := a.BeginTx(p.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { holder.Rollback() })
	step := referenceSelect("SELECT v FROM stored WHERE id=1 FOR KEY SHARE", []string{"v"}, []string{"INT4"}, [][]any{{int64(10)}})
	step.prepared = prepared
	referenceRunStep(t, p.ctx, holder, backend.recorder, step)
	if !backend.pg {
		referenceViewNativeBlocked(t, p.ctx, b, "UPDATE t SET v=99 WHERE id=1", prepared)
	} else {
		ap, bp := referenceRowPIDs(t, p.ctx, a, b)
		referenceLockExec(t, p.ctx, b, "SET lock_timeout='100ms'", 0)
		await := referenceLockOperation(t, p.ctx, func(ctx context.Context) relationOutcome {
			return referenceRowExecute(ctx, b, "UPDATE t SET v=99 WHERE id=1", prepared)
		})
		referenceRowWait(t, p.ctx, bp, ap)
		out := await()
		if out.result != nil {
			t.Fatal("failed stored UPDATE returned result")
		}
		referenceRowTimeoutDiagnostic(t, out.err, true, "55P03", "canceling statement due to lock timeout", "updating")
		referenceLockExec(t, p.ctx, b, "RESET lock_timeout", 0)
	}
	referenceRowAffected(t, p.ctx, b, "UPDATE t SET v=21 WHERE id=2", prepared, 1)
	if err := holder.Rollback(); err != nil {
		t.Fatal(err)
	}
	if !backend.pg {
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(dump.String(), "FOR UPDATE") {
			t.Fatal("lost stored clause", dump.String())
		}
		if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
	}
	holder, err = a.BeginTx(p.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	step = referenceSelect("SELECT v FROM stored WHERE id=1", []string{"v"}, []string{"INT4"}, [][]any{{int64(10)}})
	step.prepared = prepared
	referenceRunStep(t, p.ctx, holder, backend.recorder, step)
	if !backend.pg {
		referenceViewNativeBlocked(t, p.ctx, b, "DELETE FROM t WHERE id=1", prepared)
		if err := holder.Rollback(); err != nil {
			t.Fatal(err)
		}
	} else {
		ap, bp := referenceRowPIDs(t, p.ctx, a, b)
		await := referenceLockOperation(t, p.ctx, func(ctx context.Context) relationOutcome {
			return referenceRowExecute(ctx, b, "DELETE FROM t WHERE id=1", prepared)
		})
		referenceRowWait(t, p.ctx, bp, ap)
		if err := holder.Rollback(); err != nil {
			t.Fatal(err)
		}
		out := await()
		if out.err != nil {
			t.Fatal(out.err)
		}
		referenceLockAffected(t, out.result, 1)
	}
}
