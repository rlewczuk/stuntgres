package reference_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/reference/fixture"
)

func pgTSConfigMapHeld(t *testing.T, p *fixture.Pair, b fixture.Backend, q string, values []any, valid any, want [][]any) {
	t.Helper()
	conn, err := b.DB.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	stmt, err := conn.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}
	for i, value := range values {
		rows := [][]any{}
		if value == valid {
			rows = want
		}
		s := pgTSConfigMapStep(b, q, rows, value)
		fixture.PgDescriptionExecutorRows(t, p, b, held, s)
		// Endpoints and NULL/maximum boundaries also use fresh execution paths.
		if i == 0 || i == len(values)-1 || value == nil || value == int64(4294967295) || value == int64(-2147483648) || value == int64(2147483647) {
			pgTSConfigBoth(t, p, b, conn, s)
		}
	}
}

func TestReferencePGTSConfigMapPrepared(t *testing.T) {
	pgTSConfigScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		pgTSConfigMapHeld(t, p, b, pgTSConfigMapProbe, []any{int64(3748), int64(0), int64(4294967295), nil, int64(3748)}, int64(3748), pgTSConfigMapStock())
		pgTSConfigMapHeld(t, p, b, "SELECT * FROM pg_catalog.pg_ts_config_map WHERE mapcfg=3748 AND mapdict=$1 ORDER BY maptokentype,mapseqno", []any{int64(3765), int64(0), int64(4294967295), nil, int64(3765)}, int64(3765), pgTSConfigMapStock())
		pgTSConfigMapHeld(t, p, b, "SELECT * FROM pg_catalog.pg_ts_config_map WHERE mapcfg=3748 AND maptokentype=$1 ORDER BY mapseqno", []any{int64(1), int64(0), int64(-2147483648), int64(2147483647), nil, int64(12), int64(1)}, int64(1), [][]any{{int64(3748), int64(1), int64(1), int64(3765)}})
		pgTSConfigMapHeld(t, p, b, "SELECT * FROM pg_catalog.pg_ts_config_map WHERE mapcfg=3748 AND mapseqno=$1 ORDER BY maptokentype", []any{int64(1), int64(0), int64(-2147483648), int64(2147483647), nil, int64(1)}, int64(1), pgTSConfigMapStock())
	})
}

func TestReferencePGTSConfigMapCancellation(t *testing.T) {
	pgTSConfigScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		if b.PG {
			return
		} // Ordinary native context errors are not server diagnostics.
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		stmt, err := conn.PrepareContext(p.Ctx, pgTSConfigMapProbe)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: pgTSConfigMapProbe}
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		rows, err := conn.QueryContext(ctx, pgTSConfigMapProbe, int64(3748))
		if rows != nil {
			rows.Close()
			t.Fatal("canceled query published Rows")
		}
		if !errors.Is(err, context.Canceled) {
			t.Fatal("query cancellation", err)
		}
		prepared, err := conn.PrepareContext(ctx, pgTSConfigMapProbe)
		if prepared != nil {
			prepared.Close()
			t.Fatal("canceled prepare published Stmt")
		}
		if !errors.Is(err, context.Canceled) {
			t.Fatal("prepare cancellation", err)
		}
		rows, err = held.QueryContext(ctx, pgTSConfigMapProbe, int64(3748))
		if rows != nil {
			rows.Close()
			t.Fatal("canceled held execution published Rows")
		}
		if !errors.Is(err, context.Canceled) {
			t.Fatal("held cancellation", err)
		}
		s := pgTSConfigMapStep(b, pgTSConfigMapProbe, pgTSConfigMapStock(), int64(3748))
		fixture.PgDescriptionExecutorRows(t, p, b, held, s)
		pgTSConfigBoth(t, p, b, conn, s)
	})
}
