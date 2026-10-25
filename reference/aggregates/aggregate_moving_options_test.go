package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func movingBooleanOptions(t *testing.T) {
	createAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for i, tc := range []struct {
			value string
			want  bool
		}{
			{"", true}, {"=true", true}, {"=false", false}, {"=on", true}, {"=off", false}, {"=1", true}, {"=0", false}, {"='TRUE'", true}, {"='False'", false},
		} {
			name := fmt.Sprintf("ma_bool_%d", i)
			lifecycleExec(t, p, b, c, "CREATE AGGREGATE "+name+"(integer) (SFUNC=pg_catalog.int4pl,STYPE=int4,MFINALFUNC_EXTRA"+tc.value+")")
			createAggregateBoth(t, p, b, c, fixture.Select("SELECT a.aggmfinalextra FROM (SELECT oid FROM pg_proc WHERE proname='"+name+"') p JOIN pg_aggregate a ON a.aggfnoid=p.oid", []string{"aggmfinalextra"}, []string{"BOOL"}, [][]any{{tc.want}}))
		}
	})
}

func movingWholeInput(t *testing.T) {
	createAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		const q = "CREATE AGGREGATE ma_rejected(integer) (SFUNC=pg_catalog.int4pl,STYPE=int4,INITCOND='0',MSFUNC=pg_catalog.int4pl,MINVFUNC=pg_catalog.int4mi,MSTYPE=int4,MINITCOND='0') nonsense"
		for _, prepared := range []bool{false, true} {
			s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42601", Message: "syntax error at or near \"nonsense\"", Position: fixture.Position(strings.Index(q, "nonsense") + 1)})
			s.Prepared = prepared
			s.ErrorStage = "execute"
			if prepared {
				s.ErrorStage = "prepare"
			}
			createAggregateRun(t, p, b, c, s)
			movingNoResidue(t, p, b, c)
		}
		lifecycleExec(t, p, b, c, "CREATE AGGREGATE ma_rejected(integer) (SFUNC=pg_catalog.int4pl,STYPE=int4,INITCOND='0',MSFUNC=pg_catalog.int4pl,MINVFUNC=pg_catalog.int4mi,MSTYPE=int4,MINITCOND='0')")
		createAggregateBoth(t, p, b, c, createAggregateValue("SELECT ma_rejected(x) AS v FROM(VALUES(2),(3)) s(x)", int64(5)))
	})
}

func movingForcedSelection(t *testing.T) {
	for _, mode := range []string{"shareable", "read_write"} {
		t.Run(mode, func(t *testing.T) {
			createAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				movingLogSetup(t, p, b, c, false)
				lifecycleExec(t, p, b, c, "CREATE AGGREGATE ma_forced(integer) (SFUNC=ma_regular,STYPE=int4,INITCOND='0',FINALFUNC_MODIFY="+mode+",MSFUNC=ma_forward,MINVFUNC=ma_inverse,MSTYPE=int4,MINITCOND='0')")
				for _, prepared := range []bool{false, true} {
					s := movingRows("SELECT id,ma_forced(x) OVER() AS v FROM ma_input ORDER BY id", int64(17), int64(17), int64(17), int64(17))
					s.Prepared = prepared
					createAggregateRun(t, p, b, c, s)
					movingLog(t, p, b, c, [][]any{{"f", int64(2)}, {"f", int64(3)}, {"f", int64(5)}, {"f", int64(7)}})
				}
			})
		})
	}
}
