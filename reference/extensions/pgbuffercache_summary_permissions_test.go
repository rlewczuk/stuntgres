package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgBuffercacheSummaryPermissions(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"bc_sum_reader", "bc_sum_monitor", "bc_sum_noinherit", "bc_sum_stats", "bc_sum_scan", "bc_sum_super"})
	bcScenario(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, admin *sql.Conn) {
		bcDDL(t, p, b, admin,
			"CREATE ROLE bc_sum_reader", "CREATE ROLE bc_sum_monitor", "CREATE ROLE bc_sum_noinherit NOINHERIT", "CREATE ROLE bc_sum_stats", "CREATE ROLE bc_sum_scan", "CREATE ROLE bc_sum_super SUPERUSER",
			"GRANT USAGE ON SCHEMA bc_part TO bc_sum_reader,bc_sum_monitor,bc_sum_noinherit,bc_sum_stats,bc_sum_scan,bc_sum_super,pg_monitor",
			"GRANT pg_monitor TO bc_sum_monitor,bc_sum_noinherit", "GRANT pg_read_all_stats TO bc_sum_stats", "GRANT pg_stat_scan_tables TO bc_sum_scan",
			"CREATE TABLE bc_part.summary_secret(k integer)")
		bcRun(t, p, b, admin, fixture.Exec("INSERT INTO bc_part.summary_secret VALUES(1)", 1))
		actor, err := b.DB.Conn(p.Ctx)
		fixture.State(t, err, "")
		defer actor.Close()
		defer actor.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		queries := []string{"SELECT count(*) AS n FROM bc_part.pg_buffercache_summary()", "SELECT count(*) AS n FROM bc_part.pg_buffercache_usage_counts()"}
		var held []fixture.HeldStatement
		for _, q := range queries {
			st, err := actor.PrepareContext(p.Ctx, q)
			fixture.State(t, err, "")
			defer st.Close()
			held = append(held, fixture.HeldStatement{Executor: actor, Statement: st, SQL: q})
		}
		check := func(allowed [2]bool) {
			t.Helper()
			for i, q := range queries {
				s := bcError(q, "42501", "permission denied for function "+bcsRoutines[i])
				if allowed[i] {
					n := int64(1)
					if i == 1 {
						n = 6
					}
					s = fixture.Select(q, []string{"n"}, []string{"INT8"}, [][]any{{n}})
					bcBoth(t, p, b, actor, s)
				} else {
					bcFailure(t, p, b, actor, s, "execute")
				}
				bcRun(t, p, b, held[i], s)
			}
		}
		check([2]bool{true, true})
		for _, role := range []string{"bc_sum_reader", "bc_sum_noinherit", "bc_sum_stats", "bc_sum_scan"} {
			bcDDL(t, p, b, actor, "SET SESSION AUTHORIZATION "+role)
			check([2]bool{})
			bcDDL(t, p, b, actor, "RESET SESSION AUTHORIZATION")
		}
		for _, role := range []string{"bc_sum_super", "bc_sum_monitor"} {
			bcDDL(t, p, b, actor, "SET SESSION AUTHORIZATION "+role)
			check([2]bool{true, true})
			bcDDL(t, p, b, actor, "RESET SESSION AUTHORIZATION")
		}
		bcDDL(t, p, b, actor, "SET SESSION AUTHORIZATION bc_sum_noinherit", "SET ROLE pg_monitor")
		check([2]bool{true, true})
		bcDDL(t, p, b, actor, "RESET ROLE", "RESET SESSION AUTHORIZATION", "SET SESSION AUTHORIZATION bc_sum_reader")
		bcDDL(t, p, b, admin, "GRANT EXECUTE ON FUNCTION bc_part.pg_buffercache_summary() TO bc_sum_reader")
		check([2]bool{true, false})
		bcFailure(t, p, b, actor, bcError("SELECT k FROM bc_part.summary_secret", "42501", "permission denied for table summary_secret"), "execute")
		bcFailure(t, p, b, actor, bcError("SELECT * FROM bc_part.pg_buffercache", "42501", "permission denied for view pg_buffercache"), "execute")
		bcFailure(t, p, b, actor, bcError("SELECT * FROM "+bcFunction(), "42501", "permission denied for function pg_buffercache_pages"), "execute")
		bcDDL(t, p, b, admin, "REVOKE EXECUTE ON FUNCTION bc_part.pg_buffercache_summary() FROM bc_sum_reader", "GRANT EXECUTE ON FUNCTION bc_part.pg_buffercache_usage_counts() TO bc_sum_reader")
		check([2]bool{false, true})
		bcDDL(t, p, b, admin, "GRANT EXECUTE ON FUNCTION bc_part.pg_buffercache_summary() TO bc_sum_reader")
		check([2]bool{true, true})
		// Revoke/regrant by a different administrator must invalidate an
		// already accepted held execution, without touching its owner session.
		bcDDL(t, p, b, admin, "REVOKE EXECUTE ON FUNCTION bc_part.pg_buffercache_usage_counts() FROM bc_sum_reader")
		check([2]bool{true, false})
		bcDDL(t, p, b, admin, "GRANT EXECUTE ON FUNCTION bc_part.pg_buffercache_usage_counts() TO bc_sum_reader", "REVOKE USAGE ON SCHEMA bc_part FROM bc_sum_reader")
		for i, q := range queries {
			s := bcError(q, "42501", "permission denied for schema bc_part")
			s.Diagnostic.Position = fixture.Position(strings.Index(q, "bc_part") + 1)
			bcFailure(t, p, b, actor, s, "prepare")
			s.ErrorStage = "execute"
			bcRun(t, p, b, held[i], s)
		}
		bcDDL(t, p, b, admin, "GRANT USAGE ON SCHEMA bc_part TO bc_sum_reader")
		check([2]bool{true, true})
		bcDDL(t, p, b, admin, "REVOKE EXECUTE ON FUNCTION bc_part.pg_buffercache_summary(),bc_part.pg_buffercache_usage_counts() FROM bc_sum_reader", "GRANT EXECUTE ON FUNCTION bc_part.pg_buffercache_summary() TO PUBLIC")
		check([2]bool{true, false})
		bcDDL(t, p, b, admin, "REVOKE EXECUTE ON FUNCTION bc_part.pg_buffercache_summary() FROM PUBLIC", "GRANT EXECUTE ON FUNCTION bc_part.pg_buffercache_usage_counts() TO PUBLIC")
		check([2]bool{false, true})
		bcDDL(t, p, b, admin, "REVOKE EXECUTE ON FUNCTION bc_part.pg_buffercache_usage_counts() FROM PUBLIC")
		check([2]bool{})
	})
}
