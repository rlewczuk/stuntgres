package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// The shared SQL source is committed before postgres_fdw opens its loopback
// transaction. The native registered reader also reads actual independent SQL.
func fdwAnalyzeDirectSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, r *fdwSQLReader, run func(fixture.Step)) {
	t.Helper()
	r.advanced = &fdwAdvancedState{}
	fdwLive(t, p, b, r, run)
	fdwSourceExec(t, p, b, r, run, "CREATE TABLE fdw_source.direct(a integer,b integer,tag text)", 0)
	values := make([]string, 1000)
	for i := range values {
		tag := "common"
		if i >= 700 {
			tag = "rare"
		}
		values[i] = fmt.Sprintf("(%d,%d,'%s')", i%3, i%3, tag)
	}
	fdwSourceExec(t, p, b, r, run, "INSERT INTO fdw_source.direct VALUES"+strings.Join(values, ","), 1000)
	run(fixture.Exec("CREATE FOREIGN TABLE fdw_direct(a integer,b integer,tag text) SERVER fdw_live OPTIONS(schema_name 'fdw_source',table_name 'direct')", 0))
	run(fixture.Exec("CREATE STATISTICS fdw_direct_joint(ndistinct,dependencies,mcv) ON a,b FROM fdw_direct", 0))
	run(fixture.Exec("CREATE STATISTICS fdw_direct_expression ON (a+1) FROM fdw_direct", 0))
}

const fdwAnalyzeDirectData = " FROM pg_statistic_ext_data d JOIN pg_statistic_ext s ON s.oid=d.stxoid WHERE s.stxname='fdw_direct_joint'"
const fdwAnalyzeDirectExpressionData = " FROM pg_statistic_ext_data d JOIN pg_statistic_ext s ON s.oid=d.stxoid WHERE s.stxname='fdw_direct_expression'"

func fdwAnalyzeDirectCommand(run func(fixture.Step), prepared bool, columns string) {
	step := fixture.Exec("ANALYZE fdw_direct"+columns, 0)
	step.Prepared = prepared
	run(step)
}

func fdwAnalyzeDirectJoint(run func(fixture.Step), groups int) {
	run(fixture.Select("SELECT d.stxdinherit,d.stxdndistinct::text AS distincts,d.stxdmcv IS NOT NULL AS mcv"+fdwAnalyzeDirectData+" ORDER BY d.stxdinherit", []string{"stxdinherit", "distincts", "mcv"}, []string{"BOOL", "TEXT", "BOOL"}, [][]any{{false, fmt.Sprintf(`{"1, 2": %d}`, groups), true}}))
}

func fdwAnalyzeDirectExpression(run func(fixture.Step), distinct int, values string) {
	run(fixture.Select("SELECT d.stxdinherit,(stxdexpr[1]).stanullfrac,(stxdexpr[1]).stadistinct,(stxdexpr[1]).stakind1,array_length((stxdexpr[1]).stavalues1,1) AS values_count"+fdwAnalyzeDirectExpressionData+" ORDER BY d.stxdinherit", []string{"stxdinherit", "stanullfrac", "stadistinct", "stakind1", "values_count"}, []string{"BOOL", "FLOAT4", "FLOAT4", "INT2", "INT4"}, [][]any{{false, float64(0), float64(distinct), int64(1), int64(distinct)}}))
	if values != "" {
		run(fixture.Select("SELECT (stxdexpr[1]).stavalues1::text AS values,(stxdexpr[1]).stanumbers1::text AS frequencies"+fdwAnalyzeDirectExpressionData, []string{"values", "frequencies"}, []string{"TEXT", "TEXT"}, [][]any{{values, "{1}"}}))
	}
}

func fdwAnalyzeDirectEstimate(t *testing.T, p *fixture.Pair, c *sql.Conn, rows int64) {
	t.Helper()
	fdwExplainRoutes(t, p, c, "EXPLAIN SELECT * FROM fdw_direct WHERE tag='rare'", func(lines []string) {
		if got := fdwEstimate(t, lines); got != rows {
			t.Fatalf("direct scalar rare estimate=%d want=%d: %v", got, rows, lines)
		}
	})
}

func TestReferenceFDWAnalyzeDirectExtended(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fdwRowLockModeName(prepared), func(t *testing.T) {
			fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
				fdwAnalyzeDirectSetup(t, p, b, r, run)
				// Describe/Prepare does not acquire a sample or build definitions.
				statement, err := c.PrepareContext(p.Ctx, "ANALYZE fdw_direct")
				if err != nil {
					t.Fatal(err)
				}
				statement.Close()
				fdwAnalyzeRelationPopulation(run, "fdw_direct", -1)
				run(fixture.Select("SELECT count(*)"+fdwAnalyzeDirectData, []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
				fdwAnalyzeDirectCommand(run, prepared, "")
				fdwAnalyzeRelationPopulation(run, "fdw_direct", 1000)
				fdwAnalyzeDirectJoint(run, 3)
				run(fixture.Select("SELECT stxddependencies::text AS dependencies"+fdwAnalyzeDirectData, []string{"dependencies"}, []string{"TEXT"}, [][]any{{`{"1 => 2": 1.000000, "2 => 1": 1.000000}`}}))
				// Inspect genuine typed MCV values and masses, not just presence or
				// equality between backends. Marginals equal the joint masses here.
				run(fixture.Select("SELECT m.values::text AS values,m.nulls::text AS nulls,m.frequency,abs(m.base_frequency-m.frequency*m.frequency)<0.000000000001 AS independent_mass FROM pg_statistic_ext_data d JOIN pg_statistic_ext s ON s.oid=d.stxoid CROSS JOIN LATERAL pg_mcv_list_items(d.stxdmcv) m WHERE s.stxname='fdw_direct_joint' AND NOT d.stxdinherit ORDER BY m.values::text", []string{"values", "nulls", "frequency", "independent_mass"}, []string{"TEXT", "TEXT", "FLOAT8", "BOOL"}, [][]any{{"{0,0}", "{f,f}", float64(0.334), true}, {"{1,1}", "{f,f}", float64(0.333), true}, {"{2,2}", "{f,f}", float64(0.333), true}}))
				fdwAnalyzeDirectExpression(run, 3, "")
				fdwAnalyzeDirectEstimate(t, p, c, 300)
				query := fixture.Select("SELECT a,b,count(*) FROM fdw_direct GROUP BY a,b ORDER BY a,b", []string{"a", "b", "count"}, []string{"INT4", "INT4", "INT8"}, [][]any{{int64(0), int64(0), int64(334)}, {int64(1), int64(1), int64(333)}, {int64(2), int64(2), int64(333)}})
				query.Prepared = prepared
				run(query)
				// Keep a public result alive across later publication and empty input.
				held, err := b.DB.QueryContext(p.Ctx, "SELECT stxdndistinct::text"+fdwAnalyzeDirectData)
				if err != nil {
					t.Fatal(err)
				}
				defer held.Close()
				fdwSourceExec(t, p, b, r, run, "UPDATE fdw_source.direct SET b=b+10 WHERE tag='rare'", 300)
				fdwSourceExec(t, p, b, r, run, "UPDATE fdw_source.direct SET tag='rare'", 1000)
				fdwAnalyzeDirectJoint(run, 3)
				fdwAnalyzeDirectEstimate(t, p, c, 300)
				fdwAnalyzeDirectCommand(run, prepared, "")
				fdwAnalyzeDirectJoint(run, 6)
				fdwAnalyzeDirectEstimate(t, p, c, 1000)
				run(fixture.Select("SELECT a,b,count(*) FROM fdw_direct GROUP BY a,b ORDER BY a,b", []string{"a", "b", "count"}, []string{"INT4", "INT4", "INT8"}, [][]any{{int64(0), int64(0), int64(234)}, {int64(0), int64(10), int64(100)}, {int64(1), int64(1), int64(233)}, {int64(1), int64(11), int64(100)}, {int64(2), int64(2), int64(233)}, {int64(2), int64(12), int64(100)}}))
				fdwSourceExec(t, p, b, r, run, "DELETE FROM fdw_source.direct", 1000)
				fdwAnalyzeDirectCommand(run, prepared, "")
				fdwAnalyzeRelationPopulation(run, "fdw_direct", 0)
				fdwAnalyzeDirectJoint(run, 6)
				fdwAnalyzeDirectExpression(run, 3, "")
				run(fixture.Select("SELECT a,b,tag FROM fdw_direct ORDER BY a,b", []string{"a", "b", "tag"}, []string{"INT4", "INT4", "TEXT"}, nil))
				var old string
				if !held.Next() {
					t.Fatal("saved direct statistics result disappeared", held.Err())
				}
				if err := held.Scan(&old); err != nil || old != `{"1, 2": 3}` {
					t.Fatal("saved direct statistics result changed", old, err)
				}
				if held.Next() || held.Err() != nil {
					t.Fatal("unexpected saved direct statistics rows", held.Err())
				}
			})
		})
	}
}
