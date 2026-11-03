package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

var pvcFunctions = []string{"pg_check_visible", "pg_check_frozen", "pg_truncate_visibility_map"}

// Oracle first, without filtering either backend: a missing native routine must
// not prevent any independently specified PostgreSQL scenario from running.
func pvcCase(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	fixture.SchemaReferenceCleanup(t, "pv_part")
	fixture.SchemaReferenceCleanup(t, "pv_moved")
	p := fixture.NewScenario(t)
	backends := p.Backends()
	for i := len(backends) - 1; i >= 0; i-- {
		b := backends[i]
		t.Run(b.Name, func(t *testing.T) {
			c := pvConn(t, p, b)
			defer c.Close()
			if b.PG {
				clean := func() {
					ctx, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
					defer cancel()
					for _, q := range []string{"ROLLBACK", "RESET SESSION AUTHORIZATION", "DROP EXTENSION IF EXISTS pg_visibility CASCADE", "DROP SCHEMA IF EXISTS pv_part CASCADE", "DROP SCHEMA IF EXISTS pv_moved CASCADE"} {
						if _, err := c.ExecContext(ctx, q); err != nil {
							t.Error(q, err)
						}
					}
				}
				clean()
				defer clean()
			}
			for _, q := range []string{"CREATE SCHEMA pv_part", "CREATE EXTENSION pg_visibility SCHEMA pv_part", "CREATE TABLE pv_part.heap(k integer)", "ALTER TABLE pv_part.heap SET(autovacuum_enabled=false)"} {
				pvRun(t, p, b, c, fixture.Exec(q, 0))
			}
			body(t, p, b, c)
		})
	}
}

func pvcChecks(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, target any, findings [][]any) {
	t.Helper()
	for _, fn := range pvcFunctions[:2] {
		q := "SELECT t_ctid FROM pv_part." + fn + "($1::regclass) ORDER BY t_ctid"
		pvBoth(t, p, b, c, pvMetadata(fixture.Select(q, []string{"t_ctid"}, []string{"TID"}, findings, target), b.PG))
	}
}

func pvcTruncate(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, target any) {
	t.Helper()
	var value any = ""
	if target == nil {
		value = nil
	}
	pvBoth(t, p, b, c, pvMetadata(fixture.Select("SELECT pv_part.pg_truncate_visibility_map($1::regclass) AS result", []string{"result"}, []string{"VOID"}, [][]any{{value}}, target), b.PG))
}

func pvcSuppressed(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	pvProbe(t, p, b, c, "pv_part.heap", 0, false, false, true)
	pvBoth(t, p, b, c, fixture.Select("SELECT * FROM pv_part.pg_visibility('pv_part.heap'::regclass) ORDER BY blkno", pvPageRowsNames, pvPageRowsTypes, [][]any{{int64(0), false, false, true}}))
	pvBoth(t, p, b, c, fixture.Select("SELECT * FROM pv_part.pg_visibility_map('pv_part.heap'::regclass) ORDER BY blkno", pvMapRowsNames, pvMapRowsTypes, [][]any{{int64(0), false, false}}))
	pvBoth(t, p, b, c, fixture.Select("SELECT * FROM pv_part.pg_visibility_map_summary('pv_part.heap'::regclass)", pvMapNames, pvSummaryTypes, [][]any{{int64(0), int64(0)}}))
	pvBoth(t, p, b, c, fixture.Select("SELECT k FROM pv_part.heap", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}}))
	pvcChecks(t, p, b, c, "pv_part.heap", nil)
}
