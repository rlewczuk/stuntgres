package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceRoutineAggregateDirectMoving(t *testing.T) {
	aggregateDirectScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		movingSetup(t, p, b, c)
		aggregateDirectExec(t, p, b, c,
			"CREATE FUNCTION ma_direct_step(integer,integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1+$2$$",
			"CREATE FUNCTION ma_direct_final(integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1*10$$",
			"CREATE FUNCTION ma_direct_forward(integer,integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1+$2$$",
			"CREATE FUNCTION ma_direct_inverse(integer,integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1-$2$$",
			"CREATE FUNCTION ma_direct_finish(integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1*10$$",
			"CREATE AGGREGATE ma_direct(integer)(SFUNC=ma_direct_step,STYPE=integer,INITCOND='0',FINALFUNC=ma_direct_final,MSFUNC=ma_direct_forward,MINVFUNC=ma_direct_inverse,MSTYPE=integer,MINITCOND='0',MFINALFUNC=ma_direct_finish,MFINALFUNC_MODIFY=read_only)",
			"CREATE FUNCTION ma_direct_c() RETURNS integer BEGIN ATOMIC SELECT v FROM (SELECT id,ma_direct(x) OVER(ORDER BY id ROWS 1 PRECEDING) AS v FROM ma_input) s WHERE id=4; END",
			"CREATE FUNCTION ma_direct_mixed() RETURNS integer BEGIN ATOMIC SELECT v+ma_direct_forward(1,2)+ma_direct_inverse(5,2)+ma_direct_finish(1) FROM (SELECT id,ma_direct(x) OVER(ORDER BY id ROWS 1 PRECEDING) AS v FROM ma_input) s WHERE id=4; END",
			"CREATE FUNCTION ma_direct_ordinary() RETURNS integer BEGIN ATOMIC SELECT v FROM (SELECT id,ma_direct(x) OVER(ORDER BY id ROWS UNBOUNDED PRECEDING) AS v FROM ma_input) s WHERE id=4; END")
		bag := aggregateDirectBag([]string{"ma_direct", "ma_direct_step", "ma_direct_final", "ma_direct_forward", "ma_direct_inverse", "ma_direct_finish", "ma_direct_c", "ma_direct_mixed", "ma_direct_ordinary"},
			[2]string{"ma_direct", "ma_direct_step"}, [2]string{"ma_direct", "ma_direct_final"},
			[2]string{"ma_direct", "ma_direct_forward"}, [2]string{"ma_direct", "ma_direct_inverse"}, [2]string{"ma_direct", "ma_direct_finish"},
			[2]string{"ma_direct_c", "ma_direct"}, [2]string{"ma_direct_ordinary", "ma_direct"},
			[2]string{"ma_direct_mixed", "ma_direct"}, [2]string{"ma_direct_mixed", "ma_direct_forward"},
			[2]string{"ma_direct_mixed", "ma_direct_inverse"}, [2]string{"ma_direct_mixed", "ma_direct_finish"})
		const query = "SELECT ma_direct_c() AS moving,ma_direct_mixed() AS mixed,ma_direct_ordinary() AS ordinary"
		values := fixture.Select(query, []string{"moving", "mixed", "ordinary"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(120), int64(136), int64(170)}})
		check := func() {
			createAggregateBoth(t, p, b, c, bag)
			createAggregateBoth(t, p, b, c, values)
			held := aggregateDirectHeld(t, p, c, query)
			for range 2 {
				fixture.RunStep(t, p.Ctx, held, b.Recorder, values)
			}
		}
		check()
		if b.PG {
			return
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if dump.Len() == 0 {
			t.Fatal("empty moving direct-dependency dump")
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		for range 2 {
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String())); err != nil {
				t.Fatal(err)
			}
			check()
		}
	})
}
