package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGStatUserIndexesEvents(t *testing.T) {
	fixture.StatisticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		sysIndexCreate(t, p, b, c, "public", "uix_events")
		userIndexAccesses(t, p, b, c, "uix_events")
	})
}

func TestReferencePGStatUserIndexesLifetime(t *testing.T) {
	fixture.StatisticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		sysIndexCreate(t, p, b, c, "public", "uix_lifetime")
		before := userIndexAccesses(t, p, b, c, "uix_lifetime")
		userIndexPositiveMetadata(t, p, b, c, before.Name, before.Last.Valid)
		stmt, err := c.PrepareContext(p.Ctx, userIndexSelect)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		allStmt, err := c.PrepareContext(p.Ctx, indexStatSelect)
		if err != nil {
			t.Fatal(err)
		}
		defer allStmt.Close()
		sysStmt, err := c.PrepareContext(p.Ctx, sysIndexSelect)
		if err != nil {
			t.Fatal(err)
		}
		defer sysStmt.Close()
		rows, err := stmt.QueryContext(p.Ctx, before.Name)
		if err != nil {
			t.Fatal(err)
		}
		columns, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		indexStatMetadata(t, columns, b.PG)
		rows.Close()
		retained, err := userIndexHeld(t, p, stmt, before.Name)
		if err != nil || !retained.equal(before) {
			t.Fatal("initial held USER read", retained, before, err)
		}
		fixture.StatisticsExec(t, p, b, c, "ALTER INDEX public.uix_lifetime_hash RENAME TO uix_lifetime_renamed", 0)
		want := before
		want.Name = "uix_lifetime_renamed"
		assertHeld := func() {
			t.Helper()
			got, err := userIndexHeld(t, p, stmt, want.Name)
			if err != nil || !got.equal(want) || !userIndexGet(t, p, c, want.Name).equal(want) || !indexStatGet(t, p, c, want.Name).equal(want) {
				t.Fatal("current held membership/identity/history", got, want, err)
			}
		}
		assertHeld()
		if _, err := userIndexHeld(t, p, stmt, before.Name); err != sql.ErrNoRows {
			t.Fatal("old index name retained", err)
		}
		sysIndexOwned(t, b, "uix_lifetime_table")
		fixture.StatisticsExec(t, p, b, c, "ALTER TABLE public.uix_lifetime RENAME TO uix_lifetime_table", 0)
		want.Table = "uix_lifetime_table"
		assertHeld()
		var oldLabels int
		if err := c.QueryRowContext(p.Ctx, "SELECT count(*) FROM pg_stat_user_indexes WHERE relname='uix_lifetime'").Scan(&oldLabels); err != nil || oldLabels != 0 {
			t.Fatal("old parent label retained", oldLabels, err)
		}
		fixture.StatisticsExec(t, p, b, c, "ALTER TABLE public.uix_lifetime_table SET SCHEMA information_schema", 0)
		want.Schema = "information_schema"
		if _, err := userIndexHeld(t, p, stmt, want.Name); err != sql.ErrNoRows {
			t.Fatal("held USER retained information_schema parent", err)
		}
		if _, err := userIndexRead(p.Ctx, c, want.Name); err != sql.ErrNoRows {
			t.Fatal("direct USER retained information_schema parent", err)
		}
		if got := indexStatGet(t, p, c, want.Name); !got.equal(want) {
			t.Fatal("moving parent lost ALL identity/history", got, want)
		}
		if got := sysIndexGet(t, p, c, want.Name); !got.equal(want) {
			t.Fatal("SYS must admit moved parent", got, want)
		}
		if got, err := userIndexHeld(t, p, sysStmt, want.Name); err != nil || !got.equal(want) {
			t.Fatal("held SYS must admit moved parent", got, want, err)
		}
		fixture.StatisticsExec(t, p, b, c, "ALTER TABLE information_schema.uix_lifetime_table SET SCHEMA public", 0)
		want.Schema = "public"
		assertHeld()
		if _, err := sysIndexRead(p.Ctx, c, want.Name); err != sql.ErrNoRows {
			t.Fatal("SYS retained user parent", err)
		}
		fixture.StatisticsExec(t, p, b, c, "DROP INDEX public.uix_lifetime_renamed", 0)
		if _, err := userIndexHeld(t, p, stmt, want.Name); err != sql.ErrNoRows {
			t.Fatal("held USER retained dropped identity", err)
		}
		if _, err := userIndexRead(p.Ctx, c, want.Name); err != sql.ErrNoRows {
			t.Fatal("USER retained dropped identity", err)
		}
		if _, err := indexStatRead(p.Ctx, c, want.Name); err != sql.ErrNoRows {
			t.Fatal("ALL retained dropped identity", err)
		}
		for _, held := range []*sql.Stmt{allStmt, sysStmt} {
			if _, err := userIndexHeld(t, p, held, want.Name); err != sql.ErrNoRows {
				t.Fatal("held family retained dropped index", err)
			}
		}
		if _, err := sysIndexRead(p.Ctx, c, want.Name); err != sql.ErrNoRows {
			t.Fatal("SYS retained dropped index", err)
		}
		fixture.StatisticsExec(t, p, b, c, "CREATE INDEX uix_lifetime_renamed ON public.uix_lifetime_table USING hash(n)", 0)
		fresh, err := userIndexHeld(t, p, stmt, want.Name)
		if err != nil || fresh.Index == before.Index || fresh.Parent != before.Parent || fresh.Name != want.Name || fresh.Schema != "public" || fresh.Table != want.Table {
			t.Fatal("recreated current identity", fresh, before, err)
		}
		indexStatWant(t, fresh, 0, 0, 0)
		if !indexStatGet(t, p, c, want.Name).equal(fresh) || !retained.equal(before) {
			t.Fatal("fresh shared facts/retained values", fresh, retained, before)
		}
		userIndexPositiveMetadata(t, p, b, c, want.Name, false)
		fixture.StatisticsExec(t, p, b, c, "DROP TABLE public.uix_lifetime_table", 0)
		for _, held := range []*sql.Stmt{stmt, allStmt, sysStmt} {
			if _, err := userIndexHeld(t, p, held, want.Name); err != sql.ErrNoRows {
				t.Fatal("parent drop left held orphan", err)
			}
		}
		for _, read := range []func() (indexStatRow, error){
			func() (indexStatRow, error) { return userIndexRead(p.Ctx, c, want.Name) },
			func() (indexStatRow, error) { return indexStatRead(p.Ctx, c, want.Name) },
			func() (indexStatRow, error) { return sysIndexRead(p.Ctx, c, want.Name) },
		} {
			if _, err := read(); err != sql.ErrNoRows {
				t.Fatal("parent drop left direct orphan", err)
			}
		}
		if !retained.equal(before) {
			t.Fatal("retained result mutated after drop", retained, before)
		}
		indexStatMetadata(t, columns, b.PG)
	})
}

func TestReferencePGStatUserIndexesFeatureSmoke(t *testing.T) {
	fixture.StatisticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		sysIndexCreate(t, p, b, c, "public", "uix_smoke")
		sysIndexCreate(t, p, b, c, "information_schema", "uix_smoke_information_schema")
		initial := userIndexGet(t, p, c, "uix_smoke_hash")
		indexStatWant(t, initial, 0, 0, 0)
		userIndexPositiveMetadata(t, p, b, c, initial.Name, false)
		positive := userIndexAccesses(t, p, b, c, "uix_smoke")
		if !b.PG && (positive.Scans != 3 || positive.Entries != 4 || positive.Fetches != 4 || !positive.Last.Valid) {
			t.Fatal("native smoke requires real direct/prepared hit and miss events", positive)
		}
		userIndexPositiveMetadata(t, p, b, c, positive.Name, positive.Last.Valid)
		held, err := c.PrepareContext(p.Ctx, userIndexSelect)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		if got, err := userIndexHeld(t, p, held, positive.Name); err != nil || !got.equal(positive) {
			t.Fatal("smoke held positive", got, positive, err)
		}
		sysIndexPartition(t, p, c, "uix_smoke_information_schema_hash", "uix_smoke_hash")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.ErrorDiagnostic("UPDATE pg_catalog.pg_stat_user_indexes SET idx_scan=0 WHERE FALSE", indexStatMutationDiagnostic("UPDATE")))
		if got := userIndexShared(t, p, b, c, positive.Name); !got.equal(positive) {
			t.Fatal("zero-row UPDATE altered shared statistics", got, positive)
		}
		fixture.StatisticsExec(t, p, b, c, "DROP INDEX public.uix_smoke_hash", 0)
		if _, err := userIndexRead(p.Ctx, c, positive.Name); err != sql.ErrNoRows {
			t.Fatal("smoke dropped USER index remains", err)
		}
		if _, err := indexStatRead(p.Ctx, c, positive.Name); err != sql.ErrNoRows {
			t.Fatal("smoke dropped ALL index remains", err)
		}
		if _, err := userIndexHeld(t, p, held, positive.Name); err != sql.ErrNoRows {
			t.Fatal("smoke dropped held USER index remains", err)
		}
	})
}
