package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceVacuumSelection(t *testing.T) {
	vacuumScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		c := vacuumConn(t, p, b)
		run := func(s fixture.Step) { vacuumRun(t, p, b, c, s) }
		run(fixture.Exec("CREATE TABLE vacuum_selection(id INT,v TEXT)", 0))
		run(fixture.Exec("INSERT INTO vacuum_selection VALUES(1,'a'),(2,NULL),(2,'b'),(3,'c')", 4))
		run(fixture.Exec("DELETE FROM vacuum_selection WHERE id=1", 1))
		check := func() {
			run(fixture.Select("SELECT id,v FROM vacuum_selection ORDER BY id,v NULLS FIRST", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(2), nil}, {int64(2), "b"}, {int64(3), "c"}}))
		}
		options := []string{"", "FREEZE", "FREEZE false,FREEZE true", "FULL false,ANALYZE false", "SKIP_LOCKED", "INDEX_CLEANUP", "INDEX_CLEANUP AUTO", "INDEX_CLEANUP ON", "INDEX_CLEANUP OFF", "INDEX_CLEANUP true", "INDEX_CLEANUP false", "PROCESS_MAIN false", "PROCESS_TOAST false", "PROCESS_MAIN false,PROCESS_TOAST false", "DISABLE_PAGE_SKIPPING", "TRUNCATE false", "TRUNCATE true", "PARALLEL 0", "PARALLEL 2", "PARALLEL 1024", "SKIP_DATABASE_STATS", "BUFFER_USAGE_LIMIT 0", "BUFFER_USAGE_LIMIT '128kB'", "BUFFER_USAGE_LIMIT '16MB'"}
		for _, option := range options {
			q := "VACUUM "
			if option != "" {
				q += "(" + option + ") "
			}
			q += "public.vacuum_selection"
			t.Run(option, func(t *testing.T) {
				vacuumRun(t, p, b, c, fixture.Exec(q, 0))
				h := vacuumHeld(t, p, c, q)
				vacuumRun(t, p, b, h, fixture.Exec(q, 0))
				check()
			})
		}
		for _, q := range []string{"VACUUM FREEZE vacuum_selection", "VACUUM ONLY vacuum_selection", "VACUUM vacuum_selection *", "VACUUM vacuum_selection,public.vacuum_selection", "VACUUM (ONLY_DATABASE_STATS)", "VACUUM"} {
			run(fixture.Exec(q, 0))
			check()
		}
		for _, options := range []string{
			"ONLY_DATABASE_STATS,VERBOSE,PROCESS_MAIN false,PROCESS_TOAST false",
			"ONLY_DATABASE_STATS,INDEX_CLEANUP ON,TRUNCATE true,BUFFER_USAGE_LIMIT '128kB',PARALLEL 2",
			"ONLY_DATABASE_STATS,SKIP_LOCKED false,FULL false,ANALYZE false,FREEZE false,DISABLE_PAGE_SKIPPING false,SKIP_DATABASE_STATS false",
		} {
			q := "VACUUM (" + options + ")"
			run(fixture.Exec(q, 0))
			vacuumRun(t, p, b, vacuumHeld(t, p, c, q), fixture.Exec(q, 0))
			check()
		}
	})
	t.Run("descendants_and_temporary", func(t *testing.T) {
		vacuumScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			c := vacuumConn(t, p, b)
			other := vacuumConn(t, p, b)
			run := func(s fixture.Step) { vacuumRun(t, p, b, c, s) }
			for _, q := range []string{"CREATE TABLE vacuum_parent(id INT)", "CREATE TABLE vacuum_child() INHERITS(vacuum_parent)", "CREATE TABLE vacuum_partition(id INT) PARTITION BY RANGE(id)", "CREATE TABLE vacuum_leaf PARTITION OF vacuum_partition FOR VALUES FROM(0) TO(10)", "CREATE TEMP TABLE vacuum_own_temp(id INT)"} {
				run(fixture.Exec(q, 0))
			}
			run(fixture.Exec("CREATE MATERIALIZED VIEW vacuum_mv AS SELECT 1 AS id", 1))
			vacuumRun(t, p, b, other, fixture.Exec("CREATE TEMP TABLE vacuum_foreign_temp(id INT)", 0))
			for _, q := range []string{"VACUUM ONLY vacuum_parent", "VACUUM vacuum_parent *", "VACUUM vacuum_partition", "VACUUM vacuum_mv", "VACUUM vacuum_own_temp", "VACUUM"} {
				run(fixture.Exec(q, 0))
			}
			vacuumCount(t, p.Ctx, c, "vacuum_parent", 3)
			vacuumCount(t, p.Ctx, c, "vacuum_child", 2)
			vacuumCount(t, p.Ctx, c, "vacuum_leaf", 2)
			vacuumCount(t, p.Ctx, c, "vacuum_own_temp", 2)
			vacuumCount(t, p.Ctx, other, "vacuum_foreign_temp", 0)
		})
	})
	t.Run("partition_only_warning", func(t *testing.T) {
		vacuumScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			var notices []fixture.Diagnostic
			_, c, rec := fixture.NoticeReferencePool(t, p, b.PG, &notices)
			fixture.RunStep(t, p.Ctx, c, rec, fixture.Exec("CREATE TABLE vacuum_only_partition(id INT) PARTITION BY RANGE(id)", 0))
			q := "VACUUM ONLY vacuum_only_partition"
			h := vacuumHeld(t, p, c, q)
			for _, executor := range []fixture.Executor{c, h} {
				notices = nil
				fixture.RunStep(t, p.Ctx, executor, rec, fixture.Exec(q, 0))
				vacuumWarning(t, notices, "01000", `VACUUM ONLY of partitioned table "vacuum_only_partition" has no effect`)
			}
			vacuumCount(t, p.Ctx, c, "vacuum_only_partition", 0)
		})
	})
	t.Run("repeated_targets", func(t *testing.T) {
		vacuumScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			c := vacuumConn(t, p, b)
			vacuumRun(t, p, b, c, fixture.Exec("CREATE TABLE vacuum_repeated(id INT)", 0))
			q := "VACUUM vacuum_repeated,vacuum_repeated"
			vacuumRun(t, p, b, c, fixture.Exec(q, 0))
			vacuumCount(t, p.Ctx, c, "vacuum_repeated", 2)
			vacuumRun(t, p, b, vacuumHeld(t, p, c, q), fixture.Exec(q, 0))
			vacuumCount(t, p.Ctx, c, "vacuum_repeated", 4)
		})
	})
}
