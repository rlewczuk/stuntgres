package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func jsonbObjectStored(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) fixture.Step {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
	for _, q := range []string{
		`CREATE TABLE jbo_stored(id int,a text[],k text[],v text[],d jsonb DEFAULT jsonb_object('[-2:-1]={d,0}'::text[]),e jsonb DEFAULT jsonb_object('[5:5]={e}'::text[],'[-1:-1]={0}'::text[]),g jsonb GENERATED ALWAYS AS (jsonb_object(a)) STORED,h jsonb GENERATED ALWAYS AS (jsonb_object(k,v)) VIRTUAL,CHECK(jsonb_object(a)::text <> '{"blocked": "yes"}'))`,
		`CREATE INDEX jbo_stored_idx ON jbo_stored ((jsonb_object(k,v)))`,
		`CREATE VIEW jbo_view AS SELECT id,jsonb_object(a) AS one,jsonb_object(k,v) AS two FROM jbo_stored`,
	} {
		run(fixture.Exec(q, 0))
	}
	run(fixture.Exec("INSERT INTO jbo_stored(id,a,k,v) VALUES(1,'{a,1}','{a}','{1}')", 1))
	run(fixture.Prepared(fixture.Exec("INSERT INTO jbo_stored(id,a,k,v) VALUES($1,$2,$3,$4)", 1, 2, "{a,1}", "{a}", "{1}")))
	check := fixture.Select("SELECT id,one,two FROM jbo_view ORDER BY id", []string{"id", "one", "two"}, []string{"INT4", "JSONB", "JSONB"}, [][]any{{int64(1), `{"a": "1"}`, `{"a": "1"}`}, {int64(2), `{"a": "1"}`, `{"a": "1"}`}})
	jsonObjectBoth(t, p, b, c, check)
	run(fixture.Select("SELECT d,e,g,h FROM jbo_stored WHERE id=1", []string{"d", "e", "g", "h"}, []string{"JSONB", "JSONB", "JSONB", "JSONB"}, [][]any{{`{"d": "0"}`, `{"e": "0"}`, `{"a": "1"}`, `{"a": "1"}`}}))
	return check
}

func TestReferenceJSONBObjectConsumers(t *testing.T) {
	jsonbObjectScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		check := jsonbObjectStored(t, p, b, c)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, q := range []string{
			"CREATE MATERIALIZED VIEW jbo_mat AS SELECT id,jsonb_object(a) AS doc FROM jbo_stored",
			"CREATE TABLE jbo_ctas AS SELECT id,jsonb_object(k,v) AS doc FROM jbo_stored",
		} {
			run(fixture.Exec(q, 2))
		}
		for _, table := range []string{"jbo_mat", "jbo_ctas"} {
			jsonObjectBoth(t, p, b, c, fixture.Select("SELECT id,doc FROM "+table+" ORDER BY id", []string{"id", "doc"}, []string{"INT4", "JSONB"}, [][]any{{int64(1), `{"a": "1"}`}, {int64(2), `{"a": "1"}`}}))
		}
		jsonObjectBoth(t, p, b, c, fixture.Select("SELECT jsonb_object(a) AS doc,count(*) AS n FROM jbo_stored GROUP BY jsonb_object(a)", []string{"doc", "n"}, []string{"JSONB", "INT8"}, [][]any{{`{"a": "1"}`, int64(2)}}))
		run(fixture.Exec("CREATE TABLE jbo_group(id int,a text[],k text[],v text[])", 0))
		run(fixture.Exec("INSERT INTO jbo_group VALUES(1,'{b,2,a,old,a,1}','{b,a,a}','{2,old,1}'),(2,'{a,1,b,2}','{a,b}','{1,2}')", 2))
		for _, expr := range []string{"jsonb_object(a)", "jsonb_object(k,v)"} {
			jsonObjectBoth(t, p, b, c, fixture.Select(
				"SELECT "+expr+" AS doc,count(*) AS n FROM jbo_group GROUP BY "+expr,
				[]string{"doc", "n"}, []string{"JSONB", "INT8"}, [][]any{{`{"a": "1", "b": "2"}`, int64(2)}}))
		}
		jsonObjectBoth(t, p, b, c, fixture.Select("SELECT min(jsonb_object(k,v)::text) AS doc FROM jbo_stored", []string{"doc"}, []string{"TEXT"}, [][]any{{`{"a": "1"}`}}))
		run(fixture.Select("UPDATE jbo_stored SET a='{b,2}',k='{b}',v='{2}' WHERE id=1 RETURNING g,h", []string{"g", "h"}, []string{"JSONB", "JSONB"}, [][]any{{`{"b": "2"}`, `{"b": "2"}`}}))
		check.Rows = [][]any{{int64(1), `{"b": "2"}`, `{"b": "2"}`}, {int64(2), `{"a": "1"}`, `{"a": "1"}`}}
		run(check)
		for _, q := range []string{
			"INSERT INTO jbo_stored(id,a,k,v) VALUES(3,'{ok,3}','{ok}','{3}'),(4,'{bad,x,y}','{bad}','{4}')",
			"UPDATE jbo_stored SET a=CASE WHEN id=1 THEN '{ok,3}'::text[] ELSE '{bad,x,y}'::text[] END",
		} {
			s := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("2202E", "array must have even number of elements"))
			s.ErrorStage = "execute"
			jsonObjectBoth(t, p, b, c, s)
			run(check)
		}
		s := fixture.ErrorDiagnostic("INSERT INTO jbo_stored(id,a,k,v) VALUES(3,'{ok,3}','{ok}','{3}'),(4,'{NULL,x}','{bad}','{4}')", fixture.ControlsReferenceDiagnostic("22004", "null value not allowed for object key"))
		jsonObjectBoth(t, p, b, c, s)
		run(check)
		tx, err := c.BeginTx(p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		trun := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, tx, b.Recorder, s) }
		trun(fixture.Exec("SAVEPOINT jbo_save", 0))
		trun(fixture.ErrorDiagnostic("INSERT INTO jbo_stored(id,a) VALUES(5,'{odd}')", fixture.ControlsReferenceDiagnostic("2202E", "array must have even number of elements")))
		trun(fixture.Exec("ROLLBACK TO SAVEPOINT jbo_save", 0))
		trun(fixture.Exec("INSERT INTO jbo_stored(id,a,k,v) VALUES(5,'{c,3}','{c}','{3}')", 1))
		trun(jsonbObjectSelect("SELECT jsonb_object(a) AS v FROM jbo_stored WHERE id=5", `{"c": "3"}`))
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		jsonObjectBoth(t, p, b, c, jsonbObjectSelect("SELECT g AS v FROM jbo_stored WHERE id=5", `{"c": "3"}`))
	})
}

// Each catalog surface independently asserts PostgreSQL's public formatting.
func TestReferenceJSONBObjectDeparse(t *testing.T) {
	jsonbObjectScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		jsonbObjectStored(t, p, b, c)
		for _, tc := range []struct {
			name string
			step fixture.Step
		}{
			{"defaults_generated", fixture.Select(
				`SELECT d.adnum,pg_get_expr(d.adbin,d.adrelid,false) AS expression FROM pg_catalog.pg_attrdef d JOIN pg_catalog.pg_class r ON d.adrelid=r.oid WHERE r.relname='jbo_stored' ORDER BY d.adnum`,
				[]string{"adnum", "expression"}, []string{"INT2", "TEXT"}, [][]any{
					{int64(5), `jsonb_object('[-2:-1]={d,0}'::text[])`},
					{int64(6), `jsonb_object('[5:5]={e}'::text[], '[-1:-1]={0}'::text[])`},
					{int64(7), `jsonb_object(a)`},
					{int64(8), `jsonb_object(k, v)`},
				})},
			{"check", fixture.Select(
				`SELECT pg_get_expr(c.conbin,c.conrelid,false) AS expression FROM pg_catalog.pg_constraint c JOIN pg_catalog.pg_class r ON c.conrelid=r.oid WHERE r.relname='jbo_stored' AND c.contype='c'`,
				[]string{"expression"}, []string{"TEXT"}, [][]any{{`((jsonb_object(a))::text <> '{"blocked": "yes"}'::text)`}})},
			{"view", fixture.Select(
				`SELECT pg_get_viewdef('jbo_view'::regclass,false) AS definition`,
				[]string{"definition"}, []string{"TEXT"}, [][]any{{" SELECT id,\n    jsonb_object(a) AS one,\n    jsonb_object(k, v) AS two\n   FROM jbo_stored;"}})},
			{"index", fixture.Select(
				`SELECT pg_get_indexdef('jbo_stored_idx'::regclass,0,false) AS definition`,
				[]string{"definition"}, []string{"TEXT"}, [][]any{{`CREATE INDEX jbo_stored_idx ON public.jbo_stored USING btree (jsonb_object(k, v))`}})},
		} {
			t.Run(tc.name, func(t *testing.T) { jsonObjectBoth(t, p, b, c, tc.step) })
		}
	})
}
