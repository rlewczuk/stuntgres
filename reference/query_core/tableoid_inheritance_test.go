package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceQueryTableOIDInheritance(t *testing.T) {
	routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		routineExec(t, p, b, c,
			"CREATE TABLE toi_parent(a INT)",
			"CREATE TABLE toi_prefix(z TEXT)",
			"CREATE TABLE toi_child() INHERITS(toi_prefix,toi_parent)",
			"CREATE TABLE toi_sibling() INHERITS(toi_parent)",
			"CREATE TABLE toi_diamond() INHERITS(toi_child,toi_sibling)",
			"CREATE TABLE toi_partition(a INT) PARTITION BY RANGE(a)",
			"CREATE TABLE toi_leaf PARTITION OF toi_partition FOR VALUES FROM(1) TO(10)")
		for _, s := range []fixture.Step{
			fixture.Exec("INSERT INTO toi_parent VALUES(1)", 1),
			fixture.Exec("INSERT INTO toi_child(z,a) VALUES('nonprefix',2)", 1),
			fixture.Exec("INSERT INTO toi_diamond(z,a) VALUES('diamond',3),('duplicate',3)", 2),
			fixture.Exec("INSERT INTO toi_partition VALUES(4)", 1),
		} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		parent := tableoidCell(b, tableoidIdentity(t, p, c, "toi_parent"))
		child := tableoidCell(b, tableoidIdentity(t, p, c, "toi_child"))
		diamond := tableoidCell(b, tableoidIdentity(t, p, c, "toi_diamond"))
		leaf := tableoidCell(b, tableoidIdentity(t, p, c, "toi_leaf"))
		for _, s := range []fixture.Step{
			fixture.Select("SELECT a,tableoid FROM toi_parent ORDER BY a", []string{"a", "tableoid"}, []string{"INT4", "OID"}, [][]any{{int64(1), parent}, {int64(2), child}, {int64(3), diamond}, {int64(3), diamond}}),
			fixture.Select("SELECT a,tableoid FROM ONLY toi_parent", []string{"a", "tableoid"}, []string{"INT4", "OID"}, [][]any{{int64(1), parent}}),
			fixture.Select("SELECT a,tableoid FROM toi_partition", []string{"a", "tableoid"}, []string{"INT4", "OID"}, [][]any{{int64(4), leaf}}),
			fixture.Select("SELECT a,tableoid FROM ONLY toi_partition", []string{"a", "tableoid"}, []string{"INT4", "OID"}, nil),
			fixture.Select("SELECT x.tableoid AS x,y.tableoid AS y FROM toi_parent x JOIN toi_parent y ON x.a=1 AND y.a=2", []string{"x", "y"}, []string{"OID", "OID"}, [][]any{{parent, child}}),
			fixture.Select("SELECT tableoid,min(a) AS first,count(*) AS n FROM toi_parent GROUP BY tableoid ORDER BY first", []string{"tableoid", "first", "n"}, []string{"OID", "INT4", "INT8"}, [][]any{{parent, int64(1), int64(1)}, {child, int64(2), int64(1)}, {diamond, int64(3), int64(2)}}),
			fixture.Select("SELECT a,tableoid,row_number() OVER(ORDER BY a) AS n FROM toi_parent WHERE a<3 ORDER BY a DESC LIMIT 1", []string{"a", "tableoid", "n"}, []string{"INT4", "OID", "INT8"}, [][]any{{int64(2), child, int64(2)}}),
		} {
			tableoidRoutes(t, p, b, c, s)
		}
	})
}
