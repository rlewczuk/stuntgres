package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func aggregateCombineSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	createAggregateRun(t, p, b, c,
		fixture.Exec("CREATE FUNCTION ac_square_step(bigint,integer) RETURNS bigint LANGUAGE SQL AS $$SELECT CASE WHEN $2 IS NULL THEN $1 ELSE coalesce($1,0)+$2::bigint*$2::bigint END$$", 0),
		fixture.Exec("CREATE AGGREGATE ac_squares(integer) (SFUNC=ac_square_step,STYPE=bigint,COMBINEFUNC=pg_catalog.int8pl,PARALLEL=SAFE)", 0),
		fixture.Exec("CREATE TABLE ac_input(id integer,g integer,x integer)", 0),
		fixture.Exec("INSERT INTO ac_input VALUES(1,1,NULL),(2,1,1),(3,1,2),(4,2,3),(5,2,4),(6,NULL,NULL)", 6))
}

func aggregateCombineValue(q string, value any, args ...any) fixture.Step {
	return fixture.Select(q, []string{"v"}, []string{"INT8"}, [][]any{{value}}, args...)
}

func aggregateCombineHeld(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	const q = "SELECT ac_squares(x) AS v FROM ac_input WHERE id<=$1"
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
	for _, ceiling := range []int{0, 1, 2, 7} {
		createAggregateRun(t, p, b, c, fixture.Exec(fmt.Sprintf("SET max_parallel_workers_per_gather=%d", ceiling), 0))
		for _, tc := range []struct {
			limit int
			value any
		}{{0, nil}, {3, int64(5)}, {5, int64(30)}, {6, int64(30)}, {0, nil}} {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, aggregateCombineValue(q, tc.value, tc.limit))
		}
	}
}

func TestReferenceAggregateCombineValues(t *testing.T) {
	aggregateCombineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		aggregateCombineSetup(t, p, b, c)
		for _, ceiling := range []int{0, 2, 7} {
			createAggregateRun(t, p, b, c, fixture.Exec(fmt.Sprintf("SET max_parallel_workers_per_gather=%d", ceiling), 0))
			for _, tc := range []struct {
				q     string
				value any
			}{
				{"SELECT ac_squares(x) AS v FROM ac_input", int64(30)},
				{"SELECT ac_squares(x) AS v FROM ac_input WHERE FALSE", nil},
				{"SELECT ac_squares(x) AS v FROM ac_input WHERE x IS NULL", nil},
				{"SELECT ac_squares(x) FILTER(WHERE x>=3) AS v FROM ac_input", int64(25)},
				{"SELECT ac_squares(x ORDER BY id DESC) AS v FROM ac_input", int64(30)},
			} {
				createAggregateBoth(t, p, b, c, aggregateCombineValue(tc.q, tc.value))
			}
			createAggregateBoth(t, p, b, c, fixture.Select("SELECT g,ac_squares(x) AS v FROM ac_input GROUP BY g ORDER BY g NULLS LAST", []string{"g", "v"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(5)}, {int64(2), int64(25)}, {nil, nil}}))
			createAggregateBoth(t, p, b, c, fixture.Select("SELECT g,ac_squares(x) AS v FROM ac_input WHERE FALSE GROUP BY g", []string{"g", "v"}, []string{"INT4", "INT8"}, nil))
		}
		createAggregateBoth(t, p, b, c, aggregateCombineValue("SELECT ac_squares(DISTINCT x) AS v FROM(VALUES(1),(1),(2)) s(x)", int64(5)))
		createAggregateBoth(t, p, b, c, aggregateCombineValue("SELECT ac_squares(x) AS v FROM(VALUES(1),(1),(2)) s(x)", int64(6)))
		aggregateCombineHeld(t, p, b, c)
	})
}

func TestReferenceAggregateCombineMetadata(t *testing.T) {
	aggregateCombineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		aggregateCombineSetup(t, p, b, c)
		for _, tc := range []struct{ name, option string }{{"ac_default", ""}, {"ac_restricted", ",PARALLEL=RESTRICTED"}, {"ac_unsafe", ",PARALLEL=UNSAFE"}} {
			createAggregateRun(t, p, b, c, fixture.Exec("CREATE AGGREGATE "+tc.name+"(integer) (SFUNC=ac_square_step,STYPE=bigint,COMBINEFUNC=pg_catalog.int8pl"+tc.option+")", 0))
		}
		createAggregateBoth(t, p, b, c, fixture.Select("SELECT p.proname,p.prokind::text AS kind,p.prorettype=20::oid AS result_type,p.proparallel::text AS parallel,a.aggtranstype=20::oid AS state_type,a.aggcombinefn=463::oid AS combine,a.aggserialfn=0::oid AS no_serial,a.aggdeserialfn=0::oid AS no_deserial,a.aggfinalfn=0::oid AS no_final FROM (SELECT * FROM pg_proc WHERE proname IN('ac_squares','ac_default','ac_restricted','ac_unsafe')) p JOIN pg_aggregate a ON a.aggfnoid=p.oid ORDER BY p.proname",
			[]string{"proname", "kind", "result_type", "parallel", "state_type", "combine", "no_serial", "no_deserial", "no_final"}, []string{"NAME", "TEXT", "BOOL", "TEXT", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL"},
			[][]any{{"ac_default", "a", true, "u", true, true, true, true, true}, {"ac_restricted", "a", true, "r", true, true, true, true, true}, {"ac_squares", "a", true, "s", true, true, true, true, true}, {"ac_unsafe", "a", true, "u", true, true, true, true, true}}))
		for _, name := range []string{"ac_default", "ac_restricted", "ac_unsafe"} {
			createAggregateBoth(t, p, b, c, aggregateCombineValue("SELECT "+name+"(x) AS v FROM ac_input", int64(30)))
		}
	})
}

func TestReferenceAggregateCombineSerialization(t *testing.T) {
	aggregateCombineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		createAggregateRun(t, p, b, c,
			fixture.Exec("CREATE TABLE ac_strings(id integer,g integer,v text,d text,b bytea)", 0),
			fixture.Exec("INSERT INTO ac_strings VALUES(1,1,'x',',','\\x0080ff'),(2,1,'x',',','\\x0080ff'),(3,2,'x',',','\\x0080ff'),(4,2,'x',',','\\x0080ff'),(5,NULL,NULL,',',NULL)", 5))
		for _, ceiling := range []int{0, 2, 7} {
			createAggregateRun(t, p, b, c, fixture.Exec(fmt.Sprintf("SET max_parallel_workers_per_gather=%d", ceiling), 0))
			createAggregateBoth(t, p, b, c, fixture.Select("SELECT string_agg(v,d) AS v FROM ac_strings", []string{"v"}, []string{"TEXT"}, [][]any{{"x,x,x,x"}}))
			createAggregateBoth(t, p, b, c, fixture.Select("SELECT string_agg(b,'\\x2c'::bytea) AS v FROM ac_strings", []string{"v"}, []string{"BYTEA"}, [][]any{{"\x00\x80\xff,\x00\x80\xff,\x00\x80\xff,\x00\x80\xff"}}))
			createAggregateBoth(t, p, b, c, fixture.Select("SELECT g,string_agg(v,d) AS v FROM ac_strings GROUP BY g ORDER BY g NULLS LAST", []string{"g", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "x,x"}, {int64(2), "x,x"}, {nil, nil}}))
			createAggregateBoth(t, p, b, c, fixture.Select("SELECT string_agg(v,d) AS v FROM ac_strings WHERE FALSE", []string{"v"}, []string{"TEXT"}, [][]any{{nil}}))
		}
		for _, tc := range []struct{ q, value string }{
			{"SELECT string_agg(v,d ORDER BY id) AS v FROM(VALUES(1,'a'::text,','::text),(2,'b',';')) s(id,v,d)", "a;b"},
			{"SELECT string_agg(v,d ORDER BY id) AS v FROM(VALUES(1,''::text,'é'::text),(2,'b',NULL)) s(id,v,d)", "b"},
			{"SELECT string_agg(v,d) AS v FROM(VALUES(''::text,','::text)) s(v,d)", ""},
		} {
			createAggregateBoth(t, p, b, c, fixture.Select(tc.q, []string{"v"}, []string{"TEXT"}, [][]any{{tc.value}}))
		}
	})
}
