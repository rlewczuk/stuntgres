package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGStatXactUserTablesLifetime(t *testing.T) {
	fixture.StatisticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		xactUserCreate(t, p, b, c, "xut_lifetime")
		w, retire := fixture.StatisticsReportingConn(t, p, b)
		fixture.StatisticsExec(t, p, b, w, "BEGIN", 0)
		fixture.StatisticsExec(t, p, b, w, "INSERT INTO public.xut_lifetime VALUES(1,NULL),(2,'b')", 2)
		old := xactUserGet(t, p, w, "xut_lifetime")
		xactTableCounts(t, old, 2, 0, 0)
		stmt, err := w.PrepareContext(p.Ctx, "SELECT * FROM pg_catalog.pg_stat_xact_user_tables WHERE relid=$1")
		if err != nil {
			t.Fatal(err)
		}
		get := func(want bool) xactTableRow {
			t.Helper()
			var r xactTableRow
			err := stmt.QueryRowContext(p.Ctx, old.ID).Scan(r.destinations()...)
			if !want {
				if err != sql.ErrNoRows {
					t.Fatal("moved-out membership", r, err)
				}
				return r
			}
			if err != nil || r.ID != old.ID {
				t.Fatal("held relid membership", r, err)
			}
			return r
		}
		fixture.StatisticsExec(t, p, b, w, "ALTER TABLE public.xut_lifetime SET SCHEMA information_schema", 0)
		get(false)
		fixture.StatisticsExec(t, p, b, w, "ALTER TABLE information_schema.xut_lifetime SET SCHEMA public", 0)
		xactTableCounts(t, get(true), 2, 0, 0)
		fixture.StatisticsExec(t, p, b, w, "SAVEPOINT moved", 0)
		fixture.StatisticsExec(t, p, b, w, "ALTER TABLE public.xut_lifetime SET SCHEMA information_schema", 0)
		get(false)
		fixture.StatisticsExec(t, p, b, w, "ROLLBACK TO moved", 0)
		xactTableCounts(t, get(true), 2, 0, 0)
		fixture.StatisticsExec(t, p, b, w, "ALTER TABLE public.xut_lifetime RENAME TO xut_renamed", 0)
		if renamed := get(true); renamed.Name != "xut_renamed" || renamed.Schema != "public" {
			t.Fatal("current renamed row", renamed)
		}
		stmt.Close()
		xactTableEnd(t, p, b, w, "ROLLBACK")
		retire()
		if restored := xactUserGet(t, p, c, "xut_lifetime"); restored.ID != old.ID || restored.Name != "xut_lifetime" {
			t.Fatal("DDL rollback membership", restored, old)
		}
		holder := fixture.IdentityConnection(t, p.Ctx, b.DB)
		held, err := holder.QueryContext(p.Ctx, xactUserSelect, "xut_lifetime")
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		columns, err := held.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		fixture.StatisticsExec(t, p, b, c, "ALTER TABLE public.xut_lifetime RENAME TO xut_held_renamed", 0)
		if renamed := xactUserGet(t, p, c, "xut_held_renamed"); renamed.ID != old.ID || renamed.Name != "xut_held_renamed" {
			t.Fatal("current identity after committed rename", renamed, old)
		}
		fixture.StatisticsExec(t, p, b, c, "DROP TABLE public.xut_held_renamed", 0)
		if !held.Next() {
			t.Fatal("retained USER row absent", held.Err())
		}
		var retained xactTableRow
		if err := held.Scan(retained.destinations()...); err != nil {
			t.Fatal(err)
		}
		held.Close()
		xactTableMetadata(t, columns, b.PG)
		if retained.ID != old.ID || retained.Name != old.Name || retained.Schema != old.Schema {
			t.Fatal("retained identity mutated", retained, old)
		}
		var missing xactTableRow
		if err := c.QueryRowContext(p.Ctx, xactUserSelect, "xut_lifetime").Scan(missing.destinations()...); err != sql.ErrNoRows {
			t.Fatal("dropped USER member", err)
		}
		tableStatCreate(t, p, b, c, "public.xut_lifetime")
		fresh := xactUserGet(t, p, c, "xut_lifetime")
		xactTableCounts(t, fresh, 0, 0, 0)
		if fresh.ID == old.ID || fresh.Seq != 0 || fresh.Read != 0 {
			t.Fatal("new identity inherited pending work", fresh, old)
		}
		xactTableCounts(t, old, 2, 0, 0)
		if old.Name != "xut_lifetime" {
			t.Fatal("old name mutated", old)
		}
		if !b.PG {
			xactUserNativeControls(t, p, b, c)
		}
	})
}

func TestReferencePGStatXactUserTablesMaintenanceIsolation(t *testing.T) {
	fixture.StatisticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		xactUserCreate(t, p, b, c, "xut_maintenance")
		xactUserSeed(t, p, b, "xut_maintenance")
		fixture.StatisticsExec(t, p, b, c, "SET stats_fetch_consistency=none", 0)
		fixture.StatisticsExec(t, p, b, c, "SET track_counts=off", 0)
		before := tableStatGet(t, p, c, "xut_maintenance")
		if before.Inserts != 3 || before.Vacuum != 0 || before.Analyze != 0 || before.AutoVacuum != 0 || before.AutoAnalyze != 0 || before.LastVacuum.Valid || before.LastAnalyze.Valid || before.LastAutoVacuum.Valid || before.LastAutoAnalyze.Valid {
			t.Fatal("independent fresh completion baseline", before)
		}
		if sys := xactUserCumulative(t, p, c, "xut_maintenance"); !sys.equal(before) {
			t.Fatal("cumulative USER baseline", sys, before)
		}
		for range 3 {
			xactUserGet(t, p, c, "xut_maintenance")
			stmt, err := c.PrepareContext(p.Ctx, xactUserSelect)
			if err != nil {
				t.Fatal(err)
			}
			var r xactTableRow
			if err := stmt.QueryRowContext(p.Ctx, "xut_maintenance").Scan(r.destinations()...); err != nil {
				t.Fatal(err)
			}
			stmt.Close()
			if got := tableStatGet(t, p, c, "xut_maintenance"); !got.equal(before) {
				t.Fatal("USER read changed maintenance", got, before)
			}
			if got := xactUserCumulative(t, p, c, "xut_maintenance"); !got.equal(before) {
				t.Fatal("USER read changed cumulative USER", got, before)
			}
		}
		fixture.StatisticsExec(t, p, b, c, "SET track_counts=on", 0)
		fixture.StatisticsExec(t, p, b, c, "ANALYZE public.xut_maintenance", 0)
		analyzed := tableStatAwait(t, p, b, "xut_maintenance", "completed USER ANALYZE1", func(r tableStatRow) bool { return r.Analyze == 1 })
		if !analyzed.LastAnalyze.Valid || analyzed.Vacuum != 0 || analyzed.LastVacuum.Valid {
			t.Fatal("actual ANALYZE completion", analyzed)
		}
		xactUserMaintenanceUnrelated(t, before, analyzed)
		fixture.StatisticsExec(t, p, b, c, "VACUUM public.xut_maintenance", 0)
		vacuumed := tableStatAwait(t, p, b, "xut_maintenance", "completed USER VACUUM1", func(r tableStatRow) bool { return r.Vacuum == 1 })
		if !vacuumed.LastVacuum.Valid || vacuumed.Analyze != 1 || !vacuumed.LastAnalyze.Time.Equal(analyzed.LastAnalyze.Time) {
			t.Fatal("actual VACUUM completion", vacuumed)
		}
		xactUserMaintenanceUnrelated(t, analyzed, vacuumed)
		fixture.StatisticsExec(t, p, b, c, "SET track_counts=off", 0)
		for range 2 {
			xactUserGet(t, p, c, "xut_maintenance")
			stmt, err := c.PrepareContext(p.Ctx, xactUserSelect)
			if err != nil { t.Fatal(err) }
			var r xactTableRow
			if err := stmt.QueryRowContext(p.Ctx, "xut_maintenance").Scan(r.destinations()...); err != nil { t.Fatal(err) }
			stmt.Close()
			if got := tableStatGet(t, p, c, "xut_maintenance"); !got.equal(vacuumed) {
				t.Fatal("SELECT changed completion", got, vacuumed)
			}
			if got := xactUserCumulative(t, p, c, "xut_maintenance"); !got.equal(vacuumed) {
				t.Fatal("USER completion mismatch", got, vacuumed)
			}
		}
	})
}

func TestReferencePGStatXactUserTablesCancellation(t *testing.T) {
	fixture.StatisticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		xactUserCreate(t, p, b, c, "xut_cancel")
		before := xactUserGet(t, p, c, "xut_cancel")
		fixture.StatisticsExec(t, p, b, c, "SET stats_fetch_consistency=none", 0)
		fixture.StatisticsExec(t, p, b, c, "SET track_counts=off", 0)
		cumulative := tableStatGet(t, p, c, "xut_cancel")
		holder := fixture.IdentityConnection(t, p.Ctx, b.DB)
		retained, err := holder.QueryContext(p.Ctx, xactUserSelect, "xut_cancel")
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		metadata, err := retained.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		rows, err := b.DB.QueryContext(ctx, xactUserSelect, "xut_cancel")
		if rows != nil {
			rows.Close()
		}
		if !errors.Is(err, context.Canceled) {
			t.Fatal("pre-admission Query cancellation", err)
		}
		var state interface{ SQLState() string }
		if errors.As(err, &state) {
			t.Fatal("ordinary context error fabricated SQLSTATE", state.SQLState())
		}
		stmt, err := b.DB.PrepareContext(ctx, xactUserSelect)
		if stmt != nil {
			stmt.Close()
		}
		if !errors.Is(err, context.Canceled) {
			t.Fatal("pre-admission Prepare cancellation", err)
		}
		if errors.As(err, &state) {
			t.Fatal("ordinary prepare error fabricated SQLSTATE", state.SQLState())
		}
		if got := xactUserGet(t, p, c, "xut_cancel"); got != before {
			t.Fatal("cancellation changed USER", got, before)
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT count(*) FROM public.xut_cancel", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		if got := tableStatGet(t, p, c, "xut_cancel"); !got.equal(cumulative) {
			t.Fatal("cancellation changed cumulative statistics", got, cumulative)
		}
		if !retained.Next() {
			t.Fatal("successful retained row lost after cancellation", retained.Err())
		}
		var saved xactTableRow
		if err := retained.Scan(saved.destinations()...); err != nil || saved != before {
			t.Fatal("retained row changed after cancellation", saved, before, err)
		}
		retained.Close()
		xactTableMetadata(t, metadata, b.PG)
	})
}
