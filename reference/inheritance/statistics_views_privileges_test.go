package reference_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

var statisticsViewsRoles = []string{"sv_o", "sv_s", "sv_r", "sv_m", "sv_i", "sv_n", "sv_u", "sv_b"}

func TestReferenceStatisticsViewsPrivileges(t *testing.T) {
	withStatisticsViews(t, func(t *testing.T, s statisticsViewsScenario) {
		for _, role := range statisticsViewsRoles {
			options := ""
			if role == "sv_u" {
				options = " SUPERUSER"
			}
			if role == "sv_n" {
				options = " NOINHERIT"
			}
			if role == "sv_b" {
				options = " BYPASSRLS"
			}
			s.exec("CREATE ROLE " + role + options)
		}
		s.exec("GRANT USAGE ON SCHEMA public TO " + strings.Join(statisticsViewsRoles, ","))
		defer func() {
			for _, query := range []string{"RESET ROLE", "GRANT SELECT ON pg_catalog.pg_stats,pg_catalog.pg_stats_ext,pg_catalog.pg_stats_ext_exprs TO PUBLIC", "REVOKE USAGE ON SCHEMA public FROM " + strings.Join(statisticsViewsRoles, ",")} {
				if _, err := s.conn.ExecContext(context.Background(), query); err != nil {
					t.Error("privilege cleanup", query, err)
				}
			}
		}()
		statisticsViewsPopulation(s)
		s.exec("ANALYZE expr_t", "ALTER TABLE expr_t OWNER TO sv_o", "ALTER STATISTICS sv_expr OWNER TO sv_s", "ALTER STATISTICS sv_joint OWNER TO sv_s", "GRANT SELECT(a) ON expr_t TO sv_r", "GRANT MAINTAIN ON expr_t TO sv_m", "GRANT sv_o TO sv_i WITH INHERIT TRUE, SET FALSE", "GRANT sv_o TO sv_n WITH INHERIT FALSE, SET TRUE")
		s.run(fixture.Select("SELECT r.rolname,m.inherit_option,m.set_option FROM pg_auth_members m JOIN pg_roles r ON r.oid=m.member JOIN pg_roles o ON o.oid=m.roleid WHERE o.rolname='sv_o' ORDER BY r.rolname", []string{"rolname", "inherit_option", "set_option"}, []string{"NAME", "BOOL", "BOOL"}, [][]any{{"sv_i", true, false}, {"sv_n", false, true}}))
		queries := []string{"SELECT count(*) FROM pg_stats WHERE tablename='expr_t' AND attname IN ('a','b')", "SELECT count(*) FROM pg_stats_ext WHERE tablename='expr_t'", "SELECT count(*) FROM pg_stats_ext_exprs WHERE tablename='expr_t'"}
		held := make([]fixture.HeldStatement, len(queries))
		for i, query := range queries {
			held[i] = fixture.HeldStatement{Executor: s.conn, Statement: statisticsViewsHeld(t, s, query), SQL: query}
			defer held[i].Statement.Close()
		}
		assert := func(role string, columns, extended int64) {
			t.Helper()
			s.exec("RESET ROLE", "SET ROLE "+role)
			for i, query := range queries {
				want := extended
				if i == 0 {
					want = columns
				}
				step := fixture.Select(query, []string{"count"}, []string{"INT8"}, [][]any{{want}})
				s.run(step)
				fixture.RunStep(t, s.pair.Ctx, held[i], s.backend.Recorder, step)
			}
		}
		for _, tc := range []struct {
			role              string
			columns, extended int64
		}{
			{"sv_o", 2, 2}, {"sv_s", 0, 0}, {"sv_r", 1, 0}, {"sv_m", 0, 0}, {"sv_i", 2, 2}, {"sv_n", 0, 0}, {"sv_u", 2, 2}, {"sv_b", 0, 0},
		} {
			assert(tc.role, tc.columns, tc.extended)
		}
		s.exec("RESET ROLE", "GRANT pg_read_all_stats TO sv_r")
		assert("sv_r", 1, 0)
		s.exec("RESET ROLE", "GRANT SELECT(b) ON expr_t TO sv_r")
		assert("sv_r", 2, 0)
		s.exec("RESET ROLE", "REVOKE SELECT(a) ON expr_t FROM sv_r")
		assert("sv_r", 1, 0)
		s.exec("RESET ROLE", "REVOKE sv_o FROM sv_i")
		assert("sv_i", 0, 0)
		s.exec("RESET ROLE", "GRANT sv_o TO sv_i WITH INHERIT TRUE, SET FALSE", "GRANT sv_o TO sv_b WITH INHERIT TRUE, SET FALSE")
		assert("sv_i", 2, 2)
		assert("sv_b", 2, 2)
		s.run(fixture.Select("SELECT statistics_owner FROM pg_stats_ext WHERE statistics_name='sv_expr'", []string{"statistics_owner"}, []string{"NAME"}, [][]any{{"sv_s"}}))
		// Owning the represented table permits these public views but does not
		// confer SELECT on the restricted collected-data catalog.
		s.exec("RESET ROLE", "SET ROLE sv_o")
		dataDenied := fixture.ErrorDiagnostic("SELECT * FROM pg_statistic_ext_data WHERE FALSE", fixture.ControlsReferenceDiagnostic("42501", "permission denied for table pg_statistic_ext_data"))
		dataDenied.SelectRows, dataDenied.ErrorStage = true, "execute"
		s.run(dataDenied)

		s.exec("RESET ROLE", "ALTER TABLE expr_t ENABLE ROW LEVEL SECURITY")
		assert("sv_o", 2, 2)
		assert("sv_i", 2, 2)
		assert("sv_r", 0, 0)
		s.exec("RESET ROLE", "ALTER TABLE expr_t FORCE ROW LEVEL SECURITY")
		assert("sv_o", 0, 0)
		assert("sv_i", 0, 0)
		assert("sv_b", 2, 2)
		assert("sv_u", 2, 2)
		s.exec("RESET ROLE", "SET ROLE sv_o", "SET row_security=off")
		for _, query := range queries {
			s.run(fixture.Select(query, []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		}
		s.exec("RESET row_security", "RESET ROLE", "ALTER TABLE expr_t NO FORCE ROW LEVEL SECURITY", "ALTER TABLE expr_t DISABLE ROW LEVEL SECURITY")
		statisticsViewsRevokedRows(t, s)

		for i, view := range []string{"pg_stats", "pg_stats_ext", "pg_stats_ext_exprs"} {
			s.exec("RESET ROLE", "REVOKE SELECT ON pg_catalog."+view+" FROM PUBLIC", "SET ROLE sv_o")
			want := fixture.ControlsReferenceDiagnostic("42501", "permission denied for view "+view)
			for _, query := range []string{queries[i], "SELECT * FROM pg_catalog." + view + " WHERE FALSE"} {
				step := fixture.ErrorDiagnostic(query, want)
				step.SelectRows, step.ErrorStage = true, "execute"
				s.run(step)
			}
			step := fixture.ErrorDiagnostic(queries[i], want)
			step.SelectRows, step.ErrorStage = true, "execute"
			fixture.RunStep(t, s.pair.Ctx, held[i], s.backend.Recorder, step)
			s.exec("RESET ROLE", "GRANT SELECT ON pg_catalog."+view+" TO PUBLIC", "SET ROLE sv_o")
			fixture.RunStep(t, s.pair.Ctx, held[i], s.backend.Recorder, fixture.Select(queries[i], []string{"count"}, []string{"INT8"}, [][]any{{int64(2)}}))
		}
		s.exec("RESET ROLE", "SET ROLE sv_o")
		statisticsViewsMutationDiagnostics(t, s)
	}, statisticsViewsRoles...)
}

func statisticsViewsMutationDiagnostics(t *testing.T, s statisticsViewsScenario) {
	t.Helper()
	for _, view := range []string{"pg_stats", "pg_stats_ext", "pg_stats_ext_exprs"} {
		for _, operation := range []struct{ query, verb, command string }{
			{"UPDATE pg_catalog." + view + " SET tablename='bad' WHERE FALSE", "update", "UPDATE"},
			{"DELETE FROM pg_catalog." + view + " WHERE FALSE", "delete from", "DELETE"},
			{"INSERT INTO pg_catalog." + view + "(tablename) VALUES('bad')", "insert into", "INSERT"},
		} {
			want := fixture.ControlsReferenceDiagnostic("55000", fmt.Sprintf("cannot %s view %q", operation.verb, view))
			want.Detail = "Views that do not select from a single table or view are not automatically updatable."
			want.Hint = "To enable " + map[string]string{"UPDATE": "updating", "DELETE": "deleting from", "INSERT": "inserting into"}[operation.command] + " the view, provide an INSTEAD OF " + operation.command + " trigger or an unconditional ON " + operation.command + " DO INSTEAD rule."
			step := fixture.ErrorDiagnostic(operation.query, want)
			step.ErrorStage = "execute"
			if s.prepared {
				step.ErrorStage = "prepare"
			}
			s.run(step)
		}
	}
	s.run(statisticsViewsSkew("pg_stats", "tablename='expr_t' AND attname='a' AND NOT inherited", "{1,2}"))
	s.run(statisticsViewsSkew("pg_stats_ext_exprs", "statistics_name='sv_expr' AND NOT inherited", "{2,3}"))
	statisticsViewsSkewMCV(t, s)
}

func statisticsViewsRevokedRows(t *testing.T, s statisticsViewsScenario) {
	t.Helper()
	reader, err := s.backend.DB.Conn(s.pair.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer reader.ExecContext(context.Background(), "RESET ROLE")
	if _, err := reader.ExecContext(s.pair.Ctx, "SET ROLE sv_o"); err != nil {
		t.Fatal(err)
	}
	capture := statisticsViewsControlScenario(t, s, reader, s.backend)
	old, closeRows := capture.query(t, "SELECT most_common_vals,most_common_freqs FROM pg_stats_ext_exprs WHERE statistics_name='sv_expr'")
	defer closeRows()
	s.exec("REVOKE SELECT ON pg_catalog.pg_stats_ext_exprs FROM PUBLIC")
	statisticsViewsReadHeldSkew(t, capture, old)
	step := fixture.ErrorDiagnostic("SELECT * FROM pg_stats_ext_exprs WHERE FALSE", fixture.ControlsReferenceDiagnostic("42501", "permission denied for view pg_stats_ext_exprs"))
	step.SelectRows, step.ErrorStage, step.Prepared = true, "execute", s.prepared
	fixture.RunStep(t, s.pair.Ctx, reader, s.backend.Recorder, step)
	s.exec("GRANT SELECT ON pg_catalog.pg_stats_ext_exprs TO PUBLIC")
	check := statisticsViewsSkew("pg_stats_ext_exprs", "statistics_name='sv_expr' AND NOT inherited", "{2,3}")
	check.Prepared = s.prepared
	fixture.RunStep(t, s.pair.Ctx, reader, s.backend.Recorder, check)
}
