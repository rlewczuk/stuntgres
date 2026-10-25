package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceQueryTableOIDFrames(t *testing.T) {
	routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		routineExec(t, p, b, c, "CREATE TABLE toi_left(a INT)", "CREATE TABLE toi_right(a INT)", "CREATE TABLE toi_null(a INT)")
		for _, s := range []fixture.Step{fixture.Exec("INSERT INTO toi_left VALUES(1),(2)", 2), fixture.Exec("INSERT INTO toi_right VALUES(2),(3)", 2), fixture.Exec("INSERT INTO toi_null VALUES(NULL)", 1)} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		left := tableoidCell(b, tableoidIdentity(t, p, c, "toi_left"))
		right := tableoidCell(b, tableoidIdentity(t, p, c, "toi_right"))
		null := tableoidCell(b, tableoidIdentity(t, p, c, "toi_null"))
		cases := []fixture.Step{
			fixture.Select("SELECT l.tableoid AS l,r.tableoid AS r FROM toi_left l JOIN toi_right r ON l.a=r.a", []string{"l", "r"}, []string{"OID", "OID"}, [][]any{{left, right}}),
			fixture.Select("SELECT l.tableoid AS l,r.tableoid AS r FROM toi_left l LEFT JOIN toi_right r ON l.a=r.a ORDER BY l.a", []string{"l", "r"}, []string{"OID", "OID"}, [][]any{{left, nil}, {left, right}}),
			fixture.Select("SELECT l.tableoid AS l,r.tableoid AS r FROM toi_left l RIGHT JOIN toi_right r ON l.a=r.a ORDER BY r.a", []string{"l", "r"}, []string{"OID", "OID"}, [][]any{{left, right}, {nil, right}}),
			fixture.Select("SELECT l.tableoid AS l,r.tableoid AS r FROM toi_left l FULL JOIN toi_right r ON l.a=r.a ORDER BY COALESCE(l.a,r.a)", []string{"l", "r"}, []string{"OID", "OID"}, [][]any{{left, nil}, {left, right}, {nil, right}}),
			fixture.Select("SELECT n.tableoid FROM toi_null n", []string{"tableoid"}, []string{"OID"}, [][]any{{null}}),
			fixture.Select("SELECT l.tableoid AS l,n.tableoid AS n FROM toi_left l LEFT JOIN toi_null n ON FALSE ORDER BY l.a", []string{"l", "n"}, []string{"OID", "OID"}, [][]any{{left, nil}, {left, nil}}),
			fixture.Select("SELECT l.tableoid AS l,r.tableoid AS r FROM toi_left l JOIN toi_left r ON l.a=1 AND r.a=2", []string{"l", "r"}, []string{"OID", "OID"}, [][]any{{left, left}}),
			fixture.Select("SELECT l.tableoid AS l,r.tableoid AS r FROM toi_left l LEFT JOIN toi_left r ON FALSE ORDER BY l.a", []string{"l", "r"}, []string{"OID", "OID"}, [][]any{{left, nil}, {left, nil}}),
			fixture.Select("SELECT (SELECT l.tableoid FROM toi_right r WHERE r.a=3) AS outer_oid,(SELECT tableoid FROM toi_right r WHERE r.a=3) AS inner_oid FROM toi_left l WHERE l.a=1", []string{"outer_oid", "inner_oid"}, []string{"OID", "OID"}, [][]any{{left, right}}),
			fixture.Select("SELECT (SELECT tableoid FROM toi_right l WHERE l.a=3) AS nearest FROM toi_left l WHERE l.a=1", []string{"nearest"}, []string{"OID"}, [][]any{{right}}),
			fixture.Select("SELECT l.tableoid,count(*) AS n FROM toi_left l GROUP BY l.tableoid", []string{"tableoid", "n"}, []string{"OID", "INT8"}, [][]any{{left, int64(2)}}),
			fixture.Select("SELECT l.tableoid,row_number() OVER(ORDER BY a) AS n FROM toi_left l ORDER BY a DESC LIMIT 1 OFFSET 1", []string{"tableoid", "n"}, []string{"OID", "INT8"}, [][]any{{left, int64(1)}}),
			fixture.Select("SELECT DISTINCT l.tableoid FROM toi_left l", []string{"tableoid"}, []string{"OID"}, [][]any{{left}}),
			fixture.Select("SELECT (SELECT count(l.tableoid) FROM toi_right r WHERE r.a=3) AS n FROM toi_left l", []string{"n"}, []string{"INT8"}, [][]any{{int64(2)}}),
			fixture.Select("SELECT (SELECT count(r.tableoid) FROM toi_right r) AS n FROM toi_left l ORDER BY l.a", []string{"n"}, []string{"INT8"}, [][]any{{int64(2)}, {int64(2)}}),
		}
		for _, s := range cases {
			t.Run(s.Query, func(t *testing.T) { tableoidRoutes(t, p, b, c, s) })
		}
	})
}
