package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func cubeComparisonConsumerChecks() []fixture.Step {
	return []fixture.Step{
		cubeComparisonIDs("SELECT id FROM cube_compare_data ORDER BY v,id", 4, 6, 7, 2, 3, 1, 5),
		cubeComparisonIDs("SELECT id FROM cube_compare_data ORDER BY v DESC,id", 5, 1, 2, 3, 6, 7, 4),
		cubeComparisonIDs("SELECT id FROM cube_compare_data ORDER BY v ASC NULLS FIRST,id", 5, 4, 6, 7, 2, 3, 1),
		cubeComparisonIDs("SELECT id FROM cube_compare_data ORDER BY v DESC NULLS LAST,id", 1, 2, 3, 6, 7, 4, 5),
		cubeComparisonIDs("SELECT id FROM cube_compare_data WHERE v='(1)'::cube ORDER BY id", 2, 3),
		cubeComparisonIDs("SELECT id FROM cube_compare_data WHERE v IS NOT DISTINCT FROM NULL::cube ORDER BY id", 5),
		cubeComparisonIDs("SELECT id FROM cube_compare_data WHERE v IS DISTINCT FROM NULL::cube ORDER BY id", 1, 2, 3, 4, 6, 7),
		fixture.Select("SELECT a.id AS a,b.id AS b FROM cube_compare_data a JOIN cube_compare_data b ON a.v=b.v WHERE a.v='(1)'::cube ORDER BY a.id,b.id", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(2)}, {int64(2), int64(3)}, {int64(3), int64(2)}, {int64(3), int64(3)}}),
		// Boolean labels avoid choosing a textual signed-zero representative.
		fixture.Select("SELECT v='(-1)'::cube AS neg,v='(0)'::cube AS zero,v='(1)'::cube AS one,v='(2)'::cube AS two,v IS NULL AS missing,count(*) AS n FROM cube_compare_data GROUP BY v ORDER BY v", []string{"neg", "zero", "one", "two", "missing", "n"}, []string{"BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "INT8"}, [][]any{{true, false, false, false, false, int64(1)}, {false, true, false, false, false, int64(2)}, {false, false, true, false, false, int64(2)}, {false, false, false, true, false, int64(1)}, {nil, nil, nil, nil, true, int64(1)}}),
		fixture.Select("SELECT count(*) AS n FROM (SELECT DISTINCT v FROM cube_compare_data) d", []string{"n"}, []string{"INT8"}, [][]any{{int64(5)}}),
		fixture.Select("SELECT id,v IN ('(1)'::cube,NULL::cube) AS member,v NOT IN ('(1)'::cube,NULL::cube) AS nonmember,NOT(v='(1)'::cube) AS different,CASE v WHEN '(1)'::cube THEN true ELSE false END AS chosen FROM cube_compare_data ORDER BY id", []string{"id", "member", "nonmember", "different", "chosen"}, []string{"INT4", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{int64(1), nil, nil, true, false}, {int64(2), true, false, false, true}, {int64(3), true, false, false, true}, {int64(4), nil, nil, true, false}, {int64(5), nil, nil, nil, false}, {int64(6), nil, nil, true, false}, {int64(7), nil, nil, true, false}}),
		fixture.Select("SELECT NULL::cube IS NOT DISTINCT FROM NULL::cube AS same,'1'::cube IS DISTINCT FROM '[(1),(1)]'::cube AS different,NULL::cube IN ('1'::cube) AS member", []string{"same", "different", "member"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, false, nil}}),
	}
}

func cubeComparisonHeldConsumer(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	q := "SELECT id FROM cube_compare_data WHERE v=$1::cube ORDER BY v,id"
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
	for _, tc := range []struct {
		arg any
		ids []int64
	}{
		{"1", []int64{2, 3}}, {[]byte("(2)"), []int64{1}}, {nil, nil}, {[]byte("-0"), []int64{6, 7}}, {"[(1),(1)]", []int64{2, 3}},
	} {
		s := cubeComparisonIDs(q, tc.ids...)
		s.Args = []any{tc.arg}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
	}
}

func TestReferenceCubeComparisonConsumers(t *testing.T) {
	cubeComparisonScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		cubeInstall(t, p, b, c)
		cubeComparisonTable(t, p, b, c)
		for _, s := range cubeComparisonConsumerChecks() {
			t.Run(s.Query, func(t *testing.T) {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
			})
		}
		cubeComparisonHeldConsumer(t, p, b, c)
	})
}
