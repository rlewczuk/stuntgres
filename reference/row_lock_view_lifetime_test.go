package reference_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"stuntgres"
)

func TestReferenceRowLockViewLifetime(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			p := referenceViewFixture(t)
			for _, backend := range p.backends() {
				t.Run(backend.name, func(t *testing.T) {
					a, b := referenceLockConnections(t, p.ctx, backend.db)
					holder, err := a.BeginTx(p.ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { holder.Rollback() })
					step := referenceSelect("SELECT val FROM w WHERE id=1 FOR UPDATE", []string{"val"}, []string{"INT4"}, [][]any{{int64(11)}})
					step.prepared = prepared
					referenceRunStep(t, p.ctx, holder, backend.recorder, step)
					step = referenceSelect("SELECT val FROM w WHERE id=1", []string{"val"}, []string{"INT4"}, [][]any{{int64(11)}})
					step.prepared = prepared
					referenceRunStep(t, p.ctx, b, backend.recorder, step)
					if backend.pg {
						ap, bp := referenceRowPIDs(t, p.ctx, a, b)
						done := referenceRowAsync(t, p.ctx, b, "UPDATE t SET v=20 WHERE id=1", prepared, false)
						referenceRowWait(t, p.ctx, bp, ap)
						if err := holder.Commit(); err != nil {
							t.Fatal(err)
						}
						out := referenceLockResult(t, done)
						if out.err != nil {
							t.Fatal(out.err)
						}
						referenceLockAffected(t, out.result, 1)
					} else {
						referenceViewNativeBlocked(t, p.ctx, b, "UPDATE t SET v=20 WHERE id=1", prepared)
						if err := holder.Commit(); err != nil {
							t.Fatal(err)
						}
						referenceRowAffected(t, p.ctx, b, "UPDATE t SET v=20 WHERE id=1", prepared, 1)
					}
					step = referenceSelect("SELECT val FROM w WHERE id=1 FOR SHARE", []string{"val"}, []string{"INT4"}, [][]any{{int64(21)}})
					step.prepared = prepared
					referenceRunStep(t, p.ctx, b, backend.recorder, step)
					referenceRowAffected(t, p.ctx, a, "UPDATE t SET v=10 WHERE id=1", prepared, 1)
					referenceRowAffected(t, p.ctx, a, "CREATE TABLE target(v INT)", false, 0)
					failure := referenceImplicitError("INSERT INTO target SELECT 1/(val-11) FROM w FOR UPDATE", referenceDiagnostic{Code: "22012", Message: "division by zero"})
					failure.prepared, failure.errorStage = prepared, "execute"
					referenceRunStep(t, p.ctx, a, backend.recorder, failure)
					referenceRunStep(t, p.ctx, b, backend.recorder, referenceSelect("SELECT v FROM target", []string{"v"}, []string{"INT4"}, nil))
					referenceRowAffected(t, p.ctx, b, "UPDATE t SET v=v WHERE id=1", prepared, 1)
					if !backend.pg {
						referenceViewNativeLifetime(t, p, prepared)
					}
				})
			}
		})
	}
}

func referenceViewNativeLifetime(t *testing.T, p *referencePair, prepared bool) {
	t.Helper()
	a, b := referenceLockConnections(t, p.ctx, p.native)
	retain := func(q string) relationOutcome {
		out := referenceRowQuery(p.ctx, a, q, prepared)
		out.rawBackend = referenceRawNative
		if out.err != nil {
			t.Fatal(out.err)
		}
		t.Cleanup(func() {
			if out.rows != nil {
				out.rows.Close()
			}
			if out.stmt != nil {
				out.stmt.Close()
			}
		})
		return out
	}
	out := retain("SELECT val FROM w ORDER BY val FOR UPDATE")
	referenceRowAffected(t, p.ctx, b, "UPDATE t SET v=20 WHERE id=1", prepared, 1)
	referenceViewValues(t, out, "val", "INT4", 11, 21)
	referenceRowAffected(t, p.ctx, a, "UPDATE t SET v=10 WHERE id=1", false, 1)
	failure := referenceRowExecute(p.ctx, a, "INSERT INTO target SELECT 1/(val-11) FROM w FOR UPDATE", prepared)
	if failure.result != nil {
		t.Fatal("failed native INSERT returned result")
	}
	referenceWaitsDiagnostic(t, failure.err, "22012", "division by zero")
	empty := referenceRowQuery(p.ctx, b, "SELECT v FROM target", false)
	empty.rawBackend = referenceRawNative
	referenceViewValues(t, empty, "v", "INT4")
	referenceRowAffected(t, p.ctx, b, "UPDATE t SET v=v WHERE id=1", false, 1)
	holder, err := a.BeginTx(p.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { holder.Rollback() })
	marked := referenceRowQuery(p.ctx, holder, "SELECT val FROM w WHERE id=1 FOR UPDATE", prepared)
	marked.rawBackend = referenceRawNative
	referenceViewValues(t, marked, "val", "INT4", 11)
	bounded, cancel := context.WithTimeout(p.ctx, 30*time.Millisecond)
	blocked := referenceRowQuery(bounded, b, "SELECT val FROM w WHERE id=1 FOR SHARE", prepared)
	cancel()
	if blocked.rows != nil {
		blocked.rows.Close()
		t.Fatal("canceled native view cursor")
	}
	if blocked.stmt != nil {
		if err := blocked.stmt.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if !errors.Is(blocked.err, context.DeadlineExceeded) {
		t.Fatal(blocked.err)
	}
	var state interface{ SQLState() string }
	if errors.As(blocked.err, &state) {
		t.Fatal("native caller deadline fabricated SQLSTATE", blocked.err)
	}
	if err := holder.Rollback(); err != nil {
		t.Fatal(err)
	}
	out = retain("SELECT val FROM w ORDER BY val FOR KEY SHARE")
	if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader("CREATE TABLE t(id INT,v INT);INSERT INTO t VALUES(1,99);CREATE VIEW w AS SELECT id,v+1 AS val FROM t")); err != nil {
		t.Fatal(err)
	}
	referenceViewValues(t, out, "val", "INT4", 11, 21)
	fresh := referenceRowQuery(p.ctx, a, "SELECT val FROM w FOR UPDATE", prepared)
	fresh.rawBackend = referenceRawNative
	referenceViewValues(t, fresh, "val", "INT4", 100)
}
