package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

const xactUserSelect = "SELECT * FROM pg_catalog.pg_stat_xact_user_tables WHERE relname=$1"

func xactUserGet(t *testing.T, p *fixture.Pair, c *sql.Conn, name string) xactTableRow {
	t.Helper()
	var r xactTableRow
	if err := c.QueryRowContext(p.Ctx, xactUserSelect, name).Scan(r.destinations()...); err != nil {
		t.Fatal(name, err)
	}
	return r
}

// Remove only owned fixture tables, including a failed in-transaction namespace move.
// Register before creation so cleanup also covers an interrupted setup. Reporter
// cleanup registered later runs first, settling its owner before DROP.
func xactUserCreate(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, name string) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
		defer cancel()
		for _, schema := range []string{"information_schema", "public"} {
			if _, err := b.DB.ExecContext(ctx, "DROP TABLE IF EXISTS "+schema+"."+name+" CASCADE"); err != nil {
				t.Error("owned USER fixture cleanup", err)
			}
		}
	})
	tableStatCreate(t, p, b, c, "public."+name)
}

func xactUserSeed(t *testing.T, p *fixture.Pair, b fixture.Backend, name string) {
	t.Helper()
	tableStatSeed(t, p, b, name)
}

func xactUserCumulative(t *testing.T, p *fixture.Pair, c *sql.Conn, name string) tableStatRow {
	t.Helper()
	var r tableStatRow
	if err := c.QueryRowContext(p.Ctx, "SELECT * FROM pg_catalog.pg_stat_user_tables WHERE relname=$1", name).Scan(r.destinations()...); err != nil {
		t.Fatal(err)
	}
	return r
}

// Completed manual maintenance may update its own count/time and tuple
// estimates. It must not invent access, DML, or automatic-command history.
func xactUserMaintenanceUnrelated(t *testing.T, before, after tableStatRow) {
	t.Helper()
	after.Live, after.Dead, after.Mods, after.Since = before.Live, before.Dead, before.Mods, before.Since
	after.Vacuum, after.Analyze = before.Vacuum, before.Analyze
	after.LastVacuum, after.LastAnalyze = before.LastVacuum, before.LastAnalyze
	after.VacuumTime, after.AnalyzeTime = before.VacuumTime, before.AnalyzeTime
	if !after.equal(before) {
		t.Fatal("maintenance changed unrelated access/DML/automatic fields", after, before)
	}
}
