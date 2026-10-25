package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func movingLogSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, punt bool) {
	t.Helper()
	movingSetup(t, p, b, c)
	inverse := "$1-$2"
	if punt {
		inverse = "CASE WHEN $2=2 THEN NULL::integer ELSE $1-$2 END"
	}
	lifecycleExec(t, p, b, c,
		"CREATE TABLE ma_log(kind text,x integer)",
		"CREATE FUNCTION ma_forward(integer,integer) RETURNS integer BEGIN ATOMIC INSERT INTO ma_log VALUES('f',$2); SELECT $1+$2; END",
		"CREATE FUNCTION ma_inverse(integer,integer) RETURNS integer BEGIN ATOMIC INSERT INTO ma_log VALUES('i',$2); SELECT "+inverse+"; END",
		"CREATE FUNCTION ma_regular(integer,integer) RETURNS integer BEGIN ATOMIC INSERT INTO ma_log VALUES('r',$2); SELECT $1+$2; END",
		"CREATE AGGREGATE ma_logged(integer) (SFUNC=ma_regular,STYPE=int4,INITCOND='0',MSFUNC=ma_forward,MINVFUNC=ma_inverse,MSTYPE=int4,MINITCOND='0')")
}

func movingLog(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, want [][]any) {
	t.Helper()
	s := fixture.Select("SELECT kind,x FROM ma_log", []string{"kind", "x"}, []string{"TEXT", "INT4"}, want)
	s.Bag = true
	createAggregateRun(t, p, b, c, s, fixture.Exec("DELETE FROM ma_log", int64(len(want))))
}

func TestReferenceAggregateMovingReplay(t *testing.T) {
	t.Run("multiple_removal", movingMultipleRemoval)
	createAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		movingLogSetup(t, p, b, c, true)
		lifecycleExec(t, p, b, c,
			"CREATE FUNCTION ma_other_forward(integer,integer) RETURNS integer BEGIN ATOMIC INSERT INTO ma_log VALUES('F',$2); SELECT $1+$2; END",
			"CREATE FUNCTION ma_other_inverse(integer,integer) RETURNS integer BEGIN ATOMIC INSERT INTO ma_log VALUES('I',$2); SELECT $1-$2; END",
			"CREATE AGGREGATE ma_other(integer) (SFUNC=pg_catalog.int4pl,STYPE=int4,INITCOND='0',MSFUNC=ma_other_forward,MINVFUNC=ma_other_inverse,MSTYPE=int4,MINITCOND='0')")
		const q = "SELECT id,ma_logged(x) OVER w AS v,ma_other(x) OVER w AS sibling FROM ma_input WINDOW w AS(ORDER BY id ROWS 1 PRECEDING) ORDER BY id"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		step := fixture.Select(q, []string{"id", "v", "sibling"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(2), int64(2)}, {int64(2), int64(5), int64(5)}, {int64(3), int64(8), int64(8)}, {int64(4), int64(12), int64(12)}})
		for route := range 3 {
			if route == 2 {
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, step)
			} else {
				s := step
				s.Prepared = route == 1
				createAggregateRun(t, p, b, c, s)
			}
			movingLog(t, p, b, c, [][]any{{"f", int64(2)}, {"f", int64(3)}, {"i", int64(2)}, {"f", int64(3)}, {"f", int64(5)}, {"i", int64(3)}, {"f", int64(7)}, {"F", int64(2)}, {"F", int64(3)}, {"F", int64(5)}, {"F", int64(7)}, {"I", int64(2)}, {"I", int64(3)}})
		}
		// Disjoint peer frames reset without attempting an inverse.
		createAggregateRun(t, p, b, c, movingRows("SELECT id,ma_logged(x) OVER(ORDER BY id/2 GROUPS CURRENT ROW) AS v FROM ma_input ORDER BY id", int64(2), int64(8), int64(8), int64(7)))
		movingLog(t, p, b, c, [][]any{{"f", int64(2)}, {"f", int64(3)}, {"f", int64(5)}, {"f", int64(7)}})
		for _, tc := range []struct {
			frame  string
			values []any
			log    [][]any
		}{
			{"ROWS CURRENT ROW", []any{int64(2), int64(3), int64(5), int64(7)}, [][]any{{"f", int64(2)}, {"f", int64(3)}, {"f", int64(5)}, {"f", int64(7)}}},
			{"ROWS BETWEEN 1 PRECEDING AND CURRENT ROW EXCLUDE CURRENT ROW", []any{int64(0), int64(2), int64(3), int64(5)}, [][]any{{"f", int64(2)}, {"f", int64(3)}, {"f", int64(5)}}},
		} {
			createAggregateRun(t, p, b, c, movingRows("SELECT id,ma_logged(x) OVER(ORDER BY id "+tc.frame+") AS v FROM ma_input ORDER BY id", tc.values...))
			movingLog(t, p, b, c, tc.log)
		}
	})
}

func TestReferenceAggregateMovingNulls(t *testing.T) {
	createAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		movingSetup(t, p, b, c)
		lifecycleExec(t, p, b, c,
			"CREATE AGGREGATE ma_strict(integer) (SFUNC=pg_catalog.int4pl,STYPE=int4,MSFUNC=pg_catalog.int4pl,MINVFUNC=pg_catalog.int4mi,MSTYPE=int4)",
			"CREATE FUNCTION ma_null(integer,integer) RETURNS integer LANGUAGE SQL AS $$SELECT NULL::integer$$",
			"CREATE AGGREGATE ma_null_forward(integer) (SFUNC=ma_null,STYPE=int4,INITCOND='0',MSFUNC=ma_null,MINVFUNC=ma_null,MSTYPE=int4,MINITCOND='0')")
		createAggregateRun(t, p, b, c, fixture.Exec("UPDATE ma_input SET x=NULL WHERE id=2", 1))
		createAggregateBoth(t, p, b, c, movingRows("SELECT id,ma_strict(x) OVER(ORDER BY id ROWS 1 PRECEDING) AS v FROM ma_input ORDER BY id", int64(2), int64(2), int64(5), int64(12)))
		createAggregateBoth(t, p, b, c, movingRows("SELECT id,ma_strict(x) OVER(ORDER BY id ROWS BETWEEN 1 PRECEDING AND 2 PRECEDING) AS v FROM ma_input ORDER BY id", nil, nil, nil, nil))
		createAggregateBoth(t, p, b, c, movingRows("SELECT id,ma_strict(NULL::integer) OVER(ORDER BY id ROWS 1 PRECEDING) AS v FROM ma_input ORDER BY id", nil, nil, nil, nil))
		createAggregateBoth(t, p, b, c, createAggregateValue("SELECT ma_null_forward(x) AS v FROM ma_input", nil))
		s := fixture.ImplicitError("SELECT ma_null_forward(x) OVER(ORDER BY id ROWS 1 PRECEDING) AS v FROM ma_input ORDER BY v", fixture.Diagnostic{Code: "22004", Message: "moving-aggregate transition function must not return null"})
		s.SelectRows, s.ErrorStage = true, "execute"
		createAggregateBoth(t, p, b, c, s)
	})
}

func TestReferenceAggregateMovingFinals(t *testing.T) {
	t.Run("empty_final", movingEmptyFinal)
	createAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		movingSetup(t, p, b, c)
		lifecycleExec(t, p, b, c,
			"CREATE FUNCTION ma_text_add(text,integer) RETURNS text LANGUAGE SQL AS $$SELECT ($1::integer+$2)::text$$",
			"CREATE FUNCTION ma_text_sub(text,integer) RETURNS text LANGUAGE SQL AS $$SELECT ($1::integer-$2)::text$$",
			"CREATE FUNCTION ma_int_final(text) RETURNS integer LANGUAGE SQL AS $$SELECT $1::integer$$",
			"CREATE AGGREGATE ma_text_state(integer) (SFUNC=pg_catalog.int4pl,STYPE=int4,INITCOND='0',MSFUNC=ma_text_add,MINVFUNC=ma_text_sub,MSTYPE=text,MINITCOND='0',MFINALFUNC=ma_int_final)",
			"CREATE FUNCTION ma_multi_add(text,integer,text) RETURNS text LANGUAGE SQL AS $$SELECT ($1::integer+$2)::text$$",
			"CREATE FUNCTION ma_multi_sub(text,integer,text) RETURNS text LANGUAGE SQL AS $$SELECT ($1::integer-$2)::text$$",
			"CREATE FUNCTION ma_extra(text,integer,text) RETURNS text LANGUAGE SQL AS $$SELECT 'total='||$1||';null='||($2 IS NULL AND $3 IS NULL)::text$$",
			"CREATE AGGREGATE ma_multi(integer,text) (SFUNC=ma_multi_add,STYPE=text,INITCOND='0',MSFUNC=ma_multi_add,MINVFUNC=ma_multi_sub,MSTYPE=text,MINITCOND='0',MFINALFUNC=ma_extra,MFINALFUNC_EXTRA)",
			"CREATE FUNCTION ma_zero_add(integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1+1$$",
			"CREATE FUNCTION ma_zero_sub(integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1-1$$",
			"CREATE AGGREGATE ma_zero(*) (SFUNC=ma_zero_add,STYPE=int4,INITCOND='0',MSFUNC=ma_zero_add,MINVFUNC=ma_zero_sub,MSTYPE=int4,MINITCOND='0')",
			"CREATE FUNCTION ma_null_final(integer) RETURNS integer LANGUAGE SQL AS $$SELECT NULL::integer$$",
			"CREATE AGGREGATE ma_final_null(integer) (SFUNC=pg_catalog.int4pl,STYPE=int4,MSFUNC=pg_catalog.int4pl,MINVFUNC=pg_catalog.int4mi,MSTYPE=int4,MFINALFUNC=ma_null_final)",
			"CREATE AGGREGATE ma_final_strict(integer) (SFUNC=pg_catalog.int4pl,STYPE=int4,MSFUNC=pg_catalog.int4pl,MINVFUNC=pg_catalog.int4mi,MSTYPE=int4,MFINALFUNC=pg_catalog.abs)")
		s := movingRows("SELECT id,ma_text_state(x) OVER(ORDER BY id ROWS 1 PRECEDING) AS v FROM ma_input ORDER BY id", int64(2), int64(5), int64(8), int64(12))
		s.CheckMetadata = movingMetadata(b.PG)
		createAggregateBoth(t, p, b, c, s)
		text := fixture.Select("SELECT id,ma_multi(x,'label') OVER(ORDER BY id ROWS 1 PRECEDING) AS v FROM ma_input ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "total=2;null=true"}, {int64(2), "total=5;null=true"}, {int64(3), "total=8;null=true"}, {int64(4), "total=12;null=true"}})
		text.CheckMetadata = aggregateFinalMetadata(b.PG, true)
		createAggregateBoth(t, p, b, c, text)
		createAggregateBoth(t, p, b, c, movingRows("SELECT id,ma_zero(*) OVER(ORDER BY id ROWS 1 PRECEDING) AS v FROM ma_input ORDER BY id", int64(1), int64(2), int64(2), int64(2)))
		for _, name := range []string{"ma_final_null", "ma_final_strict"} {
			createAggregateBoth(t, p, b, c, movingRows("SELECT id,"+name+"(x) OVER(ORDER BY id ROWS BETWEEN 1 PRECEDING AND 2 PRECEDING) AS v FROM ma_input ORDER BY id", nil, nil, nil, nil))
		}
		lifecycleExec(t, p, b, c,
			"CREATE TABLE ma_finals(v integer)",
			"CREATE FUNCTION ma_record_final(integer) RETURNS integer BEGIN ATOMIC INSERT INTO ma_finals VALUES($1); SELECT $1; END",
			"CREATE AGGREGATE ma_record(integer) (SFUNC=pg_catalog.int4pl,STYPE=int4,INITCOND='0',MSFUNC=pg_catalog.int4pl,MINVFUNC=pg_catalog.int4mi,MSTYPE=int4,MINITCOND='0',MFINALFUNC=ma_record_final)")
		createAggregateRun(t, p, b, c, movingRows("SELECT id,ma_record(x) OVER(ORDER BY id/2 RANGE CURRENT ROW) AS v FROM ma_input ORDER BY id", int64(2), int64(8), int64(8), int64(7)))
		createAggregateRun(t, p, b, c, fixture.Select("SELECT v FROM ma_finals ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(7)}, {int64(8)}}))
	})
}

func TestReferenceAggregateMovingSelection(t *testing.T) {
	t.Run("forced_over", movingForcedSelection)
	for _, forced := range []bool{false, true} {
		name := "ordinary_read_only"
		if forced {
			name = "forced_safe"
		}
		t.Run(name, func(t *testing.T) {
			createAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				movingLogSetup(t, p, b, c, false)
				if forced {
					lifecycleExec(t, p, b, c, "DROP AGGREGATE ma_logged(integer)", "CREATE AGGREGATE ma_logged(integer) (SFUNC=ma_regular,STYPE=int4,INITCOND='0',FINALFUNC_MODIFY=read_write,MSFUNC=ma_forward,MINVFUNC=ma_inverse,MSTYPE=int4,MINITCOND='0')")
				}
				for _, tc := range []struct {
					name, arg, frame string
					bounded          bool
				}{
					{"bounded", "x", "ROWS 1 PRECEDING", true},
					{"unbounded", "x", "ROWS UNBOUNDED PRECEDING", false},
					{"volatile", "x+((random())*0)::integer", "ROWS 1 PRECEDING", false},
					{"subquery", "(SELECT x)", "ROWS 1 PRECEDING", false},
				} {
					t.Run(tc.name, func(t *testing.T) {
						q := "SELECT id,ma_logged(" + tc.arg + ") OVER(ORDER BY id " + tc.frame + ") AS v FROM ma_input ORDER BY id"
						vals := []any{int64(2), int64(5), int64(8), int64(12)}
						if tc.frame == "ROWS UNBOUNDED PRECEDING" {
							vals = []any{int64(2), int64(5), int64(10), int64(17)}
						}
						for _, prepared := range []bool{false, true} {
							s := movingRows(q, vals...)
							s.Prepared = prepared
							createAggregateRun(t, p, b, c, s)
							var log [][]any
							if tc.bounded || forced {
								log = [][]any{{"f", int64(2)}, {"f", int64(3)}, {"f", int64(5)}, {"f", int64(7)}}
								if tc.frame != "ROWS UNBOUNDED PRECEDING" {
									log = append(log, []any{"i", int64(2)}, []any{"i", int64(3)})
								}
							} else if tc.frame == "ROWS UNBOUNDED PRECEDING" {
								log = [][]any{{"r", int64(2)}, {"r", int64(3)}, {"r", int64(5)}, {"r", int64(7)}}
							} else {
								log = [][]any{{"r", int64(2)}, {"r", int64(3)}, {"r", int64(3)}, {"r", int64(5)}, {"r", int64(5)}, {"r", int64(7)}}
							}
							movingLog(t, p, b, c, log)
						}
					})
				}
			})
		})
	}
}

func TestReferenceAggregateMovingModes(t *testing.T) {
	t.Run("qualified_identity", movingWindowIdentity)
	for _, mode := range []string{"shareable", "read_write"} {
		t.Run(mode, func(t *testing.T) {
			createAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				movingLogSetup(t, p, b, c, false)
				lifecycleExec(t, p, b, c, "CREATE AGGREGATE ma_unsafe(integer) (SFUNC=ma_regular,STYPE=int4,INITCOND='0',MSFUNC=ma_forward,MINVFUNC=ma_inverse,MSTYPE=int4,MINITCOND='0',MFINALFUNC_MODIFY="+mode+")")
				createAggregateBoth(t, p, b, c, movingRows("SELECT id,ma_unsafe(x) OVER(ORDER BY id ROWS UNBOUNDED PRECEDING) AS v FROM ma_input ORDER BY id", int64(2), int64(5), int64(10), int64(17)))
				movingLog(t, p, b, c, [][]any{{"r", int64(2)}, {"r", int64(3)}, {"r", int64(5)}, {"r", int64(7)}, {"r", int64(2)}, {"r", int64(3)}, {"r", int64(5)}, {"r", int64(7)}})
				const q = "SELECT ma_unsafe(x) OVER(ORDER BY id ROWS 1 PRECEDING) AS v FROM ma_input WHERE id<=$1"
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				for _, limit := range []int64{4, 0} {
					s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "0A000", Message: "aggregate function ma_unsafe(integer) does not support use as a window function"}, limit)
					s.SelectRows, s.ErrorStage = true, "execute"
					createAggregateBoth(t, p, b, c, s)
					fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, s)
					movingLog(t, p, b, c, nil)
				}
			})
		})
	}
}
