package reference_test

import (
	"context"
	"errors"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceRowLockViewLifetime(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			p := referenceViewFixture(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					a, b := fixture.LockConnections(t, p.Ctx, backend.DB)
					holder, err := a.BeginTx(p.Ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { holder.Rollback() })
					step := fixture.Select("SELECT val FROM w WHERE id=1 FOR UPDATE", []string{"val"}, []string{"INT4"}, [][]any{{int64(11)}})
					step.Prepared = prepared
					fixture.RunStep(t, p.Ctx, holder, backend.Recorder, step)
					step = fixture.Select("SELECT val FROM w WHERE id=1", []string{"val"}, []string{"INT4"}, [][]any{{int64(11)}})
					step.Prepared = prepared
					fixture.RunStep(t, p.Ctx, b, backend.Recorder, step)
					if backend.PG {
						ap, bp := fixture.RowPIDs(t, p.Ctx, a, b)
						done := fixture.RowAsync(t, p.Ctx, b, "UPDATE t SET v=20 WHERE id=1", prepared, false)
						fixture.RowWait(t, p.Ctx, bp, ap)
						if err := holder.Commit(); err != nil {
							t.Fatal(err)
						}
						out := fixture.LockResult(t, done)
						if out.Err != nil {
							t.Fatal(out.Err)
						}
						fixture.LockAffected(t, out.Result, 1)
					} else {
						fixture.ViewNativeBlocked(t, p.Ctx, b, "UPDATE t SET v=20 WHERE id=1", prepared)
						if err := holder.Commit(); err != nil {
							t.Fatal(err)
						}
						fixture.RowAffected(t, p.Ctx, b, "UPDATE t SET v=20 WHERE id=1", prepared, 1)
					}
					step = fixture.Select("SELECT val FROM w WHERE id=1 FOR SHARE", []string{"val"}, []string{"INT4"}, [][]any{{int64(21)}})
					step.Prepared = prepared
					fixture.RunStep(t, p.Ctx, b, backend.Recorder, step)
					fixture.RowAffected(t, p.Ctx, a, "UPDATE t SET v=10 WHERE id=1", prepared, 1)
					fixture.RowAffected(t, p.Ctx, a, "CREATE TABLE target(v INT)", false, 0)
					failure := fixture.ImplicitError("INSERT INTO target SELECT 1/(val-11) FROM w FOR UPDATE", fixture.Diagnostic{Code: "22012", Message: "division by zero"})
					failure.Prepared, failure.ErrorStage = prepared, "execute"
					fixture.RunStep(t, p.Ctx, a, backend.Recorder, failure)
					fixture.RunStep(t, p.Ctx, b, backend.Recorder, fixture.Select("SELECT v FROM target", []string{"v"}, []string{"INT4"}, nil))
					fixture.RowAffected(t, p.Ctx, b, "UPDATE t SET v=v WHERE id=1", prepared, 1)
					if !backend.PG {
						referenceViewNativeLifetime(t, p, prepared)
					}
				})
			}
		})
	}
}

func referenceViewNativeLifetime(t *testing.T, p *fixture.Pair, prepared bool) {
	t.Helper()
	a, b := fixture.LockConnections(t, p.Ctx, p.Native)
	retain := func(q string) fixture.RelationOutcome {
		out := fixture.RowQuery(p.Ctx, a, q, prepared)
		out.RawBackend = fixture.RawNative
		if out.Err != nil {
			t.Fatal(out.Err)
		}
		t.Cleanup(func() {
			if out.Rows != nil {
				out.Rows.Close()
			}
			if out.Stmt != nil {
				out.Stmt.Close()
			}
		})
		return out
	}
	out := retain("SELECT val FROM w ORDER BY val FOR UPDATE")
	fixture.RowAffected(t, p.Ctx, b, "UPDATE t SET v=20 WHERE id=1", prepared, 1)
	fixture.ViewValues(t, out, "val", "INT4", 11, 21)
	fixture.RowAffected(t, p.Ctx, a, "UPDATE t SET v=10 WHERE id=1", false, 1)
	failure := fixture.RowExecute(p.Ctx, a, "INSERT INTO target SELECT 1/(val-11) FROM w FOR UPDATE", prepared)
	if failure.Result != nil {
		t.Fatal("failed native INSERT returned result")
	}
	fixture.WaitsDiagnostic(t, failure.Err, "22012", "division by zero")
	empty := fixture.RowQuery(p.Ctx, b, "SELECT v FROM target", false)
	empty.RawBackend = fixture.RawNative
	fixture.ViewValues(t, empty, "v", "INT4")
	fixture.RowAffected(t, p.Ctx, b, "UPDATE t SET v=v WHERE id=1", false, 1)
	holder, err := a.BeginTx(p.Ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { holder.Rollback() })
	marked := fixture.RowQuery(p.Ctx, holder, "SELECT val FROM w WHERE id=1 FOR UPDATE", prepared)
	marked.RawBackend = fixture.RawNative
	fixture.ViewValues(t, marked, "val", "INT4", 11)
	bounded, cancel := context.WithTimeout(p.Ctx, 30*time.Millisecond)
	blocked := fixture.RowQuery(bounded, b, "SELECT val FROM w WHERE id=1 FOR SHARE", prepared)
	cancel()
	if blocked.Rows != nil {
		blocked.Rows.Close()
		t.Fatal("canceled native view cursor")
	}
	if blocked.Stmt != nil {
		if err := blocked.Stmt.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if !errors.Is(blocked.Err, context.DeadlineExceeded) {
		t.Fatal(blocked.Err)
	}
	var state interface{ SQLState() string }
	if errors.As(blocked.Err, &state) {
		t.Fatal("native caller deadline fabricated SQLSTATE", blocked.Err)
	}
	if err := holder.Rollback(); err != nil {
		t.Fatal(err)
	}
	out = retain("SELECT val FROM w ORDER BY val FOR KEY SHARE")
	if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE TABLE t(id INT,v INT);INSERT INTO t VALUES(1,99);CREATE VIEW w AS SELECT id,v+1 AS val FROM t")); err != nil {
		t.Fatal(err)
	}
	fixture.ViewValues(t, out, "val", "INT4", 11, 21)
	fresh := fixture.RowQuery(p.Ctx, a, "SELECT val FROM w FOR UPDATE", prepared)
	fresh.RawBackend = fixture.RawNative
	fixture.ViewValues(t, fresh, "val", "INT4", 100)
}
