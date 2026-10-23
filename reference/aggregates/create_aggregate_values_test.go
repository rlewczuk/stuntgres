package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func createAggregateScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			body(t, p, b, c)
		})
	}
}

func createAggregateRun(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, steps ...fixture.Step) {
	t.Helper()
	for _, s := range steps {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	}
}

func createAggregateBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, s fixture.Step) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
}

func createAggregateValue(q string, value any, args ...any) fixture.Step {
	return fixture.Select(q, []string{"v"}, []string{"INT4"}, [][]any{{value}}, args...)
}

func createAggregateSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	createAggregateRun(t, p, b, c,
		fixture.Exec("CREATE FUNCTION ca_add(integer,integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1+$2$$", 0),
		fixture.Exec("CREATE AGGREGATE ca_sum(integer) (SFUNC=ca_add,STYPE=integer,INITCOND='000',SSPACE=32)", 0),
		fixture.Exec("CREATE TABLE ca_input(id integer,g integer,x integer)", 0),
		fixture.Exec("INSERT INTO ca_input VALUES(1,1,1),(2,1,2),(3,2,3)", 3))
}

func TestReferenceCreateAggregateCreation(t *testing.T) {
	createAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		createAggregateSetup(t, p, b, c)
		createAggregateRun(t, p, b, c,
			fixture.Prepared(fixture.Exec("CREATE AGGREGATE ca_prepared(integer) (INITCOND='0',STYPE=int4,SFUNC=public.ca_add)", 0)),
			fixture.Exec("CREATE AGGREGATE ca_repeated(integer) (SFUNC=ca_add,STYPE=int4,INITCOND='9',INITCOND='0',SSPACE=8,SSPACE=16)", 0))
		for _, name := range []string{"ca_sum", "ca_prepared", "ca_repeated"} {
			createAggregateBoth(t, p, b, c, createAggregateValue("SELECT "+name+"(x) AS v FROM ca_input", int64(6)))
			createAggregateBoth(t, p, b, c, createAggregateValue("SELECT "+name+"(x) AS v FROM ca_input WHERE FALSE", int64(0)))
		}
	})
}

func TestReferenceCreateAggregateValues(t *testing.T) {
	createAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		createAggregateSetup(t, p, b, c)
		for _, q := range []string{
			"SELECT ca_sum(x) AS v FROM ca_input",
			"SELECT ca_sum(DISTINCT x ORDER BY x) FILTER(WHERE x>0) AS v FROM ca_input",
			"SELECT ca_sum(x ORDER BY id DESC) AS v FROM ca_input",
		} {
			createAggregateBoth(t, p, b, c, createAggregateValue(q, int64(6)))
		}
		createAggregateBoth(t, p, b, c, fixture.Select("SELECT g,ca_sum(x) AS v FROM ca_input GROUP BY g HAVING ca_sum(x)=3 ORDER BY g", []string{"g", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(3)}, {int64(2), int64(3)}}))
		createAggregateBoth(t, p, b, c, fixture.Select("SELECT g,ca_sum(x) AS v FROM ca_input WHERE FALSE GROUP BY g", []string{"g", "v"}, []string{"INT4", "INT4"}, nil))
		const q = "SELECT ca_sum(x) AS v FROM ca_input WHERE id<=$1"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, tc := range []struct{ limit, value int64 }{{3, 6}, {0, 0}, {1, 1}, {3, 6}} {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, createAggregateValue(q, tc.value, tc.limit))
		}
	})
}

func TestReferenceCreateAggregateStrictness(t *testing.T) {
	createAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		createAggregateRun(t, p, b, c,
			fixture.Exec("CREATE FUNCTION ca_null_step(integer,integer) RETURNS integer LANGUAGE SQL AS $$SELECT coalesce($1,0)+coalesce($2,100)$$", 0),
			fixture.Exec("CREATE AGGREGATE ca_null(integer) (SFUNC=ca_null_step,STYPE=int4)", 0),
			fixture.Exec("CREATE AGGREGATE ca_strict(integer) (SFUNC=pg_catalog.int4pl,STYPE=int4,INITCOND='0')", 0),
			fixture.Exec("CREATE AGGREGATE ca_unseeded(integer) (SFUNC=pg_catalog.int4pl,STYPE=int4)", 0))
		for _, tc := range []struct {
			q    string
			want any
		}{
			{"SELECT ca_null(x) AS v FROM(VALUES(NULL::int),(1)) s(x)", int64(101)},
			{"SELECT ca_null(x) AS v FROM(VALUES(NULL::int),(NULL::int)) s(x)", int64(200)},
			{"SELECT ca_null(x) AS v FROM(VALUES(1)) s(x) WHERE FALSE", nil},
			{"SELECT ca_strict(x) AS v FROM(VALUES(NULL::int),(1),(2),(3)) s(x)", int64(6)},
			{"SELECT ca_unseeded(x) AS v FROM(VALUES(NULL::int),(1),(2),(3)) s(x)", int64(6)},
			{"SELECT ca_strict(x) AS v FROM(VALUES(NULL::int)) s(x)", int64(0)},
			{"SELECT ca_unseeded(x) AS v FROM(VALUES(NULL::int)) s(x)", nil},
			{"SELECT ca_strict(x) AS v FROM(VALUES(1)) s(x) WHERE FALSE", int64(0)},
			{"SELECT ca_unseeded(x) AS v FROM(VALUES(1)) s(x) WHERE FALSE", nil},
		} {
			createAggregateBoth(t, p, b, c, createAggregateValue(tc.q, tc.want))
		}
	})
}

func TestReferenceCreateAggregateArguments(t *testing.T) {
	createAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		zeroFailure := fixture.ImplicitError("CREATE AGGREGATE ca_zero_strict(*) (SFUNC=pg_catalog.abs,STYPE=int4)", fixture.Diagnostic{Code: "42P13", Message: "must not omit initial value when transition function is strict and transition type is not compatible with input type"})
		zeroFailure.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, c, b.Recorder, zeroFailure)
		createAggregateRun(t, p, b, c, fixture.Exec("CREATE AGGREGATE ca_zero_strict(*) (SFUNC=pg_catalog.abs,STYPE=int4,INITCOND='-7')", 0))
		createAggregateBoth(t, p, b, c, createAggregateValue("SELECT ca_zero_strict(*) AS v FROM(VALUES(1),(2)) s(x)", int64(7)))
		createAggregateBoth(t, p, b, c, createAggregateValue("SELECT ca_zero_strict(*) AS v FROM(VALUES(1)) s(x) WHERE FALSE", int64(-7)))
		createAggregateRun(t, p, b, c,
			fixture.Exec("CREATE FUNCTION ca_tick(integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1+1$$", 0),
			fixture.Exec("CREATE AGGREGATE ca_count(*) (SFUNC=ca_tick,STYPE=int4,INITCOND='0')", 0),
			fixture.Exec("CREATE FUNCTION ca_pair_step(integer,integer,integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1+coalesce($2,0)+coalesce($3,0)$$", 0),
			fixture.Exec("CREATE AGGREGATE ca_pair(integer,integer) (SFUNC=ca_pair_step,STYPE=int4,INITCOND='0')", 0),
			fixture.Exec("CREATE FUNCTION ca_four_step(integer,integer,integer,integer,integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1+$2+$3+$4+$5$$", 0),
			fixture.Exec("CREATE AGGREGATE ca_four(integer,integer,integer,integer) (SFUNC=ca_four_step,STYPE=int4,INITCOND='0')", 0))
		for _, tc := range []struct {
			q    string
			want int64
		}{
			{"SELECT ca_count(*) AS v FROM(VALUES(1),(2),(3)) s(x)", 3},
			{"SELECT ca_count(*) AS v FROM(VALUES(1)) s(x) WHERE FALSE", 0},
			{"SELECT ca_pair(x,y) AS v FROM(VALUES(1,10),(2,NULL),(NULL,30)) s(x,y)", 43},
			{"SELECT ca_four(1,10,100,1000) AS v", 1111},
			{"SELECT ca_pair(DISTINCT x,y ORDER BY x,y) AS v FROM(VALUES(1,10),(1,10),(1,20)) s(x,y)", 32},
		} {
			createAggregateBoth(t, p, b, c, createAggregateValue(tc.q, tc.want))
		}
		const q = "SELECT ca_four($1,$2,$3,$4) AS v"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, createAggregateValue(q, int64(1111), 1, 10, 100, 1000))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, createAggregateValue(q, nil, 1, 10, nil, 1000))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, createAggregateValue(q, int64(2222), 2, 20, 200, 2000))
		for _, tc := range []struct{ q, code, message, hint string }{
			{"SELECT ca_pair(1)", "42883", "function ca_pair(integer) does not exist", "No function matches the given name and argument types. You might need to add explicit type casts."},
			{"SELECT ca_pair(TRUE,TRUE)", "42883", "function ca_pair(boolean, boolean) does not exist", "No function matches the given name and argument types. You might need to add explicit type casts."},
			{"SELECT ca_count()", "42809", "ca_count(*) must be used to call a parameterless aggregate function", ""},
		} {
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: tc.code, Message: tc.message, Hint: tc.hint, Position: "8"})
				s.Prepared = prepared
				s.SelectRows = true
				if prepared {
					s.ErrorStage = "prepare"
				} else {
					s.ErrorStage = "execute"
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			}
		}
	})
}
