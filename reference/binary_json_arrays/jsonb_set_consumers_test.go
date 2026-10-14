package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func jsonbSetStored(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) fixture.Step {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
	for _, q := range []string{
		`CREATE TABLE jbs_store(id int,src jsonb,d jsonb DEFAULT jsonb_set('{"a":1}','{a}','2'),g jsonb GENERATED ALWAYS AS (jsonb_set(src,'{a}','9')) STORED,h jsonb GENERATED ALWAYS AS (jsonb_set_lax(src,'{b}',NULL,true,'use_json_null')) STORED,CHECK(jsonb_set(src,'{a}','9')=g))`,
		`CREATE VIEW jbs_view AS SELECT id,jsonb_set_lax(jsonb_in => src,path => '{a}',replacement => NULL,null_value_treatment => 'delete_key') AS changed FROM jbs_store`,
		`CREATE VIEW jbs_variadic AS SELECT id,pg_catalog.jsonb_set(src,'{a}',VARIADIC '9'::jsonb) AS changed FROM jbs_store`,
		`CREATE INDEX jbs_idx ON jbs_store ((jsonb_set(src,'{a}','9')))`,
	} {
		run(fixture.Exec(q, 0))
	}
	run(fixture.Exec(`INSERT INTO jbs_store(id,src) VALUES(1,'{"a":1}'),(2,'{"a":2}'),(3,NULL)`, 3))
	check := fixture.Select("SELECT id,src,d,g,h FROM jbs_store ORDER BY id", []string{"id", "src", "d", "g", "h"}, []string{"INT4", "JSONB", "JSONB", "JSONB", "JSONB"}, [][]any{
		{int64(1), `{"a": 1}`, `{"a": 2}`, `{"a": 9}`, `{"a": 1, "b": null}`},
		{int64(2), `{"a": 2}`, `{"a": 2}`, `{"a": 9}`, `{"a": 2, "b": null}`},
		{int64(3), nil, `{"a": 2}`, nil, nil},
	})
	jsonbSetBoth(t, p, b, c, check)
	jsonbSetBoth(t, p, b, c, fixture.Select("SELECT id,changed FROM jbs_view ORDER BY id", []string{"id", "changed"}, []string{"INT4", "JSONB"}, [][]any{{int64(1), `{}`}, {int64(2), `{}`}, {int64(3), nil}}))
	jsonbSetBoth(t, p, b, c, fixture.Select("SELECT id,changed FROM jbs_variadic ORDER BY id", []string{"id", "changed"}, []string{"INT4", "JSONB"}, [][]any{{int64(1), `{"a": 9}`}, {int64(2), `{"a": 9}`}, {int64(3), nil}}))
	jsonbSetBoth(t, p, b, c, fixture.Select(`SELECT id FROM jbs_store WHERE jsonb_set(src,'{a}','9')='{"a":9}' ORDER BY id`, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
	return check
}

func jsonbSetAtomic(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
	run(fixture.Exec("CREATE TABLE jbs_atomic(id int,src jsonb)", 0))
	run(fixture.Exec(`INSERT INTO jbs_atomic VALUES(1,'{"a":1}'),(2,'[1,2]')`, 2))
	run(fixture.Exec("CREATE INDEX jbs_atomic_idx ON jbs_atomic ((jsonb_set(src,'{0}','9')))", 0))
	check := fixture.Select("SELECT id,src FROM jbs_atomic ORDER BY id", []string{"id", "src"}, []string{"INT4", "JSONB"}, [][]any{{int64(1), `{"a": 1}`}, {int64(2), `[1, 2]`}})
	q := "UPDATE jbs_atomic SET src=jsonb_set(src,$1,$2) RETURNING src AS changed"
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
	fixture.RunStep(t, p.Ctx, held, b.Recorder, jsonbSetError(q, fixture.ControlsReferenceDiagnostic("22P02", `path element at position 1 is not an integer: "a"`), "{a}", "9"))
	run(check)
	jsonbSetBoth(t, p, b, c, fixture.Select(`SELECT id FROM jbs_atomic WHERE jsonb_set(src,'{0}','9')='[9,2]' ORDER BY id`, []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}}))
	s := fixture.Select(q, []string{"changed"}, []string{"JSONB"}, [][]any{{`{"0": 9, "a": 1}`}, {`[9, 2]`}}, "{0}", "9")
	s.Bag = true
	fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
}

func TestReferenceJSONBSetConsumers(t *testing.T) {
	jsonbSetScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		check := jsonbSetStored(t, p, b, c)
		s := fixture.Select("SELECT jsonb_set(src,'{a}','9') AS changed,count(*) AS n FROM jbs_store GROUP BY jsonb_set(src,'{a}','9')", []string{"changed", "n"}, []string{"JSONB", "INT8"}, [][]any{{`{"a": 9}`, int64(2)}, {nil, int64(1)}})
		s.Bag = true
		jsonbSetBoth(t, p, b, c, s)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(fixture.Select("UPDATE jbs_store SET src=$1 WHERE id=1 RETURNING src,g,h", []string{"src", "g", "h"}, []string{"JSONB", "JSONB", "JSONB"}, [][]any{{`{"a": 5}`, `{"a": 9}`, `{"a": 5, "b": null}`}}, `{"a":5}`)))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`UPDATE jbs_store SET src='{"a":1}' WHERE id=1`, 1))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, check)
		jsonbSetAtomic(t, p, b, c)
	})
}
