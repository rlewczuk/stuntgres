package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDeparseStatisticsPresentation(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "stats_shadow")
	fixture.SchemaReferenceCleanup(t, "stats_moved")
	fixture.RoleReferenceCleanup(t, []string{"stats_reader"})
	fixture.DeparseRelationsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, q := range []string{"CREATE TABLE stats_t(a INT,b INT)", "CREATE STATISTICS stats_s(mcv,dependencies) ON b,a FROM stats_t", "CREATE SCHEMA stats_shadow", "CREATE TABLE stats_shadow.stats_t(a INT,b INT)", "CREATE SCHEMA stats_moved", "CREATE ROLE stats_reader"} {
			run(fixture.Exec(q, 0))
		}
		id := statisticsDeparseID(t, c, "stats_s")
		statisticsDeparseBoth(t, p, b, c, id, statisticsDeparseBase)
		run(fixture.Exec("SET search_path=pg_catalog", 0))
		statisticsDeparseBoth(t, p, b, c, id, "CREATE STATISTICS public.stats_s (dependencies, mcv) ON a, b FROM public.stats_t")
		run(fixture.Exec("SET search_path=stats_shadow,public", 0))
		statisticsDeparseBoth(t, p, b, c, id, "CREATE STATISTICS public.stats_s (dependencies, mcv) ON a, b FROM public.stats_t")
		run(fixture.Exec("SET search_path=public", 0))
		run(fixture.Exec("SET quote_all_identifiers=on", 0))
		statisticsDeparseBoth(t, p, b, c, id, `CREATE STATISTICS "public"."stats_s" (dependencies, mcv) ON "a", "b" FROM "stats_t"`)
		run(fixture.Exec("RESET quote_all_identifiers", 0))
		// Supplied object identity is not a relation SELECT or owner admission.
		run(fixture.Exec("GRANT USAGE ON SCHEMA public TO stats_reader", 0))
		run(fixture.Exec("SET ROLE stats_reader", 0))
		statisticsDeparseBoth(t, p, b, c, id, statisticsDeparseBase)
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("ALTER STATISTICS stats_s SET SCHEMA stats_moved", 0))
		run(fixture.Exec(`ALTER STATISTICS stats_moved.stats_s RENAME TO "S ""quoted"`, 0))
		current := fixture.DeparseDefinitionID(t, c, `SELECT s.oid FROM pg_statistic_ext s JOIN pg_namespace n ON n.oid=s.stxnamespace WHERE n.nspname='stats_moved' AND s.stxname='S "quoted'`)
		if current != id {
			t.Fatal("move/rename replaced identity")
		}
		statisticsDeparseBoth(t, p, b, c, id, `CREATE STATISTICS stats_moved."S ""quoted" (dependencies, mcv) ON a, b FROM stats_t`)
		run(fixture.Exec(`CREATE TABLE "T ""quoted"("A space" INT,"B""quote" INT)`, 0))
		run(fixture.Exec(`CREATE STATISTICS "S space"(dependencies,mcv) ON "B""quote","A space" FROM "T ""quoted"`, 0))
		quoted := statisticsDeparseID(t, c, "S space")
		statisticsDeparseBoth(t, p, b, c, quoted, `CREATE STATISTICS public."S space" (dependencies, mcv) ON "A space", "B""quote" FROM "T ""quoted"`)
	})
}

func TestReferenceDeparseStatisticsExpressions(t *testing.T) {
	for _, tc := range []struct{ selection, want string }{
		{"ON (a+1)", "ON (a + 1)"},
		{"(ndistinct) ON (a+1),b", "(ndistinct) ON b, (a + 1)"},
		{"ON b,(a+1)", "ON b, (a + 1)"},
		{"ON (random())", "ON random()"},
		{"(ndistinct) ON (a+1),(b+2)", "(ndistinct) ON (a + 1), (b + 2)"},
		{"(mcv,ndistinct,dependencies) ON b,(a+1)", "ON b, (a + 1)"},
		{"ON (a::bigint)", "ON (a::bigint)"},
		{"ON (COALESCE(a,0))", "ON COALESCE(a, 0)"},
		{"ON (1+2)", "ON (1 + 2)"},
	} {
		t.Run(tc.selection, func(t *testing.T) {
			fixture.DeparseRelationsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				run(fixture.Exec("CREATE TABLE stats_t(a INT,b INT)", 0))
				run(fixture.Exec("CREATE STATISTICS stats_expr "+tc.selection+" FROM stats_t", 0))
				id := statisticsDeparseID(t, c, "stats_expr")
				want := "CREATE STATISTICS public.stats_expr " + tc.want + " FROM stats_t"
				statisticsDeparseBoth(t, p, b, c, id, want)
				if tc.selection == "ON (a+1)" {
					run(fixture.Exec(`ALTER TABLE stats_t RENAME COLUMN a TO "New A"`, 0))
					statisticsDeparseBoth(t, p, b, c, id, `CREATE STATISTICS public.stats_expr ON ("New A" + 1) FROM stats_t`)
				}
				// Replay the actual public definition, not a handwritten recipe.
				definition := fixture.DeparseRelationsRead(t, p, b, c, statisticsDeparseQuery, id)
				run(fixture.Exec("DROP STATISTICS stats_expr", 0))
				run(fixture.Exec(definition, 0))
				fresh := statisticsDeparseID(t, c, "stats_expr")
				if tc.selection != "ON (a+1)" {
					statisticsDeparseBoth(t, p, b, c, fresh, want)
				} else {
					statisticsDeparseBoth(t, p, b, c, fresh, `CREATE STATISTICS public.stats_expr ON ("New A" + 1) FROM stats_t`)
				}
			})
		})
	}
}
