package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

const statisticsDeparseQuery = "SELECT pg_get_statisticsobjdef($1) AS definition"
const statisticsDeparseBase = "CREATE STATISTICS public.stats_s (dependencies, mcv) ON a, b FROM stats_t"

func statisticsDeparseID(t *testing.T, c *sql.Conn, name string) int64 {
	t.Helper()
	return fixture.DeparseDefinitionID(t, c, "SELECT s.oid FROM pg_statistic_ext s JOIN pg_namespace n ON n.oid=s.stxnamespace WHERE n.nspname='public' AND s.stxname='"+name+"'")
}

func statisticsDeparseBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, id int64, want any) {
	t.Helper()
	s := fixture.DeparseRelationsTextStep(t, b, "SELECT pg_catalog.pg_get_statisticsobjdef($1::oid) AS definition", want, id)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
}

func TestReferenceDeparseStatisticsDefinitions(t *testing.T) {
	for _, tc := range []struct{ kinds, clause string }{
		{"(mcv,dependencies)", " (dependencies, mcv)"},
		{"(mcv,dependencies,ndistinct)", ""}, {"", ""},
		{"(ndistinct)", " (ndistinct)"}, {"(dependencies)", " (dependencies)"}, {"(mcv)", " (mcv)"},
	} {
		t.Run(tc.kinds, func(t *testing.T) {
			fixture.DeparseRelationsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				run(fixture.Exec("CREATE TABLE stats_t(a INT,b INT,c INT)", 0))
				run(fixture.Exec("CREATE STATISTICS IF NOT EXISTS stats_all"+tc.kinds+" ON b,a FROM stats_t", 0))
				run(fixture.Exec("ALTER STATISTICS stats_all SET STATISTICS 42", 0))
				id := statisticsDeparseID(t, c, "stats_all")
				statisticsDeparseBoth(t, p, b, c, id, "CREATE STATISTICS public.stats_all"+tc.clause+" ON a, b FROM stats_t")
				run(fixture.Exec("ALTER TABLE stats_t RENAME TO stats_new", 0))
				statisticsDeparseBoth(t, p, b, c, id, "CREATE STATISTICS public.stats_all"+tc.clause+" ON a, b FROM stats_new")
				run(fixture.Exec("ALTER STATISTICS stats_all RENAME TO stats_renamed", 0))
				if statisticsDeparseID(t, c, "stats_renamed") != id {
					t.Fatal("rename replaced statistics identity")
				}
				statisticsDeparseBoth(t, p, b, c, id, "CREATE STATISTICS public.stats_renamed"+tc.clause+" ON a, b FROM stats_new")
			})
		})
	}
	statisticsDeparseLifetime(t)
}

func statisticsDeparseLifetime(t *testing.T) {
	t.Helper()
	fixture.DeparseRelationsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE stats_t(a INT,b INT)", 0))
		run(fixture.Exec("CREATE STATISTICS stats_s(mcv,dependencies) ON b,a FROM stats_t", 0))
		id := statisticsDeparseID(t, c, "stats_s")
		held, err := c.PrepareContext(p.Ctx, statisticsDeparseQuery)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		e := fixture.HeldStatement{Executor: c, Statement: held, SQL: statisticsDeparseQuery}
		check := func(arg, want any) {
			fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.DeparseRelationsTextStep(t, b, statisticsDeparseQuery, want, arg))
		}
		for _, arg := range []any{id, nil, int64(0), id} {
			var want any
			if arg == id {
				want = statisticsDeparseBase
			}
			check(arg, want)
		}
		// Consume/close the PostgreSQL cursor before DDL so no server lock is
		// held across rename. The detached old row and metadata remain usable.
		rows, err := held.QueryContext(p.Ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		fixture.DeparseRelationsMetadata(t, b.PG, rows)
		columns, err := rows.ColumnTypes()
		if err != nil {
			rows.Close()
			t.Fatal(err)
		}
		if !rows.Next() {
			rows.Close()
			t.Fatal(rows.Err())
		}
		var old string
		if err := rows.Scan(&old); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		run(fixture.Exec(`ALTER TABLE stats_t RENAME COLUMN a TO "New A"`, 0))
		want := `CREATE STATISTICS public.stats_s (dependencies, mcv) ON "New A", b FROM stats_t`
		check(id, want)
		statisticsDeparseBoth(t, p, b, c, id, want)
		if old != statisticsDeparseBase || columns[0].Name() != "definition" || columns[0].DatabaseTypeName() != "TEXT" {
			t.Fatal("old capture mutated", old, columns)
		}
		run(fixture.Exec("DROP STATISTICS stats_s", 0))
		check(id, nil)
		run(fixture.Exec(`CREATE STATISTICS stats_s(mcv,dependencies) ON b,"New A" FROM stats_t`, 0))
		fresh := statisticsDeparseID(t, c, "stats_s")
		statisticsDeparseBoth(t, p, b, c, fresh, want)
		check(id, nil)
	})
}

func TestReferenceDeparseStatisticsFeatureSmoke(t *testing.T) {
	fixture.DeparseRelationsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE stats_t(a INT,b INT)", 0))
		run(fixture.Exec("CREATE STATISTICS stats_s(mcv,dependencies) ON b,a FROM stats_t", 0))
		id := statisticsDeparseID(t, c, "stats_s")
		statisticsDeparseBoth(t, p, b, c, id, statisticsDeparseBase)
		held, err := c.PrepareContext(p.Ctx, statisticsDeparseQuery)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		e := fixture.HeldStatement{Executor: c, Statement: held, SQL: statisticsDeparseQuery}
		check := func(arg, want any) {
			fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.DeparseRelationsTextStep(t, b, statisticsDeparseQuery, want, arg))
		}
		check(id, statisticsDeparseBase)
		run(fixture.Exec(`ALTER TABLE stats_t RENAME COLUMN a TO "New A"`, 0))
		check(id, `CREATE STATISTICS public.stats_s (dependencies, mcv) ON "New A", b FROM stats_t`)
		check(nil, nil)
		check(int64(0), nil)
		run(fixture.Exec("SET search_path=pg_catalog", 0))
		check(id, `CREATE STATISTICS public.stats_s (dependencies, mcv) ON "New A", b FROM public.stats_t`)
		w := fixture.ControlsReferenceDiagnostic("42883", "function pg_get_statisticsobjdef(integer, boolean) does not exist")
		w.Position = "8"
		w.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
		s := fixture.ErrorDiagnostic("SELECT pg_get_statisticsobjdef(0,true)", w)
		s.SelectRows = true
		run(s)
		run(fixture.Exec("RESET search_path", 0))
	})
}
