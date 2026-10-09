package reference_test

import (
	"testing"
)

func TestReferenceSequenceOptionsOwnershipAtomicity(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			a := sequenceCoreConn(t, p.ctx, backend.db)
			for _, q := range []string{"CREATE SEQUENCE small AS SMALLINT MAXVALUE 32767", "ALTER SEQUENCE small AS INTEGER", "CREATE SEQUENCE explicit AS SMALLINT MAXVALUE 10", "ALTER SEQUENCE explicit AS BIGINT", "CREATE SEQUENCE extreme MINVALUE -9223372036854775808 MAXVALUE 9223372036854775807 START -9223372036854775808 INCREMENT 9223372036854775807", "CREATE SEQUENCE down INCREMENT -2 MINVALUE -5 MAXVALUE -1 START -1 CYCLE"} {
				sequenceProbeExec(t, p.ctx, a, q)
			}
			sequenceProbeValue(t, p.ctx, a, "SELECT setval('small',2147483647,false)", 2147483647)
			sequenceProbeValue(t, p.ctx, a, "SELECT nextval('small')", 2147483647)
			sequenceProbeValue(t, p.ctx, a, "SELECT setval('explicit',10,false)", 10)
			sequenceProbeValue(t, p.ctx, a, "SELECT nextval('explicit')", 10)
			bound := controlsReferenceDiagnostic("2200H", `nextval: reached maximum value of sequence "explicit" (10)`)
			sequenceProbeError(t, p.ctx, a, "SELECT nextval('explicit')", &bound)
			for _, v := range []int64{-9223372036854775808, -1, 9223372036854775806} {
				sequenceProbeValue(t, p.ctx, a, "SELECT nextval('extreme')", v)
			}
			for _, v := range []int64{-1, -3, -5, -1} {
				sequenceProbeValue(t, p.ctx, a, "SELECT nextval('down')", v)
			}
			sequenceProbeExec(t, p.ctx, a, "CREATE TABLE target(v INT)")
			sequenceProbeExec(t, p.ctx, a, "CREATE SEQUENCE detached OWNED BY target.v")
			sequenceProbeExec(t, p.ctx, a, "ALTER SEQUENCE detached OWNED BY NONE")
			sequenceProbeExec(t, p.ctx, a, "DROP TABLE target RESTRICT")
			sequenceProbeValue(t, p.ctx, a, "SELECT nextval('detached')", 1)
			sequenceProbeExec(t, p.ctx, a, "CREATE SEQUENCE grouped")
			missing := controlsReferenceDiagnostic("42P01", `sequence "missing" does not exist`)
			sequenceProbeError(t, p.ctx, a, "DROP SEQUENCE grouped,missing", &missing)
			sequenceProbeValue(t, p.ctx, a, "SELECT nextval('grouped')", 1)
			sequenceProbeExec(t, p.ctx, a, "CREATE TABLE direct(v INT)")
			sequenceProbeExec(t, p.ctx, a, "CREATE SEQUENCE owned OWNED BY direct.v")
			sequenceProbeExec(t, p.ctx, a, "DROP SEQUENCE owned")
			result, err := a.ExecContext(p.ctx, "INSERT INTO direct VALUES(1)")
			if err != nil {
				t.Fatal(err)
			}
			if n, err := result.RowsAffected(); err != nil || n != 1 {
				t.Fatal(n, err)
			}
		})
	}
}
