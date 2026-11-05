package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceAnalyzeProgressShape(t *testing.T) {
	analyzeProgressScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		c := vacuumConn(t, p, b)
		for _, name := range []string{"pg_stat_progress_analyze", "pg_catalog.pg_stat_progress_analyze"} {
			s := fixture.Select("SELECT * FROM "+name, analyzeProgressNames, analyzeProgressTypes, nil)
			vacuumRun(t, p, b, c, s)
			vacuumRun(t, p, b, c, fixture.Prepared(s))
		}
		var attrs [][]any
		oids := []int64{23, 26, 19, 26, 25, 20, 20, 20, 20, 20, 20, 26, 701}
		for i, name := range analyzeProgressNames {
			collation := int64(0)
			if i == 2 {
				collation = 950
			}
			if i == 4 {
				collation = 100
			}
			attrs = append(attrs, []any{name, int64(i + 1), oids[i], int64(-1), collation, false, false})
		}
		owner := fixture.PGTableViewsOwner(b)
		steps := []fixture.Step{
			fixture.Select("SELECT attname,attnum,atttypid::bigint,atttypmod,attcollation::bigint,attnotnull,attisdropped FROM pg_attribute WHERE attrelid='pg_catalog.pg_stat_progress_analyze'::regclass AND attnum>0 ORDER BY attnum", []string{"attname", "attnum", "atttypid", "atttypmod", "attcollation", "attnotnull", "attisdropped"}, []string{"NAME", "INT2", "INT8", "INT4", "INT8", "BOOL", "BOOL"}, attrs),
			fixture.Select("SELECT relkind,pg_get_userbyid(relowner) AS owner,relhasindex,relhasrules,relacl::text FROM pg_class WHERE oid='pg_catalog.pg_stat_progress_analyze'::regclass", []string{"relkind", "owner", "relhasindex", "relhasrules", "relacl"}, []string{"CHAR", "NAME", "BOOL", "BOOL", "TEXT"}, [][]any{{"v", owner, false, true, "{" + owner + "=arwdDxtm/" + owner + ",=r/" + owner + "}"}}),
			fixture.Select("SELECT t.typname,t.typtype,t.typrelid=c.oid AS linked,t.typowner=c.relowner AS owned,a.typelem=t.oid AS element_link,a.typowner=c.relowner AS array_owned,t.typacl IS NULL AS row_acl,a.typacl IS NULL AS array_acl,a.typname FROM pg_class c JOIN pg_type t ON t.oid=c.reltype JOIN pg_type a ON a.oid=t.typarray WHERE c.oid='pg_catalog.pg_stat_progress_analyze'::regclass", []string{"typname", "typtype", "linked", "owned", "element_link", "array_owned", "row_acl", "array_acl", "typname"}, []string{"NAME", "CHAR", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "NAME"}, [][]any{{"pg_stat_progress_analyze", "c", true, true, true, true, true, true, "_pg_stat_progress_analyze"}}),
			fixture.Select("SELECT rulename,ev_type,ev_enabled,is_instead FROM pg_rewrite WHERE ev_class='pg_catalog.pg_stat_progress_analyze'::regclass", []string{"rulename", "ev_type", "ev_enabled", "is_instead"}, []string{"NAME", "CHAR", "CHAR", "BOOL"}, [][]any{{"_RETURN", "1", "O", true}}),
		}
		for _, s := range steps {
			vacuumRun(t, p, b, c, s)
			vacuumRun(t, p, b, c, fixture.Prepared(s))
		}
	})
}

func TestReferenceAnalyzeProgressSmoke(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		name := "direct"
		if prepared {
			name = "prepared"
		}
		t.Run(name, func(t *testing.T) {
			analyzeProgressScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				admin, worker := vacuumConn(t, p, b), fixture.ConcurrentIndexConn(t, p, b)
				analyzeProgressSetup(t, p, admin, "analyze_smoke")
				copyProgressExec(t, p.Ctx, admin, "ANALYZE ONLY analyze_smoke")
				if b.PG {
					copyProgressExec(t, p.Ctx, admin, "SELECT pg_stat_force_next_flush()")
					copyProgressExec(t, p.Ctx, admin, "SELECT 1")
				}
				copyProgressExec(t, p.Ctx, admin, "INSERT INTO analyze_smoke_child VALUES(202,3)")
				observer := vacuumHeld(t, p, admin, analyzeProgressQuery)
				// Local maintenance completes before the descendant wait; inherited
				// typed statistics remain private until required final success.
				id, holder, run := analyzeProgressHold(t, p, b, worker, "analyze_smoke", prepared)
				first := analyzeProgressObserve(t, p, observer, id, run)
				analyzeProgressVisible(t, first, id, b.PG)
				for range 3 {
					next := analyzeProgressObserve(t, p, admin, id, run)
					analyzeProgressVisible(t, next, id, b.PG)
					if next != first {
						t.Fatalf("held segment changed without work: first=%+v next=%+v", first, next)
					}
				}
				copyProgressExec(t, p.Ctx, holder, "ROLLBACK")
				if err := run.result(t); err != nil {
					t.Fatal(err)
				}
				analyzeProgressIdle(t, p, observer, id.pid)
				analyzeProgressPopulation(t, p, b, admin, 129)
				analyzeProgressInheritedDistinct(t, p, b, admin, 4)
				// A separate actual admitted command is canceled. Its completed local
				// reltuples/count/time survive; the unfinished inherited product does not.
				copyProgressExec(t, p.Ctx, admin, "INSERT INTO analyze_smoke_child VALUES(203,4)")
				id, holder, run = analyzeProgressHold(t, p, b, worker, "analyze_smoke", !prepared)
				analyzeProgressVisible(t, analyzeProgressObserve(t, p, observer, id, run), id, b.PG)
				analyzeProgressCancel(t, p, b, admin, id, run)
				copyProgressExec(t, p.Ctx, holder, "ROLLBACK")
				analyzeProgressPopulation(t, p, b, admin, 129)
				analyzeProgressInheritedDistinct(t, p, b, admin, 4)
				if b.PG {
					copyProgressExec(t, p.Ctx, worker, "SELECT pg_stat_force_next_flush()")
					copyProgressExec(t, p.Ctx, worker, "SELECT 1")
				}
				vacuumStatistic(t, p.Ctx, admin, "analyze_smoke", "analyze_count", 3)
				vacuumRun(t, p, b, admin, fixture.Select("SELECT last_analyze IS NOT NULL AS analyzed FROM pg_stat_all_tables WHERE relid='analyze_smoke'::regclass", []string{"analyzed"}, []string{"BOOL"}, [][]any{{true}}))
				vacuumRun(t, p, b, admin, fixture.Select("SELECT count(*) FROM analyze_smoke", []string{"count"}, []string{"INT8"}, [][]any{{int64(133)}}))
				// Detached values are not rewritten by later progress or removal.
				analyzeProgressVisible(t, first, id, b.PG)
			})
		})
	}
}

func analyzeProgressPopulation(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, want float64) {
	t.Helper()
	vacuumRun(t, p, b, c, fixture.Select("SELECT reltuples FROM pg_class WHERE oid='analyze_smoke'::regclass", []string{"reltuples"}, []string{"FLOAT4"}, [][]any{{want}}))
}

func analyzeProgressInheritedDistinct(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, want float64) {
	t.Helper()
	vacuumRun(t, p, b, c, fixture.Select("SELECT n_distinct FROM pg_stats WHERE schemaname='public' AND tablename='analyze_smoke' AND attname='b' AND inherited", []string{"n_distinct"}, []string{"FLOAT4"}, [][]any{{want}}))
}
