package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func jsonbConcatDeleteStored(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) fixture.Step {
	t.Helper()
	run := func(s fixture.Step) { jsonTypeRun(t, p, b, c, s) }
	for _, q := range []string{
		`CREATE TABLE jbcd_source(id int,doc jsonb,rhs jsonb,key text,idx int,changed jsonb GENERATED ALWAYS AS (((doc || rhs) - key) - idx) STORED)`,
		`CREATE TABLE jbcd_default(id int,v jsonb DEFAULT (('{"a":1}'::jsonb || '{"b":2}') - 'a'))`,
		`CREATE TABLE jbcd_check(doc jsonb,CONSTRAINT jbcd_empty CHECK((doc - 'a') = '{}'::jsonb))`,
		`CREATE INDEX jbcd_idx ON jbcd_source (((doc || rhs) - key))`,
		`CREATE VIEW jbcd_view AS SELECT id,((doc || rhs) - key) - idx AS changed FROM jbcd_source`,
	} {
		run(fixture.Exec(q, 0))
	}
	run(fixture.Exec(`INSERT INTO jbcd_source(id,doc,rhs,key,idx) VALUES(0,'[9]','[]','missing',99),(1,'["x",1]','[2,"x"]','x',-1),(2,NULL,'[]','x',0)`, 3))
	run(fixture.Exec("INSERT INTO jbcd_default(id) VALUES(1)", 1))
	run(fixture.Exec(`INSERT INTO jbcd_check VALUES('{"a":1}')`, 1))
	check := fixture.Select("SELECT id,doc,rhs,key,idx,changed FROM jbcd_source ORDER BY id", []string{"id", "doc", "rhs", "key", "idx", "changed"}, []string{"INT4", "JSONB", "JSONB", "TEXT", "INT4", "JSONB"}, [][]any{
		{int64(0), `[9]`, `[]`, "missing", int64(99), `[9]`},
		{int64(1), `["x", 1]`, `[2, "x"]`, "x", int64(-1), `[1]`},
		{int64(2), nil, `[]`, "x", int64(0), nil},
	})
	jsonTypeBoth(t, p, b, c, check)
	jsonTypeBoth(t, p, b, c, fixture.Select("SELECT id,changed FROM jbcd_view ORDER BY id", []string{"id", "changed"}, []string{"INT4", "JSONB"}, [][]any{{int64(0), `[9]`}, {int64(1), `[1]`}, {int64(2), nil}}))
	jsonTypeBoth(t, p, b, c, fixture.Select("SELECT pg_get_viewdef('jbcd_view'::regclass) AS v", []string{"v"}, []string{"TEXT"}, [][]any{{" SELECT id,\n    (((doc || rhs) - key) - idx) AS changed\n   FROM jbcd_source;"}}))
	jsonTypeBoth(t, p, b, c, jsonbConcatDeleteSelect("SELECT v FROM jbcd_default", `{"b": 2}`))
	bad := fixture.ErrorDiagnostic(`INSERT INTO jbcd_source(id,doc,rhs,key,idx) VALUES(3,'[1,2]','[]','missing',0),(4,'{}','{}','missing',0)`, fixture.ControlsReferenceDiagnostic("22023", "cannot delete from object using integer index"))
	jsonTypeBoth(t, p, b, c, bad)
	run(check)
	jsonbConcatDeleteFresh(t, p, b, c)
	run(fixture.Exec("DELETE FROM jbcd_source WHERE id=3", 1))
	return check
}

func jsonbConcatDeleteFresh(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor) {
	t.Helper()
	jsonTypeRun(t, p, b, c, fixture.Exec(`INSERT INTO jbcd_source(id,doc,rhs,key,idx) VALUES(3,'[1,2]','3','missing',-1)`, 1))
	for _, relation := range []string{"jbcd_source", "jbcd_view"} {
		jsonTypeBoth(t, p, b, c, jsonbConcatDeleteSelect("SELECT changed AS v FROM "+relation+" WHERE id=3", `[1, 2]`))
	}
}

func TestReferenceJSONBConcatDeleteStoredControls(t *testing.T) {
	jsonbConcatDeleteScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		check := jsonbConcatDeleteStored(t, p, b, c)
		jsonbConcatDeleteControls(t, p, b, c, check)
	})
}
