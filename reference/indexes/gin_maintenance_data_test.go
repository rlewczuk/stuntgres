package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func ginMaintDataSeed(run func(fixture.Step)) {
	run(fixture.Exec("CREATE TABLE gin_maint(id integer,a integer[],v tsvector)", 0))
	run(fixture.Exec("CREATE INDEX gin_maint_a ON gin_maint USING gin(a)", 0))
	run(fixture.Exec("CREATE INDEX gin_maint_v ON gin_maint USING gin(v)", 0))
	run(fixture.Exec("INSERT INTO gin_maint VALUES(1,'{1,2,2}','cat:1 rat:2'),(2,'{}','dog:1'),(3,NULL,NULL),(4,'{NULL,2}','cat:1 rat:3')", 4))
}
func ginMaintDataMatches(run func(fixture.Step), arrays, phrase []int64) {
	run(ginIDs("SELECT id FROM gin_maint WHERE a @> '{2}'::integer[] ORDER BY id", arrays))
	run(ginIDs("SELECT id FROM gin_maint WHERE a && '{2}'::integer[] ORDER BY id", arrays))
	run(ginIDs("SELECT id FROM gin_maint WHERE v @@ 'cat <-> rat'::tsquery ORDER BY id", phrase))
}
func ginMaintCleanIndex(name string, positive bool) fixture.Step {
	q := "SELECT gin_clean_pending_list('" + name + "'::regclass)"
	if positive {
		return ginMaintPositive(q + " > 0 AS cleaned")
	}
	return ginMaintResult(q, int64(0))
}
func TestReferenceGINMaintenanceData(t *testing.T) {
	for _, route := range []string{"direct", "fresh", "held"} {
		t.Run(route, func(t *testing.T) {
			classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				conn, run := classConn(t, p, b)
				ginMaintDataSeed(run)
				ginMaintDataMatches(run, []int64{1, 4}, []int64{1})
				clean := func(step fixture.Step) {
					switch route {
					case "fresh":
						run(fixture.Prepared(step))
					case "held":
						fixture.RunStep(t, p.Ctx, classHeld(t, p, conn, step.Query), b.Recorder, step)
					default:
						run(step)
					}
				}
				for _, name := range []string{"gin_maint_a", "gin_maint_v"} {
					clean(ginMaintCleanIndex(name, true))
					clean(ginMaintCleanIndex(name, false))
					ginMaintDataMatches(run, []int64{1, 4}, []int64{1})
				}
				run(fixture.Exec("UPDATE gin_maint SET a='{9}',v='dog:1' WHERE id=1", 1))
				run(fixture.Exec("DELETE FROM gin_maint WHERE id=4", 1))
				ginMaintDataMatches(run, nil, nil)
				for _, name := range []string{"gin_maint_a", "gin_maint_v"} {
					clean(ginMaintCleanIndex(name, true))
					clean(ginMaintCleanIndex(name, false))
				}
				ginMaintDataMatches(run, nil, nil)
				run(fixture.Exec("INSERT INTO gin_maint VALUES(5,'{2,2}','cat:1 rat:2'),(6,'{2,2}','cat:1 rat:2')", 2))
				ginMaintDataMatches(run, []int64{5, 6}, []int64{5, 6})
				run(fixture.Exec("CREATE INDEX gin_maint_multi ON gin_maint USING gin(a,v) WHERE id>0", 0))
				run(fixture.Exec("CREATE INDEX gin_maint_expr ON gin_maint USING gin((a||ARRAY[7])) WHERE id>0", 0))
				clean(ginMaintCleanIndex("gin_maint_multi", false))
				clean(ginMaintCleanIndex("gin_maint_expr", false))
				run(fixture.Exec("INSERT INTO gin_maint VALUES(7,'{}',''),(8,NULL,NULL),(9,'{NULL,2}','catalog:1')", 3))
				for _, name := range []string{"gin_maint_multi", "gin_maint_expr"} {
					clean(ginMaintCleanIndex(name, true))
					clean(ginMaintCleanIndex(name, false))
				}
				run(ginIDs("SELECT id FROM gin_maint WHERE a @> '{}'::integer[] ORDER BY id", []int64{1, 2, 5, 6, 7, 9}))
				run(ginIDs("SELECT id FROM gin_maint WHERE v @@ 'cat:*'::tsquery ORDER BY id", []int64{5, 6, 9}))
				run(ginIDs("SELECT id FROM gin_maint WHERE (a||ARRAY[7]) @> '{7}'::integer[] ORDER BY id", []int64{1, 2, 3, 5, 6, 7, 8, 9}))
			})
		})
	}
}
func TestReferenceGINMaintenanceFastupdate(t *testing.T) {
	classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		_, run := classConn(t, p, b)
		ginMaintSeed(run, true)
		run(ginMaintCleanIndex("gin_maint_oracle_i", false))
		run(fixture.Exec("ALTER INDEX gin_maint_oracle_i SET(fastupdate=on)", 0))
		run(fixture.Exec("INSERT INTO gin_maint_oracle VALUES(2,'{2,3}')", 1))
		run(fixture.Exec("ALTER INDEX gin_maint_oracle_i SET(fastupdate=off)", 0))
		run(ginMaintCleanIndex("gin_maint_oracle_i", true))
		run(ginMaintCleanIndex("gin_maint_oracle_i", false))
		run(fixture.Exec("INSERT INTO gin_maint_oracle VALUES(3,'{2,4}')", 1))
		run(ginMaintCleanIndex("gin_maint_oracle_i", false))
		run(fixture.Exec("ALTER INDEX gin_maint_oracle_i RESET(fastupdate)", 0))
		run(fixture.Exec("INSERT INTO gin_maint_oracle VALUES(4,'{2,5}')", 1))
		run(ginMaintCleanIndex("gin_maint_oracle_i", true))
		run(ginMaintCleanIndex("gin_maint_oracle_i", false))
		run(ginIDs(ginMaintIDsSQL, []int64{1, 2, 3, 4}))
	})
}
func TestReferenceGINMaintenanceFeatureSmoke(t *testing.T) {
	classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		_, run := classConn(t, p, b)
		ginMaintDataSeed(run)
		ginMaintDataMatches(run, []int64{1, 4}, []int64{1})
		for _, name := range []string{"gin_maint_a", "gin_maint_v"} {
			run(ginMaintCleanIndex(name, true))
			run(ginMaintCleanIndex(name, false))
		}
		ginMaintDataMatches(run, []int64{1, 4}, []int64{1})
		run(fixture.Exec("INSERT INTO gin_maint VALUES(5,'{2}','cat:1 rat:2')", 1))
		run(fixture.Exec("ALTER INDEX gin_maint_a SET(fastupdate=off)", 0))
		run(ginMaintCleanIndex("gin_maint_a", true))
		run(ginMaintCleanIndex("gin_maint_a", false))
		run(fixture.Exec("INSERT INTO gin_maint VALUES(6,'{2}','dog:1')", 1))
		run(ginMaintCleanIndex("gin_maint_a", false))
		ginMaintDataMatches(run, []int64{1, 4, 5, 6}, []int64{1, 5})
		ginMaintSmokeReplay(t, p, b, run)
	})
}
