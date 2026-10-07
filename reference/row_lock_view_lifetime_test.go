//go:build reference

package reference_test

import "testing"

func TestReferenceRowLockViewLifetime(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			p := referenceViewFixture(t)
			a, b := referenceLockConnections(t, p.ctx, p.postgres)
			ap, bp := referenceRowPIDs(t, p.ctx, a, b)
			holder, err := a.BeginTx(p.ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer holder.Rollback()
			// The helper consumes/closes Rows and the prepared statement. The Tx still owns
			// the physical tuple grant; prove it through the actual PostgreSQL wait graph.
			referenceViewValues(t, referenceRowQuery(p.ctx, holder, "SELECT val FROM w WHERE id=1 FOR UPDATE", prepared), "val", "INT4", 11)
			referenceViewValues(t, referenceRowQuery(p.ctx, b, "SELECT val FROM w WHERE id=1", prepared), "val", "INT4", 11)
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
			referenceViewValues(t, referenceRowQuery(p.ctx, b, "SELECT val FROM w WHERE id=1 FOR SHARE", prepared), "val", "INT4", 21)
			referenceRowAffected(t, p.ctx, a, "UPDATE t SET v=10 WHERE id=1", prepared, 1)
			referenceRowAffected(t, p.ctx, a, "CREATE TABLE target(v INT)", false, 0)
			failure := referenceImplicitError("INSERT INTO target SELECT 1/(val-11) FROM w FOR UPDATE", referenceDiagnostic{Code: "22012", Message: "division by zero"})
			failure.prepared, failure.errorStage = prepared, "execute"
			referenceRunStep(t, p.ctx, a, p.postgresRecord, failure)
			referenceRunStep(t, p.ctx, b, p.postgresRecord, referenceSelect("SELECT v FROM target", []string{"v"}, []string{"INT4"}, nil))
			referenceRowAffected(t, p.ctx, b, "UPDATE t SET v=v WHERE id=1", prepared, 1)
		})
	}
}
