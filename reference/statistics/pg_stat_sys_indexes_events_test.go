package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGStatSysIndexesEvents(t *testing.T) {
	fixture.StatisticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		sysIndexCreate(t, p, b, c, "information_schema", "six_events")
		sysIndexAccesses(t, p, b, c, "six_events")
	})
}

func TestReferencePGStatSysIndexesLifetime(t *testing.T) {
	fixture.StatisticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		sysIndexCreate(t, p, b, c, "information_schema", "six_lifetime")
		before := sysIndexAccesses(t, p, b, c, "six_lifetime")
		sysIndexPositiveMetadata(t, p, b, c, before.Name, before.Last.Valid)
		stmt, err := c.PrepareContext(p.Ctx, sysIndexSelect)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		retained, err := sysIndexHeld(t, p, stmt, before.Name)
		if err != nil || !retained.equal(before) {
			t.Fatal("initial held SYS read", retained, before, err)
		}
		fixture.StatisticsExec(t, p, b, c, "ALTER INDEX information_schema.six_lifetime_hash RENAME TO six_lifetime_renamed", 0)
		want := before
		want.Name = "six_lifetime_renamed"
		assertHeld := func() {
			t.Helper()
			got, err := sysIndexHeld(t, p, stmt, want.Name)
			if err != nil || !got.equal(want) || !sysIndexGet(t, p, c, want.Name).equal(want) || !indexStatGet(t, p, c, want.Name).equal(want) {
				t.Fatal("current held membership/identity/history", got, want, err)
			}
		}
		assertHeld()
		if _, err := sysIndexHeld(t, p, stmt, before.Name); err != sql.ErrNoRows {
			t.Fatal("old index name retained", err)
		}
		fixture.StatisticsExec(t, p, b, c, "ALTER TABLE information_schema.six_lifetime SET SCHEMA public", 0)
		want.Schema = "public"
		if _, err := sysIndexHeld(t, p, stmt, want.Name); err != sql.ErrNoRows {
			t.Fatal("held SYS retained public parent", err)
		}
		if _, err := sysIndexRead(p.Ctx, c, want.Name); err != sql.ErrNoRows {
			t.Fatal("direct SYS retained public parent", err)
		}
		if got := indexStatGet(t, p, c, want.Name); !got.equal(want) {
			t.Fatal("moving parent lost ALL identity/history", got, want)
		}
		fixture.StatisticsExec(t, p, b, c, "ALTER TABLE public.six_lifetime SET SCHEMA information_schema", 0)
		want.Schema = "information_schema"
		assertHeld()
		fixture.StatisticsExec(t, p, b, c, "DROP INDEX information_schema.six_lifetime_renamed", 0)
		if _, err := sysIndexHeld(t, p, stmt, want.Name); err != sql.ErrNoRows {
			t.Fatal("held SYS retained dropped identity", err)
		}
		if _, err := sysIndexRead(p.Ctx, c, want.Name); err != sql.ErrNoRows {
			t.Fatal("SYS retained dropped identity", err)
		}
		if _, err := indexStatRead(p.Ctx, c, want.Name); err != sql.ErrNoRows {
			t.Fatal("ALL retained dropped identity", err)
		}
		fixture.StatisticsExec(t, p, b, c, "CREATE INDEX six_lifetime_renamed ON information_schema.six_lifetime USING hash(n)", 0)
		fresh, err := sysIndexHeld(t, p, stmt, want.Name)
		if err != nil || fresh.Index == before.Index || fresh.Parent != before.Parent || fresh.Name != want.Name || fresh.Schema != "information_schema" || fresh.Table != before.Table {
			t.Fatal("recreated current identity", fresh, before, err)
		}
		indexStatWant(t, fresh, 0, 0, 0)
		if !indexStatGet(t, p, c, want.Name).equal(fresh) || !retained.equal(before) {
			t.Fatal("fresh shared facts/retained values", fresh, retained, before)
		}
		sysIndexPositiveMetadata(t, p, b, c, want.Name, false)
	})
}

func TestReferencePGStatSysIndexesFeatureSmoke(t *testing.T) {
	fixture.StatisticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		sysIndexCreate(t, p, b, c, "information_schema", "six_smoke")
		sysIndexCreate(t, p, b, c, "public", "six_smoke_public")
		initial := sysIndexGet(t, p, c, "six_smoke_hash")
		indexStatWant(t, initial, 0, 0, 0)
		sysIndexPositiveMetadata(t, p, b, c, initial.Name, false)
		positive := sysIndexAccesses(t, p, b, c, "six_smoke")
		if !b.PG && (positive.Scans != 3 || positive.Entries != 4 || positive.Fetches != 4 || !positive.Last.Valid) {
			t.Fatal("native smoke requires real direct/prepared hit and miss events", positive)
		}
		sysIndexPositiveMetadata(t, p, b, c, positive.Name, positive.Last.Valid)
		sysIndexPartition(t, p, c, "six_smoke_hash", "six_smoke_public_hash")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.ErrorDiagnostic("UPDATE pg_catalog.pg_stat_sys_indexes SET idx_scan=0 WHERE FALSE", indexStatMutationDiagnostic("UPDATE")))
		if got := sysIndexShared(t, p, b, c, positive.Name); !got.equal(positive) {
			t.Fatal("zero-row UPDATE altered shared statistics", got, positive)
		}
		fixture.StatisticsExec(t, p, b, c, "DROP INDEX information_schema.six_smoke_hash", 0)
		if _, err := sysIndexRead(p.Ctx, c, positive.Name); err != sql.ErrNoRows {
			t.Fatal("smoke dropped SYS index remains", err)
		}
		if _, err := indexStatRead(p.Ctx, c, positive.Name); err != sql.ErrNoRows {
			t.Fatal("smoke dropped ALL index remains", err)
		}
	})
}
