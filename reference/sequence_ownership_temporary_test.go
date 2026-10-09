package reference_test

import (
	"context"
	"testing"
)

func TestReferenceSequenceOwnershipTemporaryForms(t *testing.T) {
	for _, rollback := range []bool{false, true} {
		t.Run(map[bool]string{false: "Rename", true: "RollbackAndRename"}[rollback], func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, backend := range p.backends() {
				t.Run(backend.name, func(t *testing.T) {
					a, b := referenceLockConnections(t, p.ctx, backend.db)
					sequenceProbeExec(t, p.ctx, a, "CREATE TABLE t(a INT,b INT,c INT)")
					sequenceProbeExec(t, p.ctx, a, "CREATE SEQUENCE owned OWNED BY t.c")
					sequenceProbeExec(t, p.ctx, a, "ALTER TABLE t DROP COLUMN b")
					sequenceProbeExec(t, p.ctx, a, "ALTER TABLE t DROP COLUMN c RESTRICT")
					sequenceProbeError(t, p.ctx, a, "SELECT nextval('owned')", nil)
					want := controlsReferenceDiagnostic("42P01", `relation "owned" does not exist`)
					want.Position = "16"
					var value int64
					err := a.QueryRowContext(context.WithValue(p.ctx, referenceRecorderKey{}, backend.recorder), "SELECT nextval('owned')").Scan(&value)
					referenceAssertDiagnostic(t, err, &want)
					sequenceProbeExec(t, p.ctx, a, "CREATE UNLOGGED SEQUENCE s START 40")
					sequenceProbeExec(t, p.ctx, a, "ALTER SEQUENCE s SET LOGGED")
					sequenceProbeExec(t, p.ctx, a, "CREATE TEMPORARY SEQUENCE s START 7")
					sequenceProbeValue(t, p.ctx, a, "SELECT nextval('s')", 7)
					sequenceProbeValue(t, p.ctx, b, "SELECT nextval('s')", 40)
					sequenceProbeValue(t, p.ctx, a, "SELECT nextval('pg_temp.s')", 8)
					renameNext := int64(9)
					if rollback {
						sequenceProbeExec(t, p.ctx, a, "BEGIN")
						sequenceProbeExec(t, p.ctx, a, "DROP SEQUENCE pg_temp.s")
						sequenceProbeExec(t, p.ctx, a, "ROLLBACK")
						sequenceProbeValue(t, p.ctx, a, "SELECT nextval('s')", 9)
						renameNext = 10
					}
					sequenceProbeExec(t, p.ctx, a, "ALTER SEQUENCE pg_temp.s RENAME TO local_s")
					sequenceProbeValue(t, p.ctx, a, "SELECT nextval('local_s')", renameNext)
					sequenceProbeExec(t, p.ctx, a, "CREATE SCHEMA moved")
					defer sequenceProbeExec(t, p.ctx, a, "DROP SCHEMA moved CASCADE")
					sequenceProbeExec(t, p.ctx, a, "CREATE SEQUENCE detached OWNED BY t.a")
					sequenceProbeError(t, p.ctx, a, "ALTER SEQUENCE detached SET SCHEMA moved", nil)
					sequenceProbeExec(t, p.ctx, a, "ALTER SEQUENCE detached OWNED BY NONE")
					sequenceProbeExec(t, p.ctx, a, "ALTER SEQUENCE detached SET SCHEMA moved")
					sequenceProbeExec(t, p.ctx, a, "ALTER SEQUENCE moved.detached RENAME TO renamed")
					sequenceProbeExec(t, p.ctx, a, "DROP SEQUENCE moved.renamed,public.s RESTRICT")
					sequenceProbeError(t, p.ctx, a, "DROP SEQUENCE t", nil)
				})
			}
		})
	}
}
