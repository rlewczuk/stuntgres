package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceGINMaintenanceStatementStats(t *testing.T) {
	const actor = "gin_maint_stats_actor"
	fixture.RoleReferenceCleanup(t, []string{actor})
	pgssCase(t, true, func(s *pgssScenario) {
		// A fresh principal owns this history even when a previous extension
		// installation left matching normalized text in the cluster store.
		s.exec("CREATE ROLE " + actor + " SUPERUSER")
		s.exec("SET ROLE " + actor)
		s.work(fixture.Exec("SET ROLE "+actor, 0))
		defer func() {
			for _, conn := range []*sql.Conn{s.worker, s.observer} {
				if _, err := conn.ExecContext(s.p.Ctx, "RESET ROLE"); err != nil {
					s.t.Error("reset statistics actor", err)
				}
			}
		}()
		s.exec("CREATE TABLE pgss_part.gin_maint_stats(a integer[])")
		s.exec("CREATE INDEX gin_maint_stats_a ON pgss_part.gin_maint_stats USING gin(a)")
		s.exec("CREATE INDEX gin_maint_stats_b ON pgss_part.gin_maint_stats USING gin(a)")
		s.run(fixture.Exec("INSERT INTO pgss_part.gin_maint_stats VALUES('{1,2}')", 1))

		const pattern = "SELECT /* gin_maint_stats */%"
		s.counts(pattern, 0, 0, 0)
		query := func(target string) string {
			return "SELECT /* gin_maint_stats */ gin_clean_pending_list('pgss_part." + target + "'::regclass) > 0 AS cleaned"
		}
		clean := func(source string, positive, prepared bool) {
			step := fixture.Select(source, []string{"cleaned"}, []string{"BOOL"}, [][]any{{positive}})
			if prepared {
				step = fixture.Prepared(step)
			}
			s.work(step)
		}
		clean(query("gin_maint_stats_a"), true, false)
		clean(query("gin_maint_stats_b"), true, true)
		clean(query("gin_maint_stats_a"), false, true)
		s.counts(pattern, 1, 3, 3)
		s.entry("SELECT /* gin_maint_stats */ gin_clean_pending_list($1::regclass) > $2 AS cleaned", 3, 3)
		// Inspection is untracked and verifies both calls actually drained their
		// independent roots rather than merely completing statement statistics.
		s.both(fixture.Select("SELECT gin_clean_pending_list('pgss_part.gin_maint_stats_a'::regclass) AS first,gin_clean_pending_list('pgss_part.gin_maint_stats_b'::regclass) AS second", []string{"first", "second"}, []string{"INT8", "INT8"}, [][]any{{int64(0), int64(0)}}))
		s.counts(pattern, 1, 3, 3)
	})
}
