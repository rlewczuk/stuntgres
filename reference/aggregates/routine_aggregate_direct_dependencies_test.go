package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRoutineAggregateDirectBags(t *testing.T) {
	aggregateDirectScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		aggregateDirectSetup(t, p, b, c)
		aggregateDirectCallers(t, p, b, c)
		aggregateDirectExec(t, p, b, c,
			"CREATE AGGREGATE ad_b(integer)(SFUNC=ad_s,STYPE=integer,INITCOND='0',FINALFUNC=ad_f)",
			"CREATE FUNCTION ad_two() RETURNS integer BEGIN ATOMIC SELECT ad_a(v)+ad_b(v) FROM ad_input; END",
			"CREATE FUNCTION ad_argument() RETURNS integer BEGIN ATOMIC SELECT ad_a(ad_f(ad_s(v,0))) FROM ad_input; END",
			"CREATE AGGREGATE ad_builtin(integer)(SFUNC=pg_catalog.int4pl,STYPE=integer,INITCOND='0')",
			"CREATE FUNCTION ad_builtin_c() RETURNS integer BEGIN ATOMIC SELECT ad_builtin(v) FROM ad_input; END",
			// Zero-input transition and final can use the identical unary support.
			"CREATE AGGREGATE ad_roles(*)(SFUNC=ad_f,STYPE=integer,INITCOND='1',FINALFUNC=ad_f)",
			"CREATE FUNCTION ad_roles_c() RETURNS integer BEGIN ATOMIC SELECT ad_roles(*) FROM(VALUES(1)) t(v); END")
		createAggregateBoth(t, p, b, c, aggregateDirectBag([]string{"ad_a", "ad_b", "ad_c", "ad_e", "ad_m", "ad_reverse", "ad_r", "ad_two", "ad_argument", "ad_builtin", "ad_builtin_c", "ad_roles", "ad_roles_c"},
			[2]string{"ad_a", "ad_s"}, [2]string{"ad_a", "ad_f"}, [2]string{"ad_b", "ad_s"}, [2]string{"ad_b", "ad_f"},
			[2]string{"ad_c", "ad_a"}, [2]string{"ad_e", "ad_s"}, [2]string{"ad_e", "ad_f"},
			[2]string{"ad_m", "ad_a"}, [2]string{"ad_m", "ad_s"}, [2]string{"ad_m", "ad_f"},
			[2]string{"ad_reverse", "ad_a"}, [2]string{"ad_reverse", "ad_s"}, [2]string{"ad_reverse", "ad_f"},
			[2]string{"ad_r", "ad_a"}, [2]string{"ad_r", "ad_s"}, [2]string{"ad_r", "ad_f"},
			[2]string{"ad_two", "ad_a"}, [2]string{"ad_two", "ad_b"},
			[2]string{"ad_argument", "ad_a"}, [2]string{"ad_argument", "ad_s"}, [2]string{"ad_argument", "ad_f"},
			[2]string{"ad_builtin_c", "ad_builtin"}, [2]string{"ad_roles", "ad_f"}, [2]string{"ad_roles_c", "ad_roles"}))
		for _, tc := range []struct {
			name  string
			value int64
		}{{"ad_c", 30}, {"ad_e", 30}, {"ad_m", 60}, {"ad_reverse", 60}, {"ad_r", 120}, {"ad_two", 60}, {"ad_argument", 300}, {"ad_builtin_c", 3}, {"ad_roles_c", 100}} {
			createAggregateBoth(t, p, b, c, createAggregateValue("SELECT "+tc.name+"() AS v", tc.value))
		}
	})
}

func TestReferenceRoutineAggregateDirectDefaults(t *testing.T) {
	aggregateDirectScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		aggregateDirectSetup(t, p, b, c)
		aggregateDirectExec(t, p, b, c,
			"CREATE FUNCTION ad_d(x integer DEFAULT ad_f(ad_s(1,2))) RETURNS integer BEGIN ATOMIC SELECT ad_a(v) FROM(VALUES(x)) t(v); END",
			"CREATE FUNCTION ad_both(x integer DEFAULT ad_f(1)) RETURNS integer RETURN ad_f(x)",
			"CREATE FUNCTION ad_phases(x integer DEFAULT ad_f(1)+ad_f(1),y integer DEFAULT ad_f(2)) RETURNS integer RETURN ad_f(x)+ad_f(y)+ad_f(x)")
		createAggregateBoth(t, p, b, c, aggregateDirectBag([]string{"ad_a", "ad_d", "ad_both", "ad_phases"},
			[2]string{"ad_a", "ad_s"}, [2]string{"ad_a", "ad_f"},
			[2]string{"ad_d", "ad_a"}, [2]string{"ad_d", "ad_s"}, [2]string{"ad_d", "ad_f"},
			[2]string{"ad_both", "ad_f"}, [2]string{"ad_both", "ad_f"},
			[2]string{"ad_phases", "ad_f"}, [2]string{"ad_phases", "ad_f"}))
		for _, tc := range []struct {
			q     string
			value int64
		}{{"SELECT ad_d() AS v", 300}, {"SELECT ad_d(2) AS v", 20}, {"SELECT ad_both() AS v", 100}, {"SELECT ad_phases() AS v", 600}} {
			createAggregateBoth(t, p, b, c, createAggregateValue(tc.q, tc.value))
		}
		q := "CREATE FUNCTION ad_invalid(x integer DEFAULT ad_a(1)) RETURNS integer RETURN x"
		s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42803", Message: "aggregate functions are not allowed in DEFAULT expressions", Position: fixture.Position(strings.Index(q, "ad_a(") + 1)})
		s.ErrorStage = "execute"
		createAggregateRun(t, p, b, c, s, fixture.Prepared(s))
	})
}

func TestReferenceRoutineAggregateDirectTextBodies(t *testing.T) {
	aggregateDirectScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		aggregateDirectSetup(t, p, b, c)
		aggregateDirectExec(t, p, b, c,
			"CREATE FUNCTION ad_c() RETURNS integer BEGIN ATOMIC SELECT ad_a(v) FROM ad_input; END",
			"CREATE FUNCTION ad_soft() RETURNS integer LANGUAGE SQL AS $$SELECT ad_a(v) FROM ad_input$$",
			"CREATE FUNCTION ad_pl() RETURNS integer LANGUAGE plpgsql AS $$DECLARE n integer; BEGIN SELECT ad_a(v) INTO n FROM ad_input; RETURN n; END$$",
			"CREATE FUNCTION ad_soft_default(x integer DEFAULT ad_f(ad_s(1,2))) RETURNS integer LANGUAGE SQL AS $$SELECT ad_a(v) FROM(VALUES(x)) t(v)$$",
			"CREATE FUNCTION ad_pl_default(x integer DEFAULT ad_f(ad_s(1,2))) RETURNS integer LANGUAGE plpgsql AS $$DECLARE n integer; BEGIN SELECT ad_a(v) INTO n FROM(VALUES(x)) t(v); RETURN n; END$$")
		createAggregateBoth(t, p, b, c, aggregateDirectBag([]string{"ad_c", "ad_soft", "ad_pl", "ad_soft_default", "ad_pl_default"},
			[2]string{"ad_c", "ad_a"}, [2]string{"ad_soft_default", "ad_s"}, [2]string{"ad_soft_default", "ad_f"}, [2]string{"ad_pl_default", "ad_s"}, [2]string{"ad_pl_default", "ad_f"}))
		for _, name := range []string{"ad_c", "ad_soft", "ad_pl", "ad_soft_default", "ad_pl_default"} {
			want := int64(30)
			if name == "ad_soft_default" || name == "ad_pl_default" {
				want = 300
			}
			createAggregateBoth(t, p, b, c, createAggregateValue("SELECT "+name+"() AS v", want))
		}
		aggregateDirectExec(t, p, b, c, "DROP AGGREGATE ad_a(integer) CASCADE")
		createAggregateBoth(t, p, b, c, fixture.Select("SELECT proname::text AS v FROM pg_proc WHERE proname IN('ad_c','ad_soft','ad_pl','ad_soft_default','ad_pl_default') ORDER BY proname", []string{"v"}, []string{"TEXT"}, [][]any{{"ad_pl"}, {"ad_pl_default"}, {"ad_soft"}, {"ad_soft_default"}}))
		for _, name := range []string{"ad_soft", "ad_pl"} {
			native := fixture.ControlsReferenceDiagnostic("42883", "function ad_a(integer) does not exist")
			native.Position = "8"
			native.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
			native.Context = "SQL function \"" + name + "\" statement 1"
			pg := native
			pg.Position = ""
			pg.InternalPosition = "8"
			pg.InternalQuery = "SELECT ad_a(v) FROM ad_input"
			pg.Context = "SQL function \"ad_soft\" during inlining"
			if name == "ad_pl" {
				pg.InternalQuery = "SELECT ad_a(v)        FROM ad_input"
				pg.Context = "PL/pgSQL function ad_pl() line 1 at SQL statement"
				native = pg
			}
			s := fixture.ErrorDiagnostic("SELECT "+name+"() AS v", native)
			s.PostgresDiagnostic = &pg
			s.SelectRows = true
			s.ErrorStage = "execute"
			createAggregateRun(t, p, b, c, s, fixture.Prepared(s))
		}
	})
}
