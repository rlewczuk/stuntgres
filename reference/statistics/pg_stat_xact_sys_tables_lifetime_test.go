package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferencePGStatXactSysTablesLifetime(t *testing.T) {
	fixture.StatisticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		xactSysCreate(t, p, b, c, "xst_lifetime")
		w, retire := fixture.StatisticsReportingConn(t, p, b)
		fixture.StatisticsExec(t, p, b, w, "BEGIN", 0)
		fixture.StatisticsExec(t, p, b, w, "INSERT INTO information_schema.xst_lifetime VALUES(1,NULL),(2,'b')", 2)
		old := xactSysGet(t, p, w, "xst_lifetime")
		xactTableCounts(t, old, 2, 0, 0)
		stmt, err := w.PrepareContext(p.Ctx, "SELECT * FROM pg_catalog.pg_stat_xact_sys_tables WHERE relid=$1")
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
		fixture.StatisticsExec(t, p, b, w, "ALTER TABLE information_schema.xst_lifetime SET SCHEMA public", 0)
		get(false)
		fixture.StatisticsExec(t, p, b, w, "ALTER TABLE public.xst_lifetime SET SCHEMA information_schema", 0)
		xactTableCounts(t, get(true), 2, 0, 0)
		fixture.StatisticsExec(t, p, b, w, "SAVEPOINT moved", 0)
		fixture.StatisticsExec(t, p, b, w, "ALTER TABLE information_schema.xst_lifetime SET SCHEMA public", 0)
		get(false)
		fixture.StatisticsExec(t, p, b, w, "ROLLBACK TO moved", 0)
		xactTableCounts(t, get(true), 2, 0, 0)
		fixture.StatisticsExec(t, p, b, w, "ALTER TABLE information_schema.xst_lifetime RENAME TO xst_renamed", 0)
		if renamed := get(true); renamed.Name != "xst_renamed" || renamed.Schema != "information_schema" {
			t.Fatal("current renamed row", renamed)
		}
		stmt.Close()
		xactTableEnd(t, p, b, w, "ROLLBACK")
		retire()
		if restored := xactSysGet(t, p, c, "xst_lifetime"); restored.ID != old.ID || restored.Name != "xst_lifetime" {
			t.Fatal("DDL rollback membership", restored, old)
		}
		holder := fixture.IdentityConnection(t, p.Ctx, b.DB)
		held, err := holder.QueryContext(p.Ctx, xactSysSelect, "xst_lifetime")
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		columns, err := held.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		fixture.StatisticsExec(t, p, b, c, "DROP TABLE information_schema.xst_lifetime", 0)
		if !held.Next() {
			t.Fatal("retained SYS row absent", held.Err())
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
		if err := c.QueryRowContext(p.Ctx, xactSysSelect, "xst_lifetime").Scan(missing.destinations()...); err != sql.ErrNoRows {
			t.Fatal("dropped SYS member", err)
		}
		tableStatCreate(t, p, b, c, "information_schema.xst_lifetime")
		fresh := xactSysGet(t, p, c, "xst_lifetime")
		xactTableCounts(t, fresh, 0, 0, 0)
		if fresh.ID == old.ID || fresh.Seq != 0 || fresh.Read != 0 {
			t.Fatal("new identity inherited pending work", fresh, old)
		}
		xactTableCounts(t, old, 2, 0, 0)
		if old.Name != "xst_lifetime" {
			t.Fatal("old name mutated", old)
		}
		if !b.PG {
			rows, err := holder.QueryContext(p.Ctx, xactSysSelect, "pg_class")
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			metadata, err := rows.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE TABLE xst_restore(n integer); INSERT INTO xst_restore VALUES(7);")); err != nil {
				t.Fatal(err)
			}
			if !rows.Next() {
				t.Fatal("retained row lost across controls", rows.Err())
			}
			var saved xactTableRow
			if err := rows.Scan(saved.destinations()...); err != nil {
				t.Fatal(err)
			}
			rows.Close()
			xactTableMetadata(t, metadata, false)
			if saved.ID != 1259 || saved.Name != "pg_class" {
				t.Fatal("retained descriptor scalars", saved)
			}
			if got := xactSysGet(t, p, c, "pg_class"); got.ID != 1259 {
				t.Fatal("SYS reinstall", got)
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT n FROM xst_restore", []string{"n"}, []string{"INT4"}, [][]any{{int64(7)}}))
			var view, rowType int64
			if err := c.QueryRowContext(p.Ctx, "SELECT oid,reltype FROM pg_class WHERE oid='pg_stat_xact_sys_tables'::regclass").Scan(&view, &rowType); err != nil || view != 12161 || rowType != 12163 {
				t.Fatal("fixed identities after replacement", view, rowType, err)
			}
		}
	})
}

func TestReferencePGStatXactSysTablesMaintenanceIsolation(t *testing.T) {
	fixture.StatisticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		xactSysCreate(t, p, b, c, "xst_maintenance")
		xactSysSeed(t, p, b, "xst_maintenance")
		fixture.StatisticsExec(t, p, b, c, "SET stats_fetch_consistency=none", 0)
		fixture.StatisticsExec(t, p, b, c, "SET track_counts=off", 0)
		before := tableStatGet(t, p, c, "xst_maintenance")
		if sys := xactSysCumulative(t, p, c, "xst_maintenance"); !sys.equal(before) {
			t.Fatal("cumulative SYS baseline", sys, before)
		}
		for range 3 {
			xactSysGet(t, p, c, "xst_maintenance")
			stmt, err := c.PrepareContext(p.Ctx, xactSysSelect)
			if err != nil {
				t.Fatal(err)
			}
			var r xactTableRow
			if err := stmt.QueryRowContext(p.Ctx, "xst_maintenance").Scan(r.destinations()...); err != nil {
				t.Fatal(err)
			}
			stmt.Close()
			if got := tableStatGet(t, p, c, "xst_maintenance"); !got.equal(before) {
				t.Fatal("SYS read changed maintenance", got, before)
			}
			if got := xactSysCumulative(t, p, c, "xst_maintenance"); !got.equal(before) {
				t.Fatal("SYS read changed cumulative SYS", got, before)
			}
		}
		fixture.StatisticsExec(t, p, b, c, "SET track_counts=on", 0)
		fixture.StatisticsExec(t, p, b, c, "ANALYZE information_schema.xst_maintenance", 0)
		analyzed := tableStatAwait(t, p, b, "xst_maintenance", "completed SYS ANALYZE1", func(r tableStatRow) bool { return r.Analyze == 1 })
		if !analyzed.LastAnalyze.Valid || analyzed.Vacuum != 0 || analyzed.LastVacuum.Valid {
			t.Fatal("actual ANALYZE completion", analyzed)
		}
		fixture.StatisticsExec(t, p, b, c, "VACUUM information_schema.xst_maintenance", 0)
		vacuumed := tableStatAwait(t, p, b, "xst_maintenance", "completed SYS VACUUM1", func(r tableStatRow) bool { return r.Vacuum == 1 })
		if !vacuumed.LastVacuum.Valid || vacuumed.Analyze != 1 || !vacuumed.LastAnalyze.Time.Equal(analyzed.LastAnalyze.Time) {
			t.Fatal("actual VACUUM completion", vacuumed)
		}
		fixture.StatisticsExec(t, p, b, c, "SET track_counts=off", 0)
		for range 2 {
			xactSysGet(t, p, c, "xst_maintenance")
			if got := tableStatGet(t, p, c, "xst_maintenance"); !got.equal(vacuumed) {
				t.Fatal("SELECT changed completion", got, vacuumed)
			}
			if got := xactSysCumulative(t, p, c, "xst_maintenance"); !got.equal(vacuumed) {
				t.Fatal("SYS completion mismatch", got, vacuumed)
			}
		}
	})
}

func TestReferencePGStatXactSysTablesCancellation(t *testing.T) {
	fixture.StatisticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		xactSysCreate(t, p, b, c, "xst_cancel")
		before := xactSysGet(t, p, c, "xst_cancel")
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		rows, err := b.DB.QueryContext(ctx, xactSysSelect, "xst_cancel")
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
		stmt, err := b.DB.PrepareContext(ctx, xactSysSelect)
		if stmt != nil {
			stmt.Close()
		}
		if !errors.Is(err, context.Canceled) {
			t.Fatal("pre-admission Prepare cancellation", err)
		}
		if errors.As(err, &state) {
			t.Fatal("ordinary prepare error fabricated SQLSTATE", state.SQLState())
		}
		if got := xactSysGet(t, p, c, "xst_cancel"); got != before {
			t.Fatal("cancellation changed SYS", got, before)
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT count(*) FROM information_schema.xst_cancel", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
	})
}
