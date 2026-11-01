package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceVacuumFullKinds(t *testing.T) {
	fullVacuumCases(t, func(f *fullVacuumCase) {
		f.run(fixture.Exec("CREATE TEMP TABLE full_temp(id INT PRIMARY KEY)", 0))
		f.run(fixture.Exec("INSERT INTO full_temp VALUES(1),(2)", 2))
		f.run(fixture.Exec("DELETE FROM full_temp WHERE id=1", 1))
		before := f.locator("full_temp")
		f.command(fixture.Exec("VACUUM FULL full_temp", 0))
		f.rewritten("full_temp", before)
		vacuumRows(f.t, f.p, f.b, f.c, "full_temp", 2)
		f.run(fixture.Exec("CREATE TABLE full_mv_source(id INT)", 0))
		f.run(fixture.Exec("INSERT INTO full_mv_source VALUES(2)", 1))
		f.run(fixture.Exec("CREATE MATERIALIZED VIEW full_mv AS SELECT id FROM full_mv_source", 1))
		f.run(fixture.Exec("CREATE UNIQUE INDEX full_mv_ix ON full_mv(id)", 0))
		before = f.locator("full_mv")
		ix := f.locator("full_mv_ix")
		f.command(fixture.Exec("VACUUM FULL full_mv", 0))
		f.rewritten("full_mv", before)
		f.rewritten("full_mv_ix", ix)
		vacuumRows(f.t, f.p, f.b, f.c, "full_mv", 2)
		f.run(fixture.Exec("CREATE MATERIALIZED VIEW full_mv_empty AS SELECT id FROM full_mv_source WITH NO DATA", 0))
		before = f.locator("full_mv_empty")
		f.command(fixture.Exec("VACUUM (FULL,ANALYZE) full_mv_empty", 0))
		f.unchanged("full_mv_empty", before)
		f.run(fixture.Select("SELECT relispopulated FROM pg_class WHERE oid='full_mv_empty'::regclass", []string{"relispopulated"}, []string{"BOOL"}, [][]any{{false}}))
		f.run(fixture.Exec("CREATE TABLE full_parent(id INT) PARTITION BY RANGE(id)", 0))
		f.run(fixture.Exec("CREATE TABLE full_child PARTITION OF full_parent FOR VALUES FROM(0) TO(10)", 0))
		f.run(fixture.Exec("INSERT INTO full_parent VALUES(1)", 1))
		parent := f.locator("full_parent")
		child := f.locator("full_child")
		if parent.locator != 0 {
			f.t.Fatal("partition root acquired storage", parent)
		}
		f.command(fixture.Exec("VACUUM FULL full_parent", 0))
		f.unchanged("full_parent", parent)
		f.rewritten("full_child", child)
		child = f.locator("full_child")
		var onlyNotices []fixture.Diagnostic
		_, onlyWorker, onlyRecorder := fixture.NoticeReferencePool(f.t, f.p, f.b.PG, &onlyNotices)
		only := fixture.Exec("VACUUM FULL ONLY full_parent", 0)
		if f.prepared {
			fixture.RunStep(f.t, f.p.Ctx, vacuumHeld(f.t, f.p, onlyWorker, only.Query), onlyRecorder, only)
		} else {
			fixture.RunStep(f.t, f.p.Ctx, onlyWorker, onlyRecorder, only)
		}
		vacuumWarning(f.t, onlyNotices, "01000", `VACUUM ONLY of partitioned table "full_parent" has no effect`)
		f.unchanged("full_parent", parent)
		f.unchanged("full_child", child)
		f.run(fixture.Exec("CREATE TABLE full_inherit(id INT)", 0))
		f.run(fixture.Exec("CREATE TABLE full_inherit_child() INHERITS(full_inherit)", 0))
		root := f.locator("full_inherit")
		descendant := f.locator("full_inherit_child")
		f.command(fixture.Exec("VACUUM FULL ONLY full_inherit", 0))
		f.rewritten("full_inherit", root)
		f.unchanged("full_inherit_child", descendant)
		f.emptyProgress()
	})
}

func TestReferenceVacuumFullMetadata(t *testing.T) {
	fullVacuumCases(t, func(f *fullVacuumCase) {
		f.run(fixture.Exec("CREATE TABLE full_meta(id INT)", 0))
		f.run(fixture.Exec("INSERT INTO full_meta VALUES(1),(2),(3)", 3))
		f.run(fixture.Exec("DELETE FROM full_meta WHERE id=2", 1))
		if f.b.PG {
			if _, err := f.c.ExecContext(f.p.Ctx, "SELECT pg_stat_force_next_flush()"); err != nil {
				f.t.Fatal(err)
			}
		}
		vacuumStatistic(f.t, f.p.Ctx, f.c, "full_meta", "n_tup_ins", 3)
		vacuumStatistic(f.t, f.p.Ctx, f.c, "full_meta", "n_mod_since_analyze", 4)
		estimates := fixture.Select("SELECT n_live_tup,n_dead_tup FROM pg_stat_all_tables WHERE relid='full_meta'::regclass", []string{"n_live_tup", "n_dead_tup"}, []string{"INT8", "INT8"}, [][]any{{int64(2), int64(1)}})
		f.run(estimates)
		f.run(fixture.Select("SELECT vacuum_count,last_vacuum,n_ins_since_vacuum,n_tup_ins,n_tup_del,n_mod_since_analyze,analyze_count,last_analyze,total_vacuum_time FROM pg_stat_all_tables WHERE relid='full_meta'::regclass", []string{"vacuum_count", "last_vacuum", "n_ins_since_vacuum", "n_tup_ins", "n_tup_del", "n_mod_since_analyze", "analyze_count", "last_analyze", "total_vacuum_time"}, []string{"INT8", "TIMESTAMPTZ", "INT8", "INT8", "INT8", "INT8", "INT8", "TIMESTAMPTZ", "FLOAT8"}, [][]any{{int64(0), nil, int64(3), int64(3), int64(1), int64(4), int64(0), nil, float64(0)}}))
		f.command(fixture.Exec("VACUUM FULL full_meta", 0))
		f.run(fixture.Select("SELECT reltuples FROM pg_class WHERE oid='full_meta'::regclass", []string{"reltuples"}, []string{"FLOAT4"}, [][]any{{float64(2)}}))
		f.run(fixture.Select("SELECT vacuum_count,last_vacuum,n_ins_since_vacuum,n_tup_ins,n_tup_del,n_mod_since_analyze,analyze_count,last_analyze,total_vacuum_time FROM pg_stat_all_tables WHERE relid='full_meta'::regclass", []string{"vacuum_count", "last_vacuum", "n_ins_since_vacuum", "n_tup_ins", "n_tup_del", "n_mod_since_analyze", "analyze_count", "last_analyze", "total_vacuum_time"}, []string{"INT8", "TIMESTAMPTZ", "INT8", "INT8", "INT8", "INT8", "INT8", "TIMESTAMPTZ", "FLOAT8"}, [][]any{{int64(0), nil, int64(3), int64(3), int64(1), int64(4), int64(0), nil, float64(0)}}))
		f.run(estimates)
		vacuumFreezeBound(f.t, f.p, f.b, f.c, "full_meta")
		vacuumRows(f.t, f.p, f.b, f.c, "full_meta", 1, 3)
		f.run(fixture.Exec("CREATE TABLE full_recent(id INT)", 0))
		f.run(fixture.Exec("INSERT INTO full_recent VALUES(1),(2)", 2))
		f.run(fixture.Exec("CREATE TABLE full_recent_anchor(id INT)", 0))
		f.run(fixture.Exec("INSERT INTO full_recent_anchor VALUES(9)", 1))
		reader := vacuumConn(f.t, f.p, f.b)
		vacuumRun(f.t, f.p, f.b, reader, fixture.Exec("BEGIN ISOLATION LEVEL REPEATABLE READ", 0))
		defer reader.ExecContext(f.p.Ctx, "ROLLBACK")
		vacuumRows(f.t, f.p, f.b, reader, "full_recent_anchor", 9)
		f.run(fixture.Exec("DELETE FROM full_recent WHERE id=1", 1))
		if f.b.PG {
			if _, err := f.c.ExecContext(f.p.Ctx, "SELECT pg_stat_force_next_flush()"); err != nil {
				f.t.Fatal(err)
			}
		}
		vacuumStatistic(f.t, f.p.Ctx, f.c, "full_recent", "n_tup_del", 1)
		vacuumStatistic(f.t, f.p.Ctx, f.c, "full_recent", "n_live_tup", 1)
		vacuumStatistic(f.t, f.p.Ctx, f.c, "full_recent", "n_dead_tup", 1)
		f.command(fixture.Exec("VACUUM FULL full_recent", 0))
		// Independent expectations: pg_class estimates the copied live plus
		// recently-dead heap, while plain FULL leaves flushed cumulative
		// live/dead estimates untouched even after quiescent physical retirement.
		f.run(fixture.Select("SELECT reltuples FROM pg_class WHERE oid='full_recent'::regclass", []string{"reltuples"}, []string{"FLOAT4"}, [][]any{{float64(2)}}))
		f.run(fixture.Select("SELECT n_live_tup,n_dead_tup FROM pg_stat_all_tables WHERE relid='full_recent'::regclass", []string{"n_live_tup", "n_dead_tup"}, []string{"INT8", "INT8"}, [][]any{{int64(1), int64(1)}}))
		vacuumRows(f.t, f.p, f.b, reader, "full_recent", 1, 2)
		vacuumRun(f.t, f.p, f.b, reader, fixture.Exec("ROLLBACK", 0))
		f.command(fixture.Exec("VACUUM FULL full_recent", 0))
		f.run(fixture.Select("SELECT reltuples FROM pg_class WHERE oid='full_recent'::regclass", []string{"reltuples"}, []string{"FLOAT4"}, [][]any{{float64(1)}}))
		f.run(fixture.Select("SELECT n_live_tup,n_dead_tup FROM pg_stat_all_tables WHERE relid='full_recent'::regclass", []string{"n_live_tup", "n_dead_tup"}, []string{"INT8", "INT8"}, [][]any{{int64(1), int64(1)}}))
		f.emptyProgress()
	})
}

func TestReferenceVacuumFullAnalyze(t *testing.T) {
	fullVacuumCases(t, func(f *fullVacuumCase) {
		f.run(fixture.Exec("CREATE TABLE full_analyze(id INT, value TEXT)", 0))
		f.run(fixture.Exec("INSERT INTO full_analyze VALUES(1,'one'),(2,NULL)", 2))
		if f.b.PG {
			if _, err := f.c.ExecContext(f.p.Ctx, "SELECT pg_stat_force_next_flush()"); err != nil {
				f.t.Fatal(err)
			}
		}
		vacuumStatistic(f.t, f.p.Ctx, f.c, "full_analyze", "n_tup_ins", 2)
		before := f.locator("full_analyze")
		f.command(fixture.Exec("VACUUM (FULL,ANALYZE,BUFFER_USAGE_LIMIT '128kB') full_analyze(value)", 0))
		before = f.rewritten("full_analyze", before)
		vacuumStatistic(f.t, f.p.Ctx, f.c, "full_analyze", "analyze_count", 1)
		f.run(fixture.Select("SELECT attname FROM pg_stats WHERE tablename='full_analyze' ORDER BY attname", []string{"attname"}, []string{"NAME"}, [][]any{{"value"}}))
		f.command(fixture.Exec("VACUUM (FULL,ANALYZE,PROCESS_MAIN false) full_analyze(id)", 0))
		f.unchanged("full_analyze", before)
		vacuumStatistic(f.t, f.p.Ctx, f.c, "full_analyze", "analyze_count", 2)
		f.command(fixture.Exec("VACUUM (FULL,ANALYZE) full_analyze(id),full_analyze(value)", 0))
		before = f.rewritten("full_analyze", before)
		vacuumStatistic(f.t, f.p.Ctx, f.c, "full_analyze", "analyze_count", 4)
		f.command(fixture.ErrorDiagnostic("VACUUM (FULL,ANALYZE) full_analyze(missing)", fixture.ControlsReferenceDiagnostic("42703", `column "missing" of relation "full_analyze" does not exist`)))
		before = f.rewritten("full_analyze", before)
		vacuumStatistic(f.t, f.p.Ctx, f.c, "full_analyze", "analyze_count", 4)
		f.run(fixture.Exec("CREATE TABLE full_analysis_later(id INT)", 0))
		later := f.locator("full_analysis_later")
		f.command(fixture.ErrorDiagnostic("VACUUM (FULL,ANALYZE) full_analyze(id),full_analysis_later(missing)", fixture.ControlsReferenceDiagnostic("42703", `column "missing" of relation "full_analysis_later" does not exist`)))
		f.rewritten("full_analyze", before)
		f.rewritten("full_analysis_later", later)
		vacuumStatistic(f.t, f.p.Ctx, f.c, "full_analyze", "analyze_count", 5)
		vacuumCount(f.t, f.p.Ctx, f.c, "full_analyze", 0)
		// Explicit columns refresh distributions/estimates but do not reset the
		// relation-wide modification counter (REL_18_6 analyze.c resetcounter).
		f.run(fixture.Select("SELECT n_ins_since_vacuum,n_mod_since_analyze FROM pg_stat_all_tables WHERE relid='full_analyze'::regclass", []string{"n_ins_since_vacuum", "n_mod_since_analyze"}, []string{"INT8", "INT8"}, [][]any{{int64(2), int64(2)}}))
		f.command(fixture.Exec("VACUUM (FULL,ANALYZE) full_analyze", 0))
		vacuumStatistic(f.t, f.p.Ctx, f.c, "full_analyze", "analyze_count", 6)
		f.run(fixture.Select("SELECT n_ins_since_vacuum,n_mod_since_analyze FROM pg_stat_all_tables WHERE relid='full_analyze'::regclass", []string{"n_ins_since_vacuum", "n_mod_since_analyze"}, []string{"INT8", "INT8"}, [][]any{{int64(2), int64(0)}}))
		f.run(fixture.Exec("CREATE TABLE full_an_parent(id INT,value TEXT)", 0))
		f.run(fixture.Exec("CREATE TABLE full_an_child() INHERITS(full_an_parent)", 0))
		f.run(fixture.Exec("INSERT INTO full_an_parent VALUES(1,'one')", 1))
		f.run(fixture.Exec("INSERT INTO full_an_child VALUES(2,'two')", 1))
		f.command(fixture.Exec("VACUUM (FULL,ANALYZE) full_an_parent(value)", 0))
		for _, name := range []string{"full_an_parent", "full_an_child"} {
			vacuumStatistic(f.t, f.p.Ctx, f.c, name, "analyze_count", 1)
			vacuumCount(f.t, f.p.Ctx, f.c, name, 0)
		}
		f.run(fixture.Select("SELECT tablename,attname,inherited FROM pg_stats WHERE tablename IN ('full_an_parent','full_an_child') ORDER BY tablename,inherited", []string{"tablename", "attname", "inherited"}, []string{"NAME", "NAME", "BOOL"}, [][]any{{"full_an_child", "value", false}, {"full_an_parent", "value", false}, {"full_an_parent", "value", true}}))
		f.run(fixture.Exec("CREATE TABLE full_an_partition(id INT,value TEXT) PARTITION BY RANGE(id)", 0))
		f.run(fixture.Exec("CREATE TABLE full_an_leaf PARTITION OF full_an_partition FOR VALUES FROM(0) TO(10)", 0))
		f.run(fixture.Exec("INSERT INTO full_an_partition VALUES(1,'one')", 1))
		partition, leaf := f.locator("full_an_partition"), f.locator("full_an_leaf")
		f.command(fixture.Exec("VACUUM (FULL,ANALYZE) full_an_partition(value)", 0))
		f.unchanged("full_an_partition", partition)
		f.rewritten("full_an_leaf", leaf)
		for _, name := range []string{"full_an_partition", "full_an_leaf"} {
			vacuumStatistic(f.t, f.p.Ctx, f.c, name, "analyze_count", 1)
			vacuumCount(f.t, f.p.Ctx, f.c, name, 0)
		}
		f.run(fixture.Select("SELECT tablename,attname,inherited FROM pg_stats WHERE tablename IN ('full_an_partition','full_an_leaf') ORDER BY tablename,inherited", []string{"tablename", "attname", "inherited"}, []string{"NAME", "NAME", "BOOL"}, [][]any{{"full_an_leaf", "value", false}, {"full_an_partition", "value", true}}))
		f.emptyProgress()
	})
}
