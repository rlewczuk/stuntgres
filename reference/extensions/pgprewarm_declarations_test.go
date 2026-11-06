package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgPrewarmDeclarations(t *testing.T) {
	pwScenario(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		q := "SELECT p.pronargs::integer AS nargs,p.proargtypes::text AS args,p.prorettype::bigint AS result,p.pronargdefaults::integer AS defaults,p.proargnames::text AS names,p.proallargtypes::text AS allargs,p.proargmodes::text AS modes,p.proisstrict AS strict,p.provolatile::text AS volatility,p.proparallel::text AS parallel,p.proretset AS sets,p.prosecdef AS definer,p.prosrc AS source,p.probin AS binary,l.lanname::text AS language,p.procost::bigint AS cost,p.prorows::bigint AS rows FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace JOIN pg_language l ON l.oid=p.prolang WHERE n.nspname='pw_part' AND p.proname='pg_prewarm'"
		bcBoth(t, p, b, c, fixture.Select(q, []string{"nargs", "args", "result", "defaults", "names", "allargs", "modes", "strict", "volatility", "parallel", "sets", "definer", "source", "binary", "language", "cost", "rows"}, []string{"INT4", "TEXT", "INT8", "INT4", "TEXT", "TEXT", "TEXT", "BOOL", "TEXT", "TEXT", "BOOL", "BOOL", "TEXT", "TEXT", "TEXT", "INT8", "INT8"}, [][]any{{int64(5), "2205 25 25 20 20", int64(20), int64(4), "{\"\",mode,fork,first_block,last_block}", nil, nil, false, "v", "s", false, false, "pg_prewarm", "$libdir/pg_prewarm", "c", int64(1), int64(0)}}))
		bcBoth(t, p, b, c, fixture.Select("SELECT pg_get_expr(proargdefaults,0) AS defaults FROM pg_proc WHERE oid=$1::regprocedure", []string{"defaults"}, []string{"TEXT"}, [][]any{{"'buffer'::text, 'main'::text, NULL::bigint, NULL::bigint"}}, pwSignature))
		bcBoth(t, p, b, c, fixture.Select("SELECT extversion,extrelocatable,obj_description(oid,'pg_extension') AS description FROM pg_extension WHERE extname='pg_prewarm'", []string{"extversion", "extrelocatable", "description"}, []string{"TEXT", "BOOL", "TEXT"}, [][]any{{"1.2", true, "prewarm relation data"}}))
		bcBoth(t, p, b, c, fixture.Select("SELECT version,superuser,trusted,relocatable FROM pg_available_extension_versions WHERE name='pg_prewarm' AND version='1.2'", []string{"version", "superuser", "trusted", "relocatable"}, []string{"TEXT", "BOOL", "BOOL", "BOOL"}, [][]any{{"1.2", true, false, true}}))
		for _, call := range []string{
			"pw_part.pg_prewarm('pw_part.heap')",
			"pw_part.pg_prewarm('pw_part.heap','buffer')",
			"pw_part.pg_prewarm('pw_part.heap','buffer','main')",
			"pw_part.pg_prewarm('pw_part.heap','buffer','main',NULL)",
			"pw_part.pg_prewarm('pw_part.heap','buffer','main',NULL,NULL)",
			"pw_part.pg_prewarm('pw_part.heap',last_block=>NULL)",
			"pw_part.pg_prewarm('pw_part.heap',fork=>'main',mode=>'buffer')",
		} {
			pwRoutes(t, p, b, c, pwCount("SELECT "+call+" AS warmed", 0))
			pwRoutes(t, p, b, c, pwCount("SELECT * FROM "+call+" AS warmed", 0))
		}
		pwRoutes(t, p, b, c, fsMetadata(fixture.Select("SELECT pw_part.pg_prewarm('pw_part.heap')", []string{"pg_prewarm"}, []string{"INT8"}, [][]any{{int64(0)}}), b.PG))
		pwInsert(t, p, b, c, "pw_part.heap", 1)
		pwRoutes(t, p, b, c, pwCount(pwCall, 1, "pw_part.heap", "buffer", "main", nil, nil))
		pwRoutes(t, p, b, c, pwCount("SELECT pw_part.pg_prewarm($1::regclass,'buffer','main',$2::integer,$3::integer) AS warmed", 1, "pw_part.heap", int64(0), int64(0)))
		pwRoutes(t, p, b, c, pwCount("SELECT pw_part.pg_prewarm($1) AS warmed", 1, "pw_part.heap"))
		bcDDL(t, p, b, c, "CREATE SEQUENCE pw_part.eval_once")
		// Reordered named syntax retains the correct formal values. Exactly
		// five side effects are required, not a portable cross-child order.
		q = "SELECT pw_part.pg_prewarm(CASE WHEN nextval('pw_part.eval_once')>0 THEN 'pw_part.heap'::regclass END,last_block=>CASE WHEN nextval('pw_part.eval_once')>0 THEN 0::bigint ELSE -1::bigint END,mode=>CASE WHEN nextval('pw_part.eval_once')>0 THEN 'buffer' ELSE 'bad' END,first_block=>CASE WHEN nextval('pw_part.eval_once')>0 THEN 0::bigint ELSE -1::bigint END,fork=>CASE WHEN nextval('pw_part.eval_once')>0 THEN 'main' ELSE 'bad' END) AS warmed"
		bcRun(t, p, b, c, fixture.Select("SELECT nextval('pw_part.eval_once') AS n", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
		idle, err := c.PrepareContext(p.Ctx, q)
		if err != nil { t.Fatal(err) }
		bcBoth(t, p, b, c, fixture.Select("SELECT currval('pw_part.eval_once') AS n", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
		if err := idle.Close(); err != nil { t.Fatal(err) }
		for _, route := range []string{"direct", "fresh", "held"} {
			bcDDL(t, p, b, c, "ALTER SEQUENCE pw_part.eval_once RESTART WITH 1")
			s := pwCount(q, 1)
			switch route {
			case "direct":
				bcRun(t, p, b, c, s)
			case "fresh":
				bcRun(t, p, b, c, fixture.Prepared(s))
			case "held":
				st, err := c.PrepareContext(p.Ctx, q)
				if err != nil { t.Fatal(err) }
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: st, SQL: q}, b.Recorder, s)
				if err := st.Close(); err != nil { t.Fatal(err) }
			}
			bcBoth(t, p, b, c, fixture.Select("SELECT currval('pw_part.eval_once') AS n", []string{"n"}, []string{"INT8"}, [][]any{{int64(5)}}))
		}
	})
}
