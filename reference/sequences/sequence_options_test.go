package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSequenceOptionsOwnershipAtomicity(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			a := fixture.SequenceCoreConn(t, p.Ctx, backend.DB)
			for _, q := range []string{"CREATE SEQUENCE small AS SMALLINT MAXVALUE 32767", "ALTER SEQUENCE small AS INTEGER", "CREATE SEQUENCE explicit AS SMALLINT MAXVALUE 10", "ALTER SEQUENCE explicit AS BIGINT", "CREATE SEQUENCE extreme MINVALUE -9223372036854775808 MAXVALUE 9223372036854775807 START -9223372036854775808 INCREMENT 9223372036854775807", "CREATE SEQUENCE down INCREMENT -2 MINVALUE -5 MAXVALUE -1 START -1 CYCLE"} {
				fixture.SequenceProbeExec(t, p.Ctx, a, q)
			}
			fixture.SequenceProbeValue(t, p.Ctx, a, "SELECT setval('small',2147483647,false)", 2147483647)
			fixture.SequenceProbeValue(t, p.Ctx, a, "SELECT nextval('small')", 2147483647)
			fixture.SequenceProbeValue(t, p.Ctx, a, "SELECT setval('explicit',10,false)", 10)
			fixture.SequenceProbeValue(t, p.Ctx, a, "SELECT nextval('explicit')", 10)
			bound := fixture.ControlsReferenceDiagnostic("2200H", `nextval: reached maximum value of sequence "explicit" (10)`)
			fixture.SequenceProbeError(t, p.Ctx, a, "SELECT nextval('explicit')", &bound)
			for _, v := range []int64{-9223372036854775808, -1, 9223372036854775806} {
				fixture.SequenceProbeValue(t, p.Ctx, a, "SELECT nextval('extreme')", v)
			}
			for _, v := range []int64{-1, -3, -5, -1} {
				fixture.SequenceProbeValue(t, p.Ctx, a, "SELECT nextval('down')", v)
			}
			fixture.SequenceProbeExec(t, p.Ctx, a, "CREATE TABLE target(v INT)")
			fixture.SequenceProbeExec(t, p.Ctx, a, "CREATE SEQUENCE detached OWNED BY target.v")
			fixture.SequenceProbeExec(t, p.Ctx, a, "ALTER SEQUENCE detached OWNED BY NONE")
			fixture.SequenceProbeExec(t, p.Ctx, a, "DROP TABLE target RESTRICT")
			fixture.SequenceProbeValue(t, p.Ctx, a, "SELECT nextval('detached')", 1)
			fixture.SequenceProbeExec(t, p.Ctx, a, "CREATE SEQUENCE grouped")
			missing := fixture.ControlsReferenceDiagnostic("42P01", `sequence "missing" does not exist`)
			fixture.SequenceProbeError(t, p.Ctx, a, "DROP SEQUENCE grouped,missing", &missing)
			fixture.SequenceProbeValue(t, p.Ctx, a, "SELECT nextval('grouped')", 1)
			fixture.SequenceProbeExec(t, p.Ctx, a, "CREATE TABLE direct(v INT)")
			fixture.SequenceProbeExec(t, p.Ctx, a, "CREATE SEQUENCE owned OWNED BY direct.v")
			fixture.SequenceProbeExec(t, p.Ctx, a, "DROP SEQUENCE owned")
			result, err := a.ExecContext(p.Ctx, "INSERT INTO direct VALUES(1)")
			if err != nil {
				t.Fatal(err)
			}
			if n, err := result.RowsAffected(); err != nil || n != 1 {
				t.Fatal(n, err)
			}
		})
	}
}
