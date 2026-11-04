package reference_test

import (
	"context"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceStatisticsViewsLifecycle(t *testing.T) {
	withStatisticsViews(t, func(t *testing.T, s statisticsViewsScenario) {
		statisticsViewsPopulation(s)
		s.exec("CREATE STATISTICS sv_ab(ndistinct) ON a,b FROM expr_t", "ANALYZE expr_t")
		held := statisticsViewsHeld(t, s, "SELECT most_common_vals::text FROM pg_stats_ext_exprs WHERE statistics_name='sv_expr'")
		defer held.Close()
		readHeld := func(want string) {
			t.Helper()
			var got string
			if err := held.QueryRowContext(s.pair.Ctx).Scan(&got); err != nil || got != want {
				t.Fatalf("held expression %q want%q: %v", got, want, err)
			}
		}
		readHeld("{2,3}")
		peer, err := s.backend.DB.Conn(s.pair.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		// Keep the genuine cursor unread across a peer publication. No copied
		// Go observation substitutes for retained provider-owned rows.
		old, closeRows := s.query(t, "SELECT most_common_vals,most_common_freqs FROM pg_stats_ext_exprs WHERE statistics_name='sv_expr'")
		defer closeRows()
		for _, step := range []fixture.Step{fixture.Exec("UPDATE expr_t SET a=9,b=90", 20), fixture.Exec("ANALYZE expr_t(b)", 0)} {
			fixture.RunStep(t, s.pair.Ctx, peer, s.backend.Recorder, step)
		}
		statisticsViewsReadHeldSkew(t, s, old)
		readHeld("{10}")
		s.run(statisticsViewsSkew("pg_stats", "tablename='expr_t' AND attname='a' AND NOT inherited", "{1,2}"))
		s.run(statisticsViewsUniform("sv_expr", "{10}"))
		s.run(statisticsViewsUniform("sv_joint", "{10}"))
		statisticsViewsMCV(t, s, "sv_joint", statisticsViewsMCVItem{[]any{"90", "10"}, 1, 1})
		s.run(fixture.Select("SELECT n_distinct::text AS nd FROM pg_stats_ext WHERE statistics_name='sv_ab'", []string{"nd"}, []string{"TEXT"}, [][]any{{`{"1, 2": 3}`}}))
		s.exec("ALTER STATISTICS sv_joint SET STATISTICS 1")
		statisticsViewsMCV(t, s, "sv_joint", statisticsViewsMCVItem{[]any{"90", "10"}, 1, 1})
		s.exec("ANALYZE expr_t")
		s.run(fixture.Select("SELECT null_frac,n_distinct,most_common_vals::text AS vals FROM pg_stats WHERE tablename='expr_t' AND attname='a' AND NOT inherited", []string{"null_frac", "n_distinct", "vals"}, []string{"FLOAT4", "FLOAT4", "TEXT"}, [][]any{{float64(0), float64(1), "{9}"}}))
		s.run(fixture.Exec("DELETE FROM expr_t", 20))
		s.exec("ANALYZE expr_t")
		// Empty collection does not synthesize a new population or discard an
		// old measured one. Contrast the fresh-empty MissingAnalysis witness.
		s.run(statisticsViewsUniform("sv_expr", "{10}"))
		statisticsViewsMCV(t, s, "sv_joint", statisticsViewsMCVItem{[]any{"90", "10"}, 1, 1})
		s.exec("ALTER TABLE expr_t RENAME COLUMN a TO renamed")
		s.run(fixture.Select("SELECT expr,most_common_vals::text AS vals FROM pg_stats_ext_exprs WHERE statistics_name='sv_expr'", []string{"expr", "vals"}, []string{"TEXT", "TEXT"}, [][]any{{"(renamed + 1)", "{10}"}}))
		s.exec("ALTER TABLE expr_t ALTER COLUMN renamed TYPE bigint")
		s.run(statisticsViewsCount("pg_stats_ext", "statistics_name IN ('sv_expr','sv_joint','sv_ab')", 0))
		s.run(fixture.Select("SELECT statistics_name,inherited,null_frac FROM pg_stats_ext_exprs WHERE tablename='expr_t' ORDER BY statistics_name", []string{"statistics_name", "inherited", "null_frac"}, []string{"NAME", "BOOL", "FLOAT4"}, [][]any{{"sv_expr", nil, nil}, {"sv_joint", nil, nil}}))
		s.exec("ALTER TABLE expr_t DROP COLUMN renamed")
		for _, view := range []string{"pg_stats_ext", "pg_stats_ext_exprs"} {
			s.run(statisticsViewsCount(view, "tablename='expr_t'", 0))
		}
		s.run(statisticsViewsCount("pg_stats", "tablename='expr_t' AND attname='renamed'", 0))
		statisticsViewsNativeControls(t, s)
	})
}

func TestReferenceStatisticsViewsTransactions(t *testing.T) {
	withStatisticsViews(t, func(t *testing.T, s statisticsViewsScenario) {
		statisticsViewsPopulation(s)
		held := statisticsViewsHeld(t, s, "SELECT count(*) FROM pg_stats_ext WHERE statistics_name='sv_joint'")
		defer held.Close()
		statisticsViewsHeldCount(t, s, held, 0)
		s.exec("ANALYZE expr_t")
		statisticsViewsHeldCount(t, s, held, 1)
		peer, err := s.backend.DB.Conn(s.pair.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		defer s.conn.ExecContext(context.Background(), "ROLLBACK")
		observe := func(want string) {
			t.Helper()
			fixture.RunStep(t, s.pair.Ctx, peer, s.backend.Recorder, fixture.Select("SELECT most_common_vals::text AS vals FROM pg_stats_ext_exprs WHERE statistics_name='sv_expr'", []string{"vals"}, []string{"TEXT"}, [][]any{{want}}))
		}
		s.exec("BEGIN")
		s.run(fixture.Exec("UPDATE expr_t SET a=9,b=90", 20))
		s.exec("ANALYZE expr_t")
		s.run(statisticsViewsUniform("sv_expr", "{10}"))
		observe("{2,3}")
		s.exec("SAVEPOINT sv_before")
		s.run(fixture.Exec("UPDATE expr_t SET a=19,b=190", 20))
		s.exec("ANALYZE expr_t(b)")
		s.run(statisticsViewsUniform("sv_expr", "{20}"))
		s.exec("ROLLBACK TO sv_before")
		s.run(statisticsViewsUniform("sv_expr", "{10}"))
		s.exec("ROLLBACK")
		s.run(statisticsViewsSkew("pg_stats_ext_exprs", "statistics_name='sv_expr' AND NOT inherited", "{2,3}"))
		statisticsViewsSkewMCV(t, s)
		observe("{2,3}")
		s.exec("BEGIN")
		s.run(fixture.Exec("UPDATE expr_t SET a=9,b=90", 20))
		s.exec("ANALYZE expr_t(b)", "COMMIT")
		observe("{10}")
		// Private DDL changes the left/inner joins in the owning transaction;
		// its peer still sees the old committed definition and collected data.
		s.exec("BEGIN", "SAVEPOINT sv_ddl", "ALTER STATISTICS sv_expr RENAME TO sv_private")
		s.run(statisticsViewsCount("pg_stats_ext_exprs", "statistics_name='sv_private'", 1))
		fixture.RunStep(t, s.pair.Ctx, peer, s.backend.Recorder, statisticsViewsCount("pg_stats_ext_exprs", "statistics_name='sv_expr'", 1))
		s.exec("DROP STATISTICS sv_private")
		s.run(statisticsViewsCount("pg_stats_ext_exprs", "statistics_name='sv_private'", 0))
		s.exec("ROLLBACK TO sv_ddl")
		s.run(statisticsViewsUniform("sv_expr", "{10}"))
		s.exec("CREATE TABLE sv_private_t(a INT,b INT)", "CREATE STATISTICS sv_private_s ON b,(a+1) FROM sv_private_t")
		s.run(statisticsViewsCount("pg_stats_ext", "statistics_name='sv_private_s'", 0))
		s.run(fixture.Select("SELECT expr,inherited FROM pg_stats_ext_exprs WHERE statistics_name='sv_private_s'", []string{"expr", "inherited"}, []string{"TEXT", "BOOL"}, [][]any{{"(a + 1)", nil}}))
		s.run(fixture.Exec("INSERT INTO sv_private_t VALUES(7,70),(7,70),(7,70),(7,70)", 4))
		s.exec("ANALYZE sv_private_t")
		s.run(statisticsViewsCount("pg_stats_ext", "statistics_name='sv_private_s'", 1))
		fixture.RunStep(t, s.pair.Ctx, peer, s.backend.Recorder, statisticsViewsCount("pg_stats_ext_exprs", "statistics_name='sv_private_s'", 0))
		s.exec("ROLLBACK")
		s.run(statisticsViewsCount("pg_stats_ext_exprs", "statistics_name='sv_private_s'", 0))
		observe("{10}")
	})
}
