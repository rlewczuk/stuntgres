package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCountExpressionCallers(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, step := range []fixture.Step{
		fixture.Exec("CREATE TABLE t(k TEXT,v INT)", 0),
		fixture.Exec("INSERT INTO t VALUES('a',1),('a',NULL),(NULL,3)", 3),
		fixture.Exec("CREATE TABLE empty(k TEXT,v INT)", 0),
		fixture.Exec("CREATE TABLE out(k TEXT,n BIGINT)", 0),
		fixture.Exec("CREATE TABLE narrow(n SMALLINT)", 0),
		fixture.Exec("CREATE SEQUENCE s", 0),
		fixture.Exec("CREATE VIEW counted(n,z,r) AS SELECT count(v),count(NULLIF(v,1)),count(CAST('s' AS regclass)) FROM t", 0),
	} {
		p.Step(t, step)
	}
	for _, q := range []string{`SELECT pg_catalog.count(t.v) FROM t`, `SELECT "count"(v) FROM t`, `SELECT count(l.v) FROM t AS l JOIN t AS r ON l.k=r.k`, `SELECT count(v) FROM t LIMIT 1 OFFSET 0`} {
		for _, prepared := range []bool{false, true} {
			step := fixture.Select(q, []string{"count"}, []string{"INT8"}, [][]any{{int64(2)}})
			step.Prepared = prepared
			p.Step(t, step)
		}
	}
	for _, step := range []fixture.Step{
		fixture.Exec("INSERT INTO out SELECT k,count(v) FROM t GROUP BY k", 2),
		fixture.Exec("INSERT INTO out(n) SELECT count(v) FROM empty", 1),
		fixture.Exec("INSERT INTO out(n) SELECT count(v) FROM empty GROUP BY k", 0),
		fixture.Exec("INSERT INTO narrow SELECT count(v) FROM t", 1),
	} {
		p.Step(t, step)
	}
	step := fixture.Select("SELECT k,n FROM out", []string{"k", "n"}, []string{"TEXT", "INT8"}, [][]any{{"a", int64(1)}, {nil, int64(1)}, {nil, int64(0)}})
	step.Bag = true
	p.Step(t, step)
	p.Step(t, fixture.ImplicitError("INSERT INTO narrow SELECT count(1/(v-3)) FROM t", fixture.Diagnostic{Code: "22012", Message: "division by zero"}))
	p.Step(t, fixture.Select("SELECT n FROM narrow", []string{"n"}, []string{"INT2"}, [][]any{{int64(2)}}))
	p.Step(t, fixture.Select("SELECT count(nextval('s')),count(nextval('s')) FROM t", []string{"count", "count"}, []string{"INT8", "INT8"}, [][]any{{int64(3), int64(3)}}))
	p.Step(t, fixture.Select("SELECT currval('s')", []string{"currval"}, []string{"INT8"}, [][]any{{int64(6)}}))
	check := func() {
		p.Step(t, fixture.Select("SELECT n,z FROM counted", []string{"n", "z"}, []string{"INT8", "INT8"}, [][]any{{int64(2), int64(1)}}))
		p.Step(t, fixture.Select("SELECT n,z,r FROM counted", []string{"n", "z", "r"}, []string{"INT8", "INT8", "INT8"}, [][]any{{int64(2), int64(1), int64(3)}}))
	}
	check()
	p.Step(t, fixture.Exec("ALTER TABLE t RENAME COLUMN v TO renamed", 0))
	p.Step(t, fixture.Exec("ALTER TABLE t RENAME TO renamed_table", 0))
	check()
	for _, step := range []fixture.Step{
		fixture.Exec("INSERT INTO renamed_table VALUES('b',NULL)", 1),
		fixture.Select("SELECT count(renamed) FROM renamed_table", []string{"count"}, []string{"INT8"}, [][]any{{int64(2)}}),
		fixture.Exec("INSERT INTO renamed_table VALUES('b',4)", 1),
		fixture.Select("SELECT count(renamed) FROM renamed_table", []string{"count"}, []string{"INT8"}, [][]any{{int64(3)}}),
	} {
		step.Prepared = true
		p.Step(t, step)
	}
}
