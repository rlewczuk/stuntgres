package reference_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceDeparseStatisticsControls(t *testing.T) {
	p := fixture.NewScenario(t)
	native := p.Backends()[0]
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, native.DB, native.Recorder, s) }
	for _, q := range []string{
		"CREATE TABLE stats_t(a INT,gone INT,c INT)",
		"ALTER TABLE stats_t DROP COLUMN gone", "ALTER TABLE stats_t RENAME COLUMN c TO b",
		`ALTER TABLE stats_t RENAME COLUMN a TO "New A"`,
		`CREATE STATISTICS stats_s(mcv,dependencies) ON b,"New A" FROM stats_t`,
		`CREATE STATISTICS stats_expr ON ("New A"+1) FROM stats_t`,
		"ALTER STATISTICS stats_s SET STATISTICS 42",
		"CREATE VIEW stats_v AS SELECT pg_get_statisticsobjdef(oid) AS definition FROM pg_statistic_ext WHERE stxname='stats_s'",
	} {
		run(fixture.Exec(q, 0))
	}
	run(fixture.Exec("INSERT INTO stats_t VALUES(1,2),(3,4)", 2))
	run(fixture.Exec("ANALYZE stats_t", 0))
	want := `CREATE STATISTICS public.stats_s (dependencies, mcv) ON "New A", b FROM stats_t`
	wantExpr := `CREATE STATISTICS public.stats_expr ON ("New A" + 1) FROM stats_t`
	var saved bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Len() == 0 || !strings.Contains(saved.String(), "CREATE STATISTICS") || !strings.Contains(saved.String(), "pg_get_statisticsobjdef") {
		t.Fatal("missing genuine state or stored call")
	}
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			check := func(b fixture.Backend) {
				c, err := b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer c.Close()
				for _, tc := range []struct{ name, want string }{{"stats_s", want}, {"stats_expr", wantExpr}} {
					id := statisticsDeparseID(t, c, tc.name)
					statisticsDeparseBoth(t, p, b, c, id, tc.want)
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.DeparseRelationsTextStep(t, b, "SELECT definition FROM stats_v", want))
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT * FROM stats_t ORDER BY b", []string{"New A", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}, {int64(3), int64(4)}}))
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT s.stxstattarget,r.relname,n.nspname,s.stxowner=p.oid AS owner_ok FROM pg_statistic_ext s JOIN pg_class r ON r.oid=s.stxrelid JOIN pg_namespace n ON n.oid=s.stxnamespace JOIN pg_roles p ON p.rolname='postgres' WHERE s.stxname='stats_s'", []string{"stxstattarget", "relname", "nspname", "owner_ok"}, []string{"INT2", "NAME", "NAME", "BOOL"}, [][]any{{int64(42), "stats_t", "public", true}}))
				if !b.PG {
					// PostgreSQL replays actual native SQL but intentionally ignores
					// native collected-state comments. Native replacement retains it.
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT d.stxddependencies IS NOT NULL AS dependencies,d.stxdmcv IS NOT NULL AS mcv FROM pg_statistic_ext_data d JOIN pg_statistic_ext s ON s.oid=d.stxoid WHERE s.stxname='stats_s' AND NOT d.stxdinherit", []string{"dependencies", "mcv"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}))
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT d.stxdexpr IS NOT NULL AS expressions FROM pg_statistic_ext_data d JOIN pg_statistic_ext s ON s.oid=d.stxoid WHERE s.stxname='stats_expr' AND NOT d.stxdinherit", []string{"expressions"}, []string{"BOOL"}, [][]any{{true}}))
				}
			}
			if b.PG {
				c := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
				defer c.Close()
				if _, err := c.ExecContext(p.Ctx, saved.String()); err != nil {
					t.Fatal("actual native dump rejected by PostgreSQL", err)
				}
				if _, err := c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION"); err != nil {
					t.Fatal(err)
				}
				check(b)
				return
			}
			check(b)
			old, err := p.Native.QueryContext(p.Ctx, "SELECT definition FROM stats_v")
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
			fixture.DeparseRelationsMetadata(t, false, old)
			columns, err := old.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			fixture.DeparseRelationsMetadata(t, false, old)
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(saved.Bytes())); err != nil {
				t.Fatal(err)
			}
			fixture.DeparseRelationsMetadata(t, false, old)
			if !old.Next() {
				t.Fatal("old rendered row lost", old.Err())
			}
			var retained string
			if err := old.Scan(&retained); err != nil {
				t.Fatal(err)
			}
			if retained != want || old.Next() || old.Err() != nil {
				t.Fatal("old rendered value mutated", retained, old.Err())
			}
			if columns[0].Name() != "definition" || columns[0].DatabaseTypeName() != "TEXT" {
				t.Fatal("old metadata mutated")
			}
			old.Close()
			check(b)
			var again bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &again); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(saved.Bytes(), again.Bytes()) {
				t.Fatal("native redump changed", saved.String(), again.String())
			}
			for _, suffix := range []string{"CREATE STATISTICS bad ON missing,b FROM stats_t;", "CREATE STATISTICS bad(bogus) ON b,\"New A\" FROM stats_t;"} {
				if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(saved.String()+suffix)); err == nil {
					t.Fatal("failed import published")
				}
				check(b)
			}
			canceled, cancel := context.WithCancel(p.Ctx)
			cancel()
			if err := stuntgres.Restore(canceled, p.Native, bytes.NewReader(saved.Bytes())); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			check(b)
			driver := stuntgres.NewDriver()
			connector, err := driver.OpenConnector(t.Name() + "-collision")
			if err != nil {
				t.Fatal(err)
			}
			other, raw, recorder := fixture.DatabaseConnectorScenarioPool(t, connector, false)
			if _, err := raw.ExecContext(p.Ctx, "CREATE TABLE collision(v INT); INSERT INTO collision VALUES(99)"); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, raw, strings.NewReader(saved.String()+"CREATE STATISTICS bad ON missing,b FROM stats_t;")); err == nil {
				t.Fatal("invalid destination import published")
			}
			fixture.RunStep(t, p.Ctx, other, recorder, fixture.Select("SELECT v FROM collision", []string{"v"}, []string{"INT4"}, [][]any{{int64(99)}}))
			if err := stuntgres.Restore(p.Ctx, raw, bytes.NewReader(saved.Bytes())); err != nil {
				t.Fatal(err)
			}
			// check independently resolves destination OIDs and relation links;
			// source numeric identities are never treated as portable authority.
			check(fixture.Backend{DB: other, Recorder: recorder})
		})
	}
}
