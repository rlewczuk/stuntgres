package reference_test

import (
	"context"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSequenceOwnershipTemporaryForms(t *testing.T) {
	for _, rollback := range []bool{false, true} {
		t.Run(map[bool]string{false: "Rename", true: "RollbackAndRename"}[rollback], func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					a, b := fixture.LockConnections(t, p.Ctx, backend.DB)
					fixture.SequenceProbeExec(t, p.Ctx, a, "CREATE TABLE t(a INT,b INT,c INT)")
					fixture.SequenceProbeExec(t, p.Ctx, a, "CREATE SEQUENCE owned OWNED BY t.c")
					fixture.SequenceProbeExec(t, p.Ctx, a, "ALTER TABLE t DROP COLUMN b")
					fixture.SequenceProbeExec(t, p.Ctx, a, "ALTER TABLE t DROP COLUMN c RESTRICT")
					fixture.SequenceProbeError(t, p.Ctx, a, "SELECT nextval('owned')", nil)
					want := fixture.ControlsReferenceDiagnostic("42P01", `relation "owned" does not exist`)
					want.Position = "16"
					var value int64
					err := a.QueryRowContext(context.WithValue(p.Ctx, fixture.RecorderKey{}, backend.Recorder), "SELECT nextval('owned')").Scan(&value)
					fixture.AssertDiagnostic(t, err, &want)
					fixture.SequenceProbeExec(t, p.Ctx, a, "CREATE UNLOGGED SEQUENCE s START 40")
					fixture.SequenceProbeExec(t, p.Ctx, a, "ALTER SEQUENCE s SET LOGGED")
					fixture.SequenceProbeExec(t, p.Ctx, a, "CREATE TEMPORARY SEQUENCE s START 7")
					fixture.SequenceProbeValue(t, p.Ctx, a, "SELECT nextval('s')", 7)
					fixture.SequenceProbeValue(t, p.Ctx, b, "SELECT nextval('s')", 40)
					fixture.SequenceProbeValue(t, p.Ctx, a, "SELECT nextval('pg_temp.s')", 8)
					renameNext := int64(9)
					if rollback {
						fixture.SequenceProbeExec(t, p.Ctx, a, "BEGIN")
						fixture.SequenceProbeExec(t, p.Ctx, a, "DROP SEQUENCE pg_temp.s")
						fixture.SequenceProbeExec(t, p.Ctx, a, "ROLLBACK")
						fixture.SequenceProbeValue(t, p.Ctx, a, "SELECT nextval('s')", 9)
						renameNext = 10
					}
					fixture.SequenceProbeExec(t, p.Ctx, a, "ALTER SEQUENCE pg_temp.s RENAME TO local_s")
					fixture.SequenceProbeValue(t, p.Ctx, a, "SELECT nextval('local_s')", renameNext)
					fixture.SequenceProbeExec(t, p.Ctx, a, "CREATE SCHEMA moved")
					defer fixture.SequenceProbeExec(t, p.Ctx, a, "DROP SCHEMA moved CASCADE")
					fixture.SequenceProbeExec(t, p.Ctx, a, "CREATE SEQUENCE detached OWNED BY t.a")
					fixture.SequenceProbeError(t, p.Ctx, a, "ALTER SEQUENCE detached SET SCHEMA moved", nil)
					fixture.SequenceProbeExec(t, p.Ctx, a, "ALTER SEQUENCE detached OWNED BY NONE")
					fixture.SequenceProbeExec(t, p.Ctx, a, "ALTER SEQUENCE detached SET SCHEMA moved")
					fixture.SequenceProbeExec(t, p.Ctx, a, "ALTER SEQUENCE moved.detached RENAME TO renamed")
					fixture.SequenceProbeExec(t, p.Ctx, a, "DROP SEQUENCE moved.renamed,public.s RESTRICT")
					fixture.SequenceProbeError(t, p.Ctx, a, "DROP SEQUENCE t", nil)
				})
			}
		})
	}
}
