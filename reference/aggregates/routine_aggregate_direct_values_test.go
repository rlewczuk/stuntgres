package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRoutineAggregateDirectValues(t *testing.T) {
	aggregateDirectScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		aggregateDirectSetup(t, p, b, c)
		aggregateDirectExec(t, p, b, c,
			"CREATE FUNCTION ad_c() RETURNS integer BEGIN ATOMIC SELECT ad_a(v) FROM ad_input; END",
			"CREATE AGGREGATE ad_strict(integer)(SFUNC=pg_catalog.int4pl,STYPE=integer)",
			"CREATE AGGREGATE ad_seed(integer)(SFUNC=pg_catalog.int4pl,STYPE=integer,INITCOND='-7',FINALFUNC=pg_catalog.abs)",
			"CREATE FUNCTION ad_strict_c() RETURNS integer BEGIN ATOMIC SELECT ad_strict(v) FROM ad_input; END",
			"CREATE FUNCTION ad_seed_c() RETURNS integer BEGIN ATOMIC SELECT ad_seed(v) FROM ad_input; END",
			"CREATE FUNCTION ad_i8_s(bigint,bigint) RETURNS bigint LANGUAGE SQL AS $$SELECT coalesce($1,0)+coalesce($2,0)$$",
			"CREATE AGGREGATE ad_i8(bigint)(SFUNC=ad_i8_s,STYPE=bigint,INITCOND='0')",
			"CREATE FUNCTION ad_i8_c() RETURNS bigint BEGIN ATOMIC SELECT ad_i8(v::bigint) FROM ad_input; END",
			"CREATE FUNCTION ad_num_s(numeric,numeric) RETURNS numeric LANGUAGE SQL AS $$SELECT coalesce($1,0)+coalesce($2,0)$$",
			"CREATE AGGREGATE ad_num(numeric)(SFUNC=ad_num_s,STYPE=numeric,INITCOND='0')",
			"CREATE FUNCTION ad_num_c() RETURNS numeric BEGIN ATOMIC SELECT ad_num(v::numeric) FROM ad_input; END",
			"CREATE FUNCTION ad_extra_s(integer,integer,text) RETURNS integer LANGUAGE SQL AS $$SELECT $1+coalesce($2,0)$$",
			"CREATE FUNCTION ad_extra_f(integer,integer,text) RETURNS text LANGUAGE SQL AS $$SELECT 'total='||$1::text||';nulls='||(($2 IS NULL) AND ($3 IS NULL))::text$$",
			"CREATE AGGREGATE ad_extra(integer,text)(SFUNC=ad_extra_s,STYPE=integer,INITCOND='0',FINALFUNC=ad_extra_f,FINALFUNC_EXTRA)",
			"CREATE FUNCTION ad_extra_c() RETURNS text BEGIN ATOMIC SELECT ad_extra(v,'x') FROM ad_input; END",
			"CREATE TABLE ad_calls(v integer)",
			"CREATE FUNCTION ad_record(integer) RETURNS integer BEGIN ATOMIC INSERT INTO ad_calls VALUES($1); SELECT $1; END",
			"CREATE AGGREGATE ad_recorded(integer)(SFUNC=pg_catalog.int4pl,STYPE=integer,INITCOND='0',FINALFUNC=ad_record)",
			"CREATE FUNCTION ad_recorded_c() RETURNS integer BEGIN ATOMIC SELECT ad_recorded(v) FROM ad_input; END")
		names := []string{"ad_c", "ad_strict_c", "ad_seed_c", "ad_i8_c", "ad_num_c", "ad_extra_c"}
		types := []string{"INT4", "INT4", "INT4", "INT8", "NUMERIC", "TEXT"}
		held := make([]fixture.HeldStatement, len(names))
		for i, name := range names {
			held[i] = aggregateDirectHeld(t, p, c, "SELECT "+name+"() AS v")
		}
		recorded := aggregateDirectHeld(t, p, c, "SELECT ad_recorded_c() AS v")
		createAggregateRun(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM ad_calls", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		for phase, wants := range [][]any{
			{int64(30), int64(3), int64(4), int64(3), "3", "total=3;nulls=true"},
			{int64(0), nil, int64(7), int64(0), "0", "total=0;nulls=true"},
			{int64(0), nil, int64(7), int64(0), "0", "total=0;nulls=true"},
			{int64(30), int64(3), int64(4), int64(3), "3", "total=3;nulls=true"},
		} {
			if phase > 0 {
				count := int64(3)
				if phase == 2 {
					count = 2
				}
				if phase == 3 {
					count = 0
				}
				createAggregateRun(t, p, b, c, fixture.Exec("DELETE FROM ad_input", count))
				if phase == 1 {
					createAggregateRun(t, p, b, c, fixture.Exec("INSERT INTO ad_input VALUES(NULL),(NULL)", 2))
				}
				if phase == 3 {
					createAggregateRun(t, p, b, c, fixture.Exec("INSERT INTO ad_input VALUES(1),(2),(NULL)", 3))
				}
			}
			for i, name := range names {
				s := fixture.Select("SELECT "+name+"() AS v", []string{"v"}, []string{types[i]}, [][]any{{wants[i]}})
				createAggregateBoth(t, p, b, c, s)
				fixture.RunStep(t, p.Ctx, held[i], b.Recorder, s)
			}
		}
		fixture.RunStep(t, p.Ctx, recorded, b.Recorder, createAggregateValue(recorded.SQL, int64(3)))
		createAggregateRun(t, p, b, c, fixture.Select("SELECT v FROM ad_calls", []string{"v"}, []string{"INT4"}, [][]any{{int64(3)}}))
	})
}
