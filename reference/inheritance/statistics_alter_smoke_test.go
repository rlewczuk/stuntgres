package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceStatisticsAlterFeatureSmoke(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			fixture.SchemaReferenceCleanup(t, "stats_alter_smoke")
			fixture.RoleReferenceCleanup(t, []string{"statistics_smoke_recipient"})
			p := fixture.NewScenario(t)
			var saved bytes.Buffer
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					var notices []fixture.Diagnostic
					_, conn, rec := fixture.NoticeReferencePool(t, p, b.PG, &notices)
					run := func(step fixture.Step) { step.Prepared = prepared; fixture.RunStep(t, p.Ctx, conn, rec, step) }
					s := ndistinctScenario{run: run, pg: b.PG, conn: conn, pair: p, prepared: prepared}
					for _, q := range []string{"CREATE ROLE statistics_smoke_recipient", "CREATE SCHEMA stats_alter_smoke", "GRANT USAGE,CREATE ON SCHEMA stats_alter_smoke TO statistics_smoke_recipient", "CREATE TABLE public.stats_alter_input(a INT,b INT)", "CREATE STATISTICS public.stats_alter_before (ndistinct,dependencies,mcv) ON a,b FROM public.stats_alter_input"} {
						run(fixture.Exec(q, 0))
					}
					run(fixture.Exec("INSERT INTO stats_alter_input VALUES(1,10),(1,10),(2,20),(2,20)", 4))
					run(fixture.Exec("ANALYZE stats_alter_input", 0))
					oid := fixture.DeparseDefinitionID(t, conn, "SELECT oid FROM pg_statistic_ext WHERE stxname='stats_alter_before'")
					facts := func(name string) {
						run(ndistinctRows(b.PG, []any{name, false, `{"1, 2": 2}`}))
						run(dependenciesRows(b.PG, []any{name, false, dependenciesFull}))
						mcvAssert(t, s, name, mcvExpected{"1,10", .5, .25}, mcvExpected{"2,20", .5, .25})
					}
					facts("stats_alter_before")
					run(fixture.Exec("ALTER STATISTICS public.stats_alter_before RENAME TO stats_alter_after", 0))
					run(statisticsError("ALTER STATISTICS public.stats_alter_before SET STATISTICS 0", "42704", `statistics object "public.stats_alter_before" does not exist`))
					facts("stats_alter_after")
					if got := fixture.DeparseDefinitionID(t, conn, "SELECT oid FROM pg_statistic_ext WHERE stxname='stats_alter_after'"); got != oid {
						t.Fatal("rename replaced identity")
					}
					run(fixture.Exec("ALTER STATISTICS public.stats_alter_after SET SCHEMA stats_alter_smoke", 0))
					facts("stats_alter_after")
					run(fixture.Exec("ALTER STATISTICS stats_alter_smoke.stats_alter_after OWNER TO statistics_smoke_recipient", 0))
					facts("stats_alter_after")
					for _, tc := range []struct {
						sql    string
						target any
						warn   bool
					}{{"0", int64(0), false}, {"10000", int64(10000), false}, {"10001", int64(10000), true}, {"DEFAULT", nil, false}, {"-1", nil, false}} {
						notices = nil
						run(fixture.Exec("ALTER STATISTICS stats_alter_smoke.stats_alter_after SET STATISTICS "+tc.sql, 0))
						var want []fixture.Diagnostic
						if tc.warn {
							want = []fixture.Diagnostic{{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "22023", Message: "lowering statistics target to 10000"}}
						}
						if !reflect.DeepEqual(notices, want) {
							t.Fatal(notices, want)
						}
						run(statisticsAlterMetadata("stats_alter_after", "stats_alter_smoke", "statistics_smoke_recipient", tc.target))
						facts("stats_alter_after")
					}
					run(statisticsError("ALTER STATISTICS stats_alter_smoke.stats_alter_after RENAME TO stats_alter_after", "42710", `statistics object "stats_alter_after" already exists in schema "stats_alter_smoke"`))
					run(statisticsError("ALTER STATISTICS stats_alter_smoke.stats_alter_after SET STATISTICS -2", "22023", "statistics target -2 is too low"))
					facts("stats_alter_after")
					run(fixture.Select("SELECT count(*) FROM stats_alter_input", []string{"count"}, []string{"INT8"}, [][]any{{int64(4)}}))
					run(fixture.Exec("REVOKE CREATE ON SCHEMA stats_alter_smoke FROM statistics_smoke_recipient", 0))
					if !b.PG {
						if err := stuntgres.Dump(p.Ctx, p.Native, &saved); err != nil {
							t.Fatal(err)
						}
						if saved.Len() == 0 {
							t.Fatal("empty smoke dump")
						}
						driver := stuntgres.NewDriver()
						connector, err := driver.OpenConnector(t.Name() + "-restored")
						if err != nil {
							t.Fatal(err)
						}
						other := sql.OpenDB(connector)
						defer other.Close()
						if _, err = other.ExecContext(p.Ctx, "CREATE ROLE statistics_smoke_recipient; CREATE TABLE collision(v INT)"); err != nil {
							t.Fatal(err)
						}
						if err = stuntgres.Restore(p.Ctx, other, bytes.NewReader(saved.Bytes())); err != nil {
							t.Fatal(err)
						}
						recorded, recorder := fixture.RecordedNativePool(t, connector)
						fixture.RunStep(t, p.Ctx, recorded, recorder, statisticsAlterMetadata("stats_alter_after", "stats_alter_smoke", "statistics_smoke_recipient", nil))
						fixture.RunStep(t, p.Ctx, recorded, recorder, ndistinctRows(false, []any{"stats_alter_after", false, `{"1, 2": 2}`}))
						fixture.RunStep(t, p.Ctx, recorded, recorder, dependenciesRows(false, []any{"stats_alter_after", false, dependenciesFull}))
						var again bytes.Buffer
						if err = stuntgres.Dump(p.Ctx, other, &again); err != nil {
							t.Fatal(err)
						}
						if !bytes.Equal(saved.Bytes(), again.Bytes()) {
							t.Fatal("smoke redump changed")
						}
					} else {
						run(fixture.Exec("DROP SCHEMA stats_alter_smoke CASCADE", 0))
						run(fixture.Exec("DROP TABLE stats_alter_input", 0))
						replay := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
						defer replay.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
						if _, err := replay.ExecContext(p.Ctx, saved.String()); err != nil {
							t.Fatal("actual smoke declaration replay", err)
						}
						fixture.RunStep(t, p.Ctx, replay, b.Recorder, statisticsAlterMetadata("stats_alter_after", "stats_alter_smoke", "statistics_smoke_recipient", nil))
					}
					if !b.PG {
						if got := fixture.DeparseDefinitionID(t, conn, "SELECT oid FROM pg_statistic_ext WHERE stxname='stats_alter_after'"); got != oid {
							t.Fatal("metadata changes replaced original")
						}
					}
				})
			}
		})
	}
}
