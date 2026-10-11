package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSequencePhysicalLifetime(t *testing.T) {
	p := fixture.NewScenario(t)
	ctx := p.Ctx
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			a, b := fixture.LockConnections(t, ctx, backend.DB)
			fixture.SequenceProbeExec(t, ctx, a, "CREATE SCHEMA seq_schema")
			fixture.SequenceProbeExec(t, ctx, a, "CREATE SEQUENCE seq_schema.s")
			_, err := a.ExecContext(ctx, "DROP SCHEMA seq_schema RESTRICT")
			if err == nil {
				t.Fatal("schema restrict ignored sequence")
			}
			fixture.SequenceProbeExec(t, ctx, a, "DROP SCHEMA seq_schema CASCADE")
			fixture.SequenceProbeExec(t, ctx, a, "BEGIN")
			fixture.SequenceProbeExec(t, ctx, a, "CREATE TEMP SEQUENCE rolled_temp")
			fixture.SequenceProbeExec(t, ctx, a, "ROLLBACK")
			_, err = a.ExecContext(ctx, "SELECT nextval('pg_temp.rolled_temp')")
			if err == nil {
				t.Fatal("temporary creation survived rollback")
			}
			fixture.SequenceProbeExec(t, ctx, a, "CREATE SEQUENCE shadow START 40")
			fixture.SequenceProbeExec(t, ctx, a, "CREATE TEMP SEQUENCE shadow START 7 CACHE 3")
			fixture.SequenceProbeValue(t, ctx, a, "SELECT nextval('shadow')", 7)
			fixture.SequenceProbeValue(t, ctx, b, "SELECT nextval('shadow')", 40)
			fixture.SequenceProbeExec(t, ctx, a, "BEGIN")
			fixture.SequenceProbeExec(t, ctx, a, "DROP SEQUENCE pg_temp.shadow")
			fixture.SequenceProbeExec(t, ctx, a, "ROLLBACK")
			fixture.SequenceProbeValue(t, ctx, a, "SELECT nextval('shadow')", 8)
			if err = a.Close(); err != nil {
				t.Fatal(err)
			}
			backend.DB.SetMaxIdleConns(0)
			fresh := fixture.SequenceCoreConn(t, ctx, backend.DB)
			fixture.SequenceProbeValue(t, ctx, fresh, "SELECT nextval('shadow')", 41)
			var n int64
			err = fresh.QueryRowContext(ctx, "SELECT currval('shadow')").Scan(&n)
			if err != nil || n != 41 {
				t.Fatal(n, err)
			}
			fixture.SequenceProbeExec(t, ctx, b, "CREATE SEQUENCE cached_exit CACHE 10")
			fixture.SequenceProbeValue(t, ctx, b, "SELECT nextval('cached_exit')", 1)
			b.Close()
			fixture.SequenceProbeValue(t, ctx, fresh, "SELECT nextval('cached_exit')", 11)
		})
	}
}
