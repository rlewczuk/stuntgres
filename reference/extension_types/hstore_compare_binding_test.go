package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func hstoreComparisonBinding(t *testing.T) {
	const ns = "hs_cmp_binding"
	fixture.SchemaReferenceCleanup(t, ns)
	hstoreComparisonScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, q := range []string{
			"CREATE SCHEMA " + ns,
			"CREATE EXTENSION hstore SCHEMA " + ns,
			"SET search_path=" + ns + ",public",
			"CREATE TABLE public.hs_cmp_binding_data(id integer,v hstore)",
		} {
			hstoreRun(t, p, b, c, fixture.Exec(q, 0))
		}
		hstoreRun(t, p, b, c, fixture.Exec("INSERT INTO public.hs_cmp_binding_data VALUES (1,'ab=>c'),(2,'a=>bc'),(3,'ab=>c')", 3))
		qualified := "SELECT $1::" + ns + ".hstore OPERATOR(" + ns + ".=) $2::" + ns + ".hstore AS eq," + ns + ".hstore_cmp($1::" + ns + ".hstore,$2::" + ns + ".hstore) AS cmp"
		stmt, err := c.PrepareContext(p.Ctx, qualified)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: qualified}
		check := func() {
			for _, tc := range []struct {
				left, right string
				equal       bool
				cmp         int64
			}{{"a=>bc", "ab=>c", false, -1}, {"b=>2,a=>1", "a=>1,b=>2", true, 0}} {
				s := fixture.Select(qualified, []string{"eq", "cmp"}, []string{"BOOL", "INT4"}, [][]any{{tc.equal, tc.cmp}}, tc.left, tc.right)
				hstoreRun(t, p, b, c, s)
				hstoreRun(t, p, b, c, fixture.Prepared(s))
				hstoreRun(t, p, b, held, s)
			}
			s := hstoreComparisonIDs("SELECT id FROM public.hs_cmp_binding_data ORDER BY v,id", 2, 1, 3)
			hstoreRun(t, p, b, c, s)
			hstoreRun(t, p, b, c, fixture.Prepared(s))
		}
		check()
		// Real user-defined overloads intentionally disagree. A name/signature
		// match is not extension authority for qualified calls or class support.
		for _, q := range []string{
			"CREATE FUNCTION public.hstore_cmp(" + ns + ".hstore," + ns + ".hstore) RETURNS integer LANGUAGE SQL IMMUTABLE STRICT AS $$ SELECT 99 $$",
			"CREATE FUNCTION public.hstore_eq(" + ns + ".hstore," + ns + ".hstore) RETURNS boolean LANGUAGE SQL IMMUTABLE STRICT AS $$ SELECT false $$",
			"CREATE OPERATOR public.= (LEFTARG=" + ns + ".hstore,RIGHTARG=" + ns + ".hstore,FUNCTION=public.hstore_eq)",
			"SET search_path=public," + ns,
		} {
			hstoreRun(t, p, b, c, fixture.Exec(q, 0))
		}
		for _, s := range []fixture.Step{
			fixture.Select("SELECT hstore_cmp('a=>bc'::"+ns+".hstore,'ab=>c'::"+ns+".hstore) AS shadow,'a=>1'::"+ns+".hstore='a=>1'::"+ns+".hstore AS equal", []string{"shadow", "equal"}, []string{"INT4", "BOOL"}, [][]any{{int64(99), false}}),
			fixture.Select("SELECT count(*) AS n FROM (SELECT DISTINCT v FROM public.hs_cmp_binding_data) d", []string{"n"}, []string{"INT8"}, [][]any{{int64(2)}}),
		} {
			hstoreRun(t, p, b, c, s)
			hstoreRun(t, p, b, c, fixture.Prepared(s))
		}
		check()
		// Presentation-only column rename keeps the same stored type and class.
		hstoreRun(t, p, b, c, fixture.Exec("ALTER TABLE public.hs_cmp_binding_data RENAME COLUMN v TO value", 0))
		hstoreRun(t, p, b, c, hstoreComparisonIDs("SELECT id FROM public.hs_cmp_binding_data ORDER BY value,id", 2, 1, 3))
		hstoreRun(t, p, b, held, fixture.Select(qualified, []string{"eq", "cmp"}, []string{"BOOL", "INT4"}, [][]any{{true, int64(0)}}, "a=>1", "a=>1"))
	})
}
