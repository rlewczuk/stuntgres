package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceStatisticsViewsFeatureSmoke(t *testing.T) {
	withStatisticsViews(t, func(t *testing.T, s statisticsViewsScenario) {
		statisticsViewsPopulation(s)
		statisticsViewsMissing(s, "expr_t", [][]any{statisticsViewsUnbuilt("sv_expr", "(a + 1)"), statisticsViewsUnbuilt("sv_joint", "(a + 1)")})
		heldCount := statisticsViewsHeld(t, s, "SELECT count(*) FROM pg_stats_ext WHERE tablename='expr_t'")
		defer heldCount.Close()
		heldValues := statisticsViewsHeld(t, s, "SELECT most_common_vals::text FROM pg_stats_ext_exprs WHERE statistics_name='sv_expr'")
		defer heldValues.Close()
		statisticsViewsHeldCount(t, s, heldCount, 0)
		var missing sql.NullString
		if err := heldValues.QueryRowContext(s.pair.Ctx).Scan(&missing); err != nil || missing.Valid {
			t.Fatal("unbuilt held expression", missing, err)
		}
		s.exec("ANALYZE expr_t")
		statisticsViewsHeldCount(t, s, heldCount, 2)
		s.run(statisticsViewsSkew("pg_stats", "tablename='expr_t' AND attname='a' AND NOT inherited", "{1,2}"))
		s.run(statisticsViewsSkew("pg_stats_ext_exprs", "statistics_name='sv_expr' AND NOT inherited", "{2,3}"))
		s.run(fixture.Select("SELECT attnames,exprs,kinds,inherited FROM pg_stats_ext WHERE statistics_name='sv_joint'", []string{"attnames", "exprs", "kinds", "inherited"}, []string{"_NAME", "_TEXT", "_CHAR", "BOOL"}, [][]any{{"{b}", `{"(a + 1)"}`, "{d,f,m,e}", false}}))
		statisticsViewsSkewMCV(t, s)
		for _, role := range []string{"sv_smoke_owner", "sv_smoke_reader", "sv_smoke_member"} {
			s.exec("CREATE ROLE " + role)
		}
		s.exec("GRANT USAGE ON SCHEMA public TO sv_smoke_owner,sv_smoke_reader,sv_smoke_member", "ALTER TABLE expr_t OWNER TO sv_smoke_owner", "GRANT SELECT(a) ON expr_t TO sv_smoke_reader", "GRANT sv_smoke_owner TO sv_smoke_member WITH INHERIT TRUE, SET FALSE")
		defer func() {
			for _, query := range []string{"RESET ROLE", "REVOKE USAGE ON SCHEMA public FROM sv_smoke_owner,sv_smoke_reader,sv_smoke_member"} {
				if _, err := s.conn.ExecContext(context.Background(), query); err != nil {
					t.Error("smoke cleanup", err)
				}
			}
		}()
		s.exec("SET ROLE sv_smoke_reader")
		s.run(fixture.Select("SELECT attname FROM pg_stats WHERE tablename='expr_t' ORDER BY attname", []string{"attname"}, []string{"NAME"}, [][]any{{"a"}}))
		statisticsViewsHeldCount(t, s, heldCount, 0)
		s.run(statisticsViewsCount("pg_stats_ext_exprs", "tablename='expr_t'", 0))
		s.exec("RESET ROLE", "SET ROLE sv_smoke_member")
		statisticsViewsHeldCount(t, s, heldCount, 2)
		s.run(statisticsViewsSkew("pg_stats_ext_exprs", "statistics_name='sv_expr' AND NOT inherited", "{2,3}"))
		s.exec("RESET ROLE")
		reader, err := s.backend.DB.Conn(s.pair.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer reader.Close()
		capture := statisticsViewsControlScenario(t, s, reader, s.backend)
		old, closeRows := capture.query(t, "SELECT most_common_vals,most_common_freqs FROM pg_stats_ext_exprs WHERE statistics_name='sv_expr'")
		defer closeRows()
		s.run(fixture.Exec("UPDATE expr_t SET a=9,b=90", 20))
		s.run(statisticsViewsSkew("pg_stats_ext_exprs", "statistics_name='sv_expr' AND NOT inherited", "{2,3}"))
		statisticsViewsSkewMCV(t, s)
		s.exec("ANALYZE expr_t(b)")
		s.run(statisticsViewsUniform("sv_expr", "{10}"))
		statisticsViewsMCV(t, s, "sv_joint", statisticsViewsMCVItem{[]any{"90", "10"}, 1, 1})
		var refreshed string
		if err := heldValues.QueryRowContext(s.pair.Ctx).Scan(&refreshed); err != nil || refreshed != "{10}" {
			t.Fatal("held refresh", refreshed, err)
		}
		statisticsViewsReadHeldSkew(t, capture, old)
		s.run(statisticsViewsFailure("SELECT missing FROM pg_stats_ext_exprs", "42703", `column "missing" does not exist`, s.prepared))
		statisticsViewsHeldCount(t, s, heldCount, 2)
		if !s.pg {
			s.run(fixture.Exec("UPDATE expr_t SET a=19,b=190", 20))
			var saved bytes.Buffer
			if err := stuntgres.Dump(s.pair.Ctx, s.pair.Native, &saved); err != nil {
				t.Fatal(err)
			}
			if saved.Len() == 0 {
				t.Fatal("empty smoke dump")
			}
			if err := stuntgres.Reset(s.pair.Ctx, s.pair.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(s.pair.Ctx, s.pair.Native, bytes.NewReader(saved.Bytes())); err != nil {
				t.Fatal(err)
			}
			s.run(statisticsViewsUniform("sv_expr", "{10}"))
			statisticsViewsMCV(t, s, "sv_joint", statisticsViewsMCVItem{[]any{"90", "10"}, 1, 1})
			s.run(fixture.Select("SELECT min(a),max(a),count(*) FROM expr_t", []string{"min", "max", "count"}, []string{"INT4", "INT4", "INT8"}, [][]any{{int64(19), int64(19), int64(20)}}))
			s.exec("ANALYZE expr_t")
			s.run(statisticsViewsUniform("sv_expr", "{20}"))
		}
	}, "sv_smoke_owner", "sv_smoke_reader", "sv_smoke_member")
}
