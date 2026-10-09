package reference_test

import "testing"

func TestReferenceSequencePhysicalLifetime(t *testing.T) {
	p := newReferenceScenario(t)
	ctx := p.ctx
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			a, b := referenceLockConnections(t, ctx, backend.db)
			sequenceProbeExec(t, ctx, a, "CREATE SCHEMA seq_schema")
			sequenceProbeExec(t, ctx, a, "CREATE SEQUENCE seq_schema.s")
			_, err := a.ExecContext(ctx, "DROP SCHEMA seq_schema RESTRICT")
			if err == nil {
				t.Fatal("schema restrict ignored sequence")
			}
			sequenceProbeExec(t, ctx, a, "DROP SCHEMA seq_schema CASCADE")
			sequenceProbeExec(t, ctx, a, "BEGIN")
			sequenceProbeExec(t, ctx, a, "CREATE TEMP SEQUENCE rolled_temp")
			sequenceProbeExec(t, ctx, a, "ROLLBACK")
			_, err = a.ExecContext(ctx, "SELECT nextval('pg_temp.rolled_temp')")
			if err == nil {
				t.Fatal("temporary creation survived rollback")
			}
			sequenceProbeExec(t, ctx, a, "CREATE SEQUENCE shadow START 40")
			sequenceProbeExec(t, ctx, a, "CREATE TEMP SEQUENCE shadow START 7 CACHE 3")
			sequenceProbeValue(t, ctx, a, "SELECT nextval('shadow')", 7)
			sequenceProbeValue(t, ctx, b, "SELECT nextval('shadow')", 40)
			sequenceProbeExec(t, ctx, a, "BEGIN")
			sequenceProbeExec(t, ctx, a, "DROP SEQUENCE pg_temp.shadow")
			sequenceProbeExec(t, ctx, a, "ROLLBACK")
			sequenceProbeValue(t, ctx, a, "SELECT nextval('shadow')", 8)
			if err = a.Close(); err != nil {
				t.Fatal(err)
			}
			backend.db.SetMaxIdleConns(0)
			fresh := sequenceCoreConn(t, ctx, backend.db)
			sequenceProbeValue(t, ctx, fresh, "SELECT nextval('shadow')", 41)
			var n int64
			err = fresh.QueryRowContext(ctx, "SELECT currval('shadow')").Scan(&n)
			if err != nil || n != 41 {
				t.Fatal(n, err)
			}
			sequenceProbeExec(t, ctx, b, "CREATE SEQUENCE cached_exit CACHE 10")
			sequenceProbeValue(t, ctx, b, "SELECT nextval('cached_exit')", 1)
			b.Close()
			sequenceProbeValue(t, ctx, fresh, "SELECT nextval('cached_exit')", 11)
		})
	}
}
