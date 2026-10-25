package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceAggregateFinalModes(t *testing.T) {
	for _, tc := range []struct{ name, mode, flag string }{{"read", "read_only", "r"}, {"share", "shareable", "s"}, {"write", "read_write", "w"}} {
		t.Run(tc.name, func(t *testing.T) {
			aggregateFinalScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				aggregateFinalSetup(t, p, b, c)
				createAggregateRun(t, p, b, c,
					fixture.Exec("CREATE FUNCTION fa_double(integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1*2$$", 0),
					fixture.Exec("CREATE AGGREGATE fa_"+tc.name+"(integer) (SFUNC=pg_catalog.int4pl,STYPE=int4,INITCOND='0',FINALFUNC=fa_final,FINALFUNC_MODIFY="+tc.mode+")", 0),
					fixture.Exec("CREATE AGGREGATE fa_"+tc.name+"_double(integer) (SFUNC=pg_catalog.int4pl,STYPE=int4,INITCOND='0',FINALFUNC=fa_double,FINALFUNC_MODIFY="+tc.mode+")", 0))
				text := "fa_" + tc.name + "(x)"
				double := "fa_" + tc.name + "_double(x)"
				for _, projection := range []bool{false, true} {
					q := "SELECT " + text + " AS v," + double + " AS d," + text + " AS again FROM fa_input"
					columns, types := []string{"v", "d", "again"}, []string{"TEXT", "INT4", "TEXT"}
					values := [][]any{{"total=6", int64(12), "total=6"}}
					if projection {
						q = "SELECT " + double + " AS d," + text + " AS v," + double + " AS again FROM fa_input"
						columns, types = []string{"d", "v", "again"}, []string{"INT4", "TEXT", "INT4"}
						values = [][]any{{int64(12), "total=6", int64(12)}}
					}
					createAggregateBoth(t, p, b, c, fixture.Select(q, columns, types, values))
				}
				createAggregateBoth(t, p, b, c, fixture.Select("SELECT g,"+text+" AS v,"+double+" AS d FROM fa_input GROUP BY g ORDER BY g", []string{"g", "v", "d"}, []string{"INT4", "TEXT", "INT4"}, [][]any{{int64(1), "total=3", int64(6)}, {int64(2), "total=3", int64(6)}}))
				q := "SELECT " + text + " AS v," + double + " AS d FROM fa_input WHERE id<=$1"
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				for _, value := range []struct {
					limit, d int64
					text     string
				}{{3, 12, "total=6"}, {0, 0, "total=0"}, {1, 2, "total=1"}, {3, 12, "total=6"}} {
					fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"v", "d"}, []string{"TEXT", "INT4"}, [][]any{{value.text, value.d}}, value.limit))
				}
				createAggregateBoth(t, p, b, c, fixture.Select("SELECT a.aggfinalmodify::text AS modify FROM (SELECT oid FROM pg_proc WHERE proname='fa_"+tc.name+"') p JOIN pg_aggregate a ON a.aggfnoid=p.oid", []string{"modify"}, []string{"TEXT"}, [][]any{{tc.flag}}))
				if tc.flag != "r" {
					q := "SELECT fa_" + tc.name + "(x) OVER() AS v FROM fa_input"
					for _, prepared := range []bool{false, true} {
						s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "0A000", Message: "aggregate function fa_" + tc.name + "(integer) does not support use as a window function"})
						s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
						fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					}
					createAggregateRun(t, p, b, c,
						fixture.Exec("CREATE TABLE fa_window_log(v integer)", 0),
						fixture.Exec("CREATE FUNCTION fa_window_final(integer) RETURNS text BEGIN ATOMIC INSERT INTO fa_window_log VALUES($1); SELECT $1::text; END", 0),
						fixture.Exec("CREATE AGGREGATE fa_window_guard(integer) (SFUNC=pg_catalog.int4pl,STYPE=int4,INITCOND='0',FINALFUNC=fa_window_final,FINALFUNC_MODIFY="+tc.mode+")", 0))
					const windowSQL = "SELECT fa_window_guard(x) OVER() AS v FROM fa_input WHERE id<=$1"
					windowStatement, err := c.PrepareContext(p.Ctx, windowSQL)
					if err != nil {
						t.Fatal(err)
					}
					defer windowStatement.Close()
					windowHeld := fixture.HeldStatement{Executor: c, Statement: windowStatement, SQL: windowSQL}
					for _, limit := range []int64{3, 0, 3} {
						s := fixture.ImplicitError(windowSQL, fixture.Diagnostic{Code: "0A000", Message: "aggregate function fa_window_guard(integer) does not support use as a window function"}, limit)
						s.SelectRows, s.ErrorStage = true, "execute"
						createAggregateBoth(t, p, b, c, s)
						fixture.RunStep(t, p.Ctx, windowHeld, b.Recorder, s)
					}
					createAggregateRun(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM fa_window_log", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
					createAggregateRun(t, p, b, c, aggregateFinalText("SELECT fa_window_guard(x) AS v FROM fa_input", "6"))
					createAggregateRun(t, p, b, c, fixture.Select("SELECT v FROM fa_window_log", []string{"v"}, []string{"INT4"}, [][]any{{int64(6)}}))
				}
			})
		})
	}
}
