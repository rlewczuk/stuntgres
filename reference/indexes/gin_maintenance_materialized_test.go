package reference_test

import (
	"bytes"
	"database/sql"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceGINMaintenanceMaterialized(t *testing.T) {
	classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		_, raw := classConn(t, p, b)
		phase := "bulk/create"
		run := func(step fixture.Step) {
			t.Helper()
			defer func() {
				if t.Failed() {
					t.Logf("materialized phase=%s SQL=%s", phase, step.Query)
				}
			}()
			raw(step)
		}
		// Populated CREATE MATERIALIZED VIEW reports its SELECT cardinality;
		// empty/unpopulated creation and the index DDL report zero.
		for _, step := range []fixture.Step{
			fixture.Exec("CREATE TABLE gin_maint_mv_base(id integer,a integer[])", 0),
			fixture.Exec("INSERT INTO gin_maint_mv_base VALUES(1,'{2}'),(2,'{2}')", 2),
			fixture.Exec("CREATE MATERIALIZED VIEW gin_maint_mv AS SELECT id,a FROM gin_maint_mv_base", 2),
			fixture.Exec("CREATE UNIQUE INDEX gin_maint_mv_u ON gin_maint_mv(id)", 0),
			fixture.Exec("CREATE INDEX gin_maint_mv_i ON gin_maint_mv USING gin(a)", 0),
			fixture.Exec("CREATE INDEX gin_maint_mv_e ON gin_maint_mv USING gin((a||ARRAY[9]))", 0),
			fixture.Exec("CREATE MATERIALIZED VIEW gin_maint_mv_dup AS SELECT a FROM gin_maint_mv_base", 2),
			fixture.Exec("CREATE INDEX gin_maint_mv_dup_i ON gin_maint_mv_dup USING gin(a)", 0),
			fixture.Exec("CREATE MATERIALIZED VIEW gin_maint_mv_empty AS SELECT id,a FROM gin_maint_mv_base WHERE id<0", 0),
			fixture.Exec("CREATE INDEX gin_maint_mv_empty_i ON gin_maint_mv_empty USING gin(a)", 0),
			fixture.Exec("CREATE MATERIALIZED VIEW gin_maint_mv_unpop AS SELECT id,a FROM gin_maint_mv_base WITH NO DATA", 0),
			fixture.Exec("CREATE INDEX gin_maint_mv_unpop_i ON gin_maint_mv_unpop USING gin(a)", 0),
		} {
			run(step)
		}
		check := func(run func(fixture.Step)) {
			run(ginIDs("SELECT id FROM public.gin_maint_mv WHERE a @> '{2}'::integer[] ORDER BY id", []int64{1, 2}))
			run(ginIDs("SELECT 1::integer AS id FROM public.gin_maint_mv_dup ORDER BY id", []int64{1, 1}))
			run(ginIDs("SELECT id FROM public.gin_maint_mv_empty ORDER BY id", nil))
			for _, name := range []string{"gin_maint_mv_i", "gin_maint_mv_e", "gin_maint_mv_dup_i", "gin_maint_mv_empty_i", "gin_maint_mv_unpop_i"} {
				run(ginMaintCleanIndex("public."+name, false))
			}
		}
		phase = "bulk/clean"
		check(run)
		phase = "source/concurrent-seed"
		run(fixture.Exec("INSERT INTO gin_maint_mv_base VALUES(3,'{2}')", 1))
		run(fixture.Exec("REFRESH MATERIALIZED VIEW CONCURRENTLY gin_maint_mv", 0))
		run(fixture.Exec("REFRESH MATERIALIZED VIEW CONCURRENTLY gin_maint_mv", 0))
		run(fixture.Exec("ALTER INDEX gin_maint_mv_i SET(fastupdate=off)", 0))
		run(fixture.Exec("INSERT INTO gin_maint_mv_base VALUES(4,'{2}')", 1))
		run(fixture.Exec("REFRESH MATERIALIZED VIEW CONCURRENTLY gin_maint_mv", 0))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("REFRESH MATERIALIZED VIEW CONCURRENTLY gin_maint_mv", 0))
		run(fixture.Exec("COMMIT", 0))
		checkPending := func(run func(fixture.Step)) {
			run(ginIDs("SELECT id FROM public.gin_maint_mv WHERE a @> '{2}'::integer[] ORDER BY id", []int64{1, 2, 3, 4}))
			run(ginIDs("SELECT 1::integer AS id FROM public.gin_maint_mv_dup ORDER BY id", []int64{1, 1}))
			run(ginIDs("SELECT id FROM public.gin_maint_mv_empty ORDER BY id", nil))
			for _, name := range []string{"gin_maint_mv_i", "gin_maint_mv_e"} {
				run(ginMaintCleanIndex("public."+name, true))
				run(ginMaintCleanIndex("public."+name, false))
			}
			for _, name := range []string{"gin_maint_mv_dup_i", "gin_maint_mv_empty_i", "gin_maint_mv_unpop_i"} {
				run(ginMaintCleanIndex("public."+name, false))
			}
		}
		afterClean := func(run func(fixture.Step)) {
			origin := phase
			phase = origin + "/unchanged-refresh"
			run(fixture.Exec("REFRESH MATERIALIZED VIEW CONCURRENTLY gin_maint_mv", 0))
			phase = origin + "/unchanged-clean"
			for _, name := range []string{"gin_maint_mv_i", "gin_maint_mv_e"} {
				run(ginMaintCleanIndex("public."+name, false))
			}
			run(fixture.Exec("DELETE FROM gin_maint_mv_base WHERE id=2", 1))
			phase = origin + "/delete-refresh"
			run(fixture.Exec("REFRESH MATERIALIZED VIEW CONCURRENTLY gin_maint_mv", 0))
			run(ginIDs("SELECT id FROM public.gin_maint_mv WHERE a @> '{2}'::integer[] ORDER BY id", []int64{1, 3, 4}))
			phase = origin + "/delete-clean"
			for _, name := range []string{"gin_maint_mv_i", "gin_maint_mv_e"} {
				run(ginMaintCleanIndex("public."+name, false))
			}
			phase = origin + "/private-rollback"
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("INSERT INTO gin_maint_mv_base VALUES(5,'{2}')", 1))
			run(fixture.Exec("REFRESH MATERIALIZED VIEW CONCURRENTLY gin_maint_mv", 0))
			run(fixture.Exec("ROLLBACK", 0))
			run(ginIDs("SELECT id FROM public.gin_maint_mv WHERE a @> '{2}'::integer[] ORDER BY id", []int64{1, 3, 4}))
		}
		if b.PG {
			checkPending(run)
			afterClean(run)
			return
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		phase = "source/split-clean"
		checkPending(run)
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		phase = "same-restore/split-clean"
		checkPending(run)
		afterClean(run)
		connector, err := stuntgres.NewDriver().OpenConnector(t.Name() + "/materialized-copy")
		if err != nil {
			t.Fatal(err)
		}
		target := sql.OpenDB(connector)
		defer target.Close()
		recorded, recorder := fixture.RecordedNativePool(t, connector)
		other := func(step fixture.Step) {
			t.Helper()
			defer func() {
				if t.Failed() {
					t.Logf("materialized phase=%s SQL=%s", phase, step.Query)
				}
			}()
			fixture.RunStep(t, p.Ctx, recorded, recorder, step)
		}
		if err := stuntgres.Restore(p.Ctx, target, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		phase = "independent-restore/split-clean"
		checkPending(other)
		afterClean(other)
	})
}
