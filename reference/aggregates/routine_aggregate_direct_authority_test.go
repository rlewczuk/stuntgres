package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRoutineAggregateDirectAuthority(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"ad_owner", "ad_reader", "ad_new_owner"})
	aggregateDirectScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		aggregateDirectSetup(t, p, b, c)
		exec := func(q ...string) { aggregateDirectExec(t, p, b, c, q...) }
		exec("CREATE ROLE ad_owner", "CREATE ROLE ad_reader", "CREATE ROLE ad_new_owner",
			"GRANT USAGE,CREATE ON SCHEMA public TO ad_owner,ad_new_owner", "GRANT USAGE ON SCHEMA public TO ad_reader",
			"GRANT SELECT ON ad_input TO ad_reader",
			"REVOKE EXECUTE ON FUNCTION ad_s(integer,integer),ad_f(integer) FROM PUBLIC",
			"GRANT EXECUTE ON FUNCTION ad_s(integer,integer),ad_f(integer) TO ad_owner",
			"ALTER AGGREGATE ad_a(integer) OWNER TO ad_owner",
			"REVOKE EXECUTE ON FUNCTION ad_a(integer) FROM PUBLIC",
			"GRANT EXECUTE ON FUNCTION ad_a(integer) TO ad_reader",
			"CREATE FUNCTION ad_c(populated boolean) RETURNS integer BEGIN ATOMIC SELECT ad_a(v) FROM ad_input WHERE populated; END",
			"CREATE FUNCTION ad_m(populated boolean) RETURNS integer BEGIN ATOMIC SELECT ad_a(v)+ad_f(ad_s(1,2)) FROM ad_input WHERE populated; END")
		defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		exec("SET SESSION AUTHORIZATION ad_reader")
		held := aggregateDirectHeld(t, p, c, "SELECT ad_c($1) AS v")
		mixed := aggregateDirectHeld(t, p, c, "SELECT ad_m($1) AS v")
		values := func() {
			for _, populated := range []bool{true, false} {
				want := int64(0)
				if populated {
					want = 30
				}
				s := createAggregateValue(held.SQL, want, populated)
				createAggregateBoth(t, p, b, c, s)
				fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			}
		}
		denied := func(h fixture.HeldStatement, name, context string) {
			for _, populated := range []bool{true, false} {
				s := fixture.ImplicitError(h.SQL, fixture.Diagnostic{Code: "42501", Message: "permission denied for " + name, Context: context}, populated)
				s.SelectRows = true
				s.ErrorStage = "execute"
				createAggregateBoth(t, p, b, c, s)
				fixture.RunStep(t, p.Ctx, h, b.Recorder, s)
			}
		}
		values()
		denied(mixed, "function ad_f", "SQL function \"ad_m\" statement 1")
		for _, support := range []string{"ad_s(integer,integer)", "ad_f(integer)"} {
			exec("RESET SESSION AUTHORIZATION", "REVOKE EXECUTE ON FUNCTION "+support+" FROM ad_owner", "SET SESSION AUTHORIZATION ad_reader")
			name := "ad_s"
			if support == "ad_f(integer)" {
				name = "ad_f"
			}
			denied(held, "function "+name, "SQL function \"ad_c\" statement 1")
			exec("RESET SESSION AUTHORIZATION", "GRANT EXECUTE ON FUNCTION "+support+" TO ad_owner", "SET SESSION AUTHORIZATION ad_reader")
			values()
		}
		exec("RESET SESSION AUTHORIZATION", "REVOKE EXECUTE ON FUNCTION ad_a(integer) FROM ad_reader", "SET SESSION AUTHORIZATION ad_reader")
		denied(held, "aggregate ad_a", "SQL function \"ad_c\" statement 1")
		exec("RESET SESSION AUTHORIZATION", "GRANT EXECUTE ON FUNCTION ad_a(integer) TO ad_reader", "SET SESSION AUTHORIZATION ad_reader")
		values()
		exec("RESET SESSION AUTHORIZATION", "ALTER AGGREGATE ad_a(integer) OWNER TO ad_new_owner", "SET SESSION AUTHORIZATION ad_reader")
		denied(held, "function ad_f", "SQL function \"ad_c\" statement 1")
		exec("RESET SESSION AUTHORIZATION", "GRANT EXECUTE ON FUNCTION ad_s(integer,integer),ad_f(integer) TO ad_new_owner", "SET SESSION AUTHORIZATION ad_reader")
		values()
		exec("RESET SESSION AUTHORIZATION", "GRANT EXECUTE ON FUNCTION ad_s(integer,integer),ad_f(integer) TO ad_reader", "SET SESSION AUTHORIZATION ad_reader")
		for _, populated := range []bool{true, false} {
			want := int64(30)
			if populated {
				want = 60
			}
			s := createAggregateValue(mixed.SQL, want, populated)
			createAggregateBoth(t, p, b, c, s)
			fixture.RunStep(t, p.Ctx, mixed, b.Recorder, s)
		}
		exec("RESET SESSION AUTHORIZATION",
			"CREATE TABLE ad_private(v integer)",
			"CREATE FUNCTION ad_body_s(integer,integer) RETURNS integer BEGIN ATOMIC SELECT $1+$2+v FROM ad_private; END",
			"REVOKE EXECUTE ON FUNCTION ad_body_s(integer,integer) FROM PUBLIC",
			"GRANT EXECUTE ON FUNCTION ad_body_s(integer,integer) TO ad_owner",
			"GRANT SELECT ON ad_private TO ad_owner",
			"SET SESSION AUTHORIZATION ad_owner",
			"CREATE AGGREGATE ad_body(integer)(SFUNC=ad_body_s,STYPE=integer,INITCOND='0')",
			"RESET SESSION AUTHORIZATION",
			"CREATE FUNCTION ad_body_c() RETURNS integer BEGIN ATOMIC SELECT ad_body(v) FROM(VALUES(1)) t(v); END")
		createAggregateRun(t, p, b, c, fixture.Exec("INSERT INTO ad_private VALUES(10)", 1))
		exec("SET SESSION AUTHORIZATION ad_reader")
		s := fixture.ImplicitError("SELECT ad_body_c() AS v", fixture.Diagnostic{Code: "42501", Message: "permission denied for table ad_private", Context: "SQL function \"ad_body_s\" statement 1\nSQL function \"ad_body_c\" statement 1"})
		s.SelectRows = true
		s.ErrorStage = "execute"
		createAggregateRun(t, p, b, c, s, fixture.Prepared(s))
		exec("RESET SESSION AUTHORIZATION", "GRANT SELECT ON ad_private TO ad_reader", "SET SESSION AUTHORIZATION ad_reader")
		createAggregateBoth(t, p, b, c, createAggregateValue("SELECT ad_body_c() AS v", int64(11)))
	})
}

func TestReferenceRoutineAggregateDirectIdentity(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "ad_moved")
	aggregateDirectScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		aggregateDirectSetup(t, p, b, c)
		aggregateDirectExec(t, p, b, c, "CREATE FUNCTION ad_c() RETURNS integer BEGIN ATOMIC SELECT ad_a(v) FROM ad_input; END")
		ids := aggregateDirectIDs(t, p, c)
		held := aggregateDirectHeld(t, p, c, "SELECT ad_c() AS v")
		aggregateDirectExec(t, p, b, c,
			"CREATE SCHEMA ad_moved",
			"ALTER FUNCTION ad_s(integer,integer) RENAME TO ad_step",
			"ALTER FUNCTION ad_step(integer,integer) SET SCHEMA ad_moved",
			"ALTER FUNCTION ad_f(integer) RENAME TO ad_final",
			"ALTER FUNCTION ad_final(integer) SET SCHEMA ad_moved",
			"ALTER AGGREGATE ad_a(integer) RENAME TO ad_total",
			"ALTER AGGREGATE ad_total(integer) SET SCHEMA ad_moved",
			"REVOKE EXECUTE ON FUNCTION ad_moved.ad_step(integer,integer) FROM PUBLIC",
			"GRANT EXECUTE ON FUNCTION ad_moved.ad_step(integer,integer) TO PUBLIC",
			"ALTER AGGREGATE ad_moved.ad_total(integer) OWNER TO CURRENT_USER")
		moved := aggregateDirectIDs(t, p, c)
		for old, current := range map[string]string{"ad_s": "ad_step", "ad_f": "ad_final", "ad_a": "ad_total"} {
			if moved["ad_moved."+current] != ids["public."+old] {
				t.Fatalf("identity changed for %s: %v -> %v", old, ids, moved)
			}
		}
		aggregateDirectExec(t, p, b, c,
			"CREATE FUNCTION ad_s(integer,integer) RETURNS integer LANGUAGE SQL AS $$SELECT coalesce($1,0)+coalesce($2,0)+100$$",
			"CREATE FUNCTION ad_f(integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1*100$$",
			"CREATE AGGREGATE ad_a(integer)(SFUNC=ad_s,STYPE=integer,INITCOND='0',FINALFUNC=ad_f)")
		competitors := aggregateDirectIDs(t, p, c)
		for _, name := range []string{"ad_s", "ad_f", "ad_a"} {
			if competitors["public."+name] == ids["public."+name] {
				t.Fatal("competitor reused original", name)
			}
		}
		check := func(want int64) {
			createAggregateBoth(t, p, b, c, createAggregateValue(held.SQL, want))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, createAggregateValue(held.SQL, want))
			createAggregateBoth(t, p, b, c, aggregateDirectBag([]string{"ad_c", "ad_total"}, [2]string{"ad_c", "ad_total"}, [2]string{"ad_total", "ad_step"}, [2]string{"ad_total", "ad_final"}))
		}
		check(30)
		createAggregateBoth(t, p, b, c, createAggregateValue("SELECT ad_a(v) AS v FROM ad_input", int64(30300)))
		createAggregateRun(t, p, b, c, fixture.Exec("DELETE FROM ad_input", 3))
		check(0)
		createAggregateRun(t, p, b, c, fixture.Exec("INSERT INTO ad_input VALUES(1),(2),(NULL)", 3))
		check(30)
		s := aggregateDirectRestrict("DROP AGGREGATE ad_moved.ad_total(integer)", "ad_moved.ad_total(integer)", "function ad_c() depends on function ad_moved.ad_total(integer)")
		createAggregateRun(t, p, b, c, s, fixture.Prepared(s))
		check(30)
		aggregateDirectSameIDs(t, p, c, competitors)
	})
}
