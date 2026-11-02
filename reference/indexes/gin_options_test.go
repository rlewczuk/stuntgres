package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceGINOptions(t *testing.T) {
	classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		conn, run := classConn(t, p, b)
		run(fixture.Exec("CREATE TABLE gin_options(id integer,a integer[])", 0))
		run(fixture.Exec("INSERT INTO gin_options VALUES(1,'{1,2}'),(2,'{}'),(3,NULL)", 3))
		run(fixture.Exec("CREATE INDEX gin_options_i ON gin_options USING gin(a) WITH(fastupdate=off,gin_pending_list_limit=64)", 0))
		run(fixture.Exec("CREATE INDEX gin_options_absent ON gin_options USING gin(a)", 0))
		run(fixture.Exec("CREATE INDEX gin_options_defaults ON gin_options USING gin(a) WITH(fastupdate=on,gin_pending_list_limit=4096)", 0))
		run(fixture.Prepared(fixture.Exec("CREATE INDEX gin_options_max ON gin_options USING gin(a) WITH(gin_pending_list_limit=2147483647)", 0)))
		run(fixture.Exec("CREATE INDEX gin_options_lexical ON gin_options USING gin(a) WITH(fastupdate='OFF',gin_pending_list_limit='0100')", 0))
		classRoutes(t, p, b, conn, fixture.Select("SELECT relname,reloptions::text AS options FROM pg_class WHERE relname IN ('gin_options_absent','gin_options_defaults','gin_options_i','gin_options_max','gin_options_lexical') ORDER BY relname", []string{"relname", "options"}, []string{"NAME", "TEXT"}, [][]any{
			{"gin_options_absent", nil}, {"gin_options_defaults", "{fastupdate=on,gin_pending_list_limit=4096}"}, {"gin_options_i", "{fastupdate=off,gin_pending_list_limit=64}"}, {"gin_options_lexical", "{fastupdate=OFF,gin_pending_list_limit=0100}"}, {"gin_options_max", "{gin_pending_list_limit=2147483647}"},
		}))
		const query = "SELECT id FROM gin_options WHERE a @> $1::integer[] ORDER BY id"
		held := classHeld(t, p, conn, query)
		check := func(options any, ids []int64) {
			classRoutes(t, p, b, conn, fixture.Select("SELECT reloptions::text AS options FROM pg_class WHERE oid='gin_options_i'::regclass", []string{"options"}, []string{"TEXT"}, [][]any{{options}}))
			classRoutes(t, p, b, conn, fixture.Select("SELECT pg_get_indexdef('gin_options_i'::regclass,1,true) AS key", []string{"key"}, []string{"TEXT"}, [][]any{{"a"}}))
			s := ginIDs(query, ids, "{2}")
			classRoutes(t, p, b, conn, s)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		check("{fastupdate=off,gin_pending_list_limit=64}", []int64{1})
		for _, tc := range ginInvalidOptions {
			d := fixture.ControlsReferenceDiagnostic("22023", tc.message)
			d.Detail = tc.detail
			ginUtilityError(t, p, b, conn, "CREATE INDEX gin_options_bad ON gin_options USING gin(a) WITH("+tc.options+")", d)
			ginUtilityError(t, p, b, conn, "ALTER INDEX gin_options_i SET("+tc.options+")", d)
			run(fixture.Select("SELECT relname FROM pg_class WHERE relname='gin_options_bad' AND relnamespace='public'::regnamespace", []string{"relname"}, []string{"NAME"}, nil))
			check("{fastupdate=off,gin_pending_list_limit=64}", []int64{1})
			run(fixture.Exec("CREATE INDEX gin_options_bad ON gin_options USING gin(a)", 0))
			run(fixture.Exec("DROP INDEX gin_options_bad", 0))
		}
		run(fixture.Exec("BEGIN", 0))
		defer conn.ExecContext(p.Ctx, "ROLLBACK")
		run(fixture.Exec("SAVEPOINT gin_option_prefix", 0))
		run(fixture.Prepared(fixture.Exec("ALTER INDEX gin_options_i SET(fastupdate=on)", 0)))
		check("{gin_pending_list_limit=64,fastupdate=on}", []int64{1})
		run(fixture.Exec("ROLLBACK TO gin_option_prefix", 0))
		check("{fastupdate=off,gin_pending_list_limit=64}", []int64{1})
		run(fixture.Exec("ALTER INDEX gin_options_i SET(gin_pending_list_limit=128)", 0))
		check("{fastupdate=off,gin_pending_list_limit=128}", []int64{1})
		run(fixture.Exec("ALTER INDEX gin_options_i RESET(fastupdate,gin_pending_list_limit)", 0))
		run(fixture.Exec("COMMIT", 0))
		check(nil, []int64{1})
		classRoutes(t, p, b, conn, fixture.Select("SELECT pg_get_indexdef('gin_options_i'::regclass) AS definition", []string{"definition"}, []string{"TEXT"}, [][]any{{"CREATE INDEX gin_options_i ON public.gin_options USING gin (a)"}}))
		run(fixture.Exec("INSERT INTO gin_options VALUES(4,'{2,2}')", 1))
		check(nil, []int64{1, 4})
		run(fixture.Exec("REINDEX INDEX gin_options_i", 0))
		check(nil, []int64{1, 4})
	})
}
