package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func jsonObjectStored(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) fixture.Step {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
	for _, q := range []string{
		`CREATE TABLE jo_stored(id int,a text[],k text[],v text[],d json DEFAULT json_object('[-2:-1]={d,0}'::text[]),e json DEFAULT json_object('[5:5]={e}'::text[],'[-1:-1]={0}'::text[]),g json GENERATED ALWAYS AS (json_object(a)) STORED,h json GENERATED ALWAYS AS (json_object(k,v)) VIRTUAL,CHECK(json_object(a)::text <> '{"blocked" : "yes"}'))`,
		`CREATE INDEX jo_stored_idx ON jo_stored ((json_object(k,v)::text))`,
		`CREATE VIEW jo_view AS SELECT id,json_object(a) AS one,json_object(k,v) AS two FROM jo_stored`,
	} {
		run(fixture.Exec(q, 0))
	}
	run(fixture.Exec("INSERT INTO jo_stored(id,a,k,v) VALUES(1,'{a,1}','{a}','{1}')", 1))
	run(fixture.Prepared(fixture.Exec("INSERT INTO jo_stored(id,a,k,v) VALUES($1,$2,$3,$4)", 1, 2, "{a,1}", "{a}", "{1}")))
	check := fixture.Select("SELECT id,one,two FROM jo_view ORDER BY id", []string{"id", "one", "two"}, []string{"INT4", "JSON", "JSON"}, [][]any{{int64(1), `{"a" : "1"}`, `{"a" : "1"}`}, {int64(2), `{"a" : "1"}`, `{"a" : "1"}`}})
	jsonObjectBoth(t, p, b, c, check)
	run(fixture.Select("SELECT d,e,g,h FROM jo_stored WHERE id=1", []string{"d", "e", "g", "h"}, []string{"JSON", "JSON", "JSON", "JSON"}, [][]any{{`{"d" : "0"}`, `{"e" : "0"}`, `{"a" : "1"}`, `{"a" : "1"}`}}))
	return check
}

func TestReferenceJSONObjectConsumers(t *testing.T) {
	jsonObjectScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		check := jsonObjectStored(t, p, b, c)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, q := range []string{
			"CREATE MATERIALIZED VIEW jo_mat AS SELECT id,json_object(a) AS doc FROM jo_stored",
			"CREATE TABLE jo_ctas AS SELECT id,json_object(k,v) AS doc FROM jo_stored",
		} {
			run(fixture.Exec(q, 2))
		}
		for _, table := range []string{"jo_mat", "jo_ctas"} {
			jsonObjectBoth(t, p, b, c, fixture.Select("SELECT id,doc FROM "+table+" ORDER BY id", []string{"id", "doc"}, []string{"INT4", "JSON"}, [][]any{{int64(1), `{"a" : "1"}`}, {int64(2), `{"a" : "1"}`}}))
		}
		jsonObjectBoth(t, p, b, c, fixture.Select("SELECT json_object(a)::text AS doc,count(*) AS n FROM jo_stored GROUP BY json_object(a)::text", []string{"doc", "n"}, []string{"TEXT", "INT8"}, [][]any{{`{"a" : "1"}`, int64(2)}}))
		jsonObjectBoth(t, p, b, c, fixture.Select("SELECT min(json_object(k,v)::text) AS doc FROM jo_stored", []string{"doc"}, []string{"TEXT"}, [][]any{{`{"a" : "1"}`}}))
		run(fixture.Select("UPDATE jo_stored SET a='{b,2}',k='{b}',v='{2}' WHERE id=1 RETURNING g,h", []string{"g", "h"}, []string{"JSON", "JSON"}, [][]any{{`{"b" : "2"}`, `{"b" : "2"}`}}))
		check.Rows = [][]any{{int64(1), `{"b" : "2"}`, `{"b" : "2"}`}, {int64(2), `{"a" : "1"}`, `{"a" : "1"}`}}
		run(check)
		for _, q := range []string{
			"INSERT INTO jo_stored(id,a,k,v) VALUES(3,'{ok,3}','{ok}','{3}'),(4,'{bad,x,y}','{bad}','{4}')",
			"UPDATE jo_stored SET a=CASE WHEN id=1 THEN '{ok,3}'::text[] ELSE '{bad,x,y}'::text[] END",
		} {
			s := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("2202E", "array must have even number of elements"))
			s.ErrorStage = "execute"
			jsonObjectBoth(t, p, b, c, s)
			run(check)
		}
		s := fixture.ErrorDiagnostic("INSERT INTO jo_stored(id,a,k,v) VALUES(3,'{ok,3}','{ok}','{3}'),(4,'{NULL,x}','{bad}','{4}')", fixture.ControlsReferenceDiagnostic("22004", "null value not allowed for object key"))
		jsonObjectBoth(t, p, b, c, s)
		run(check)
		tx, err := c.BeginTx(p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		trun := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, tx, b.Recorder, s) }
		trun(fixture.Exec("SAVEPOINT jo_save", 0))
		trun(fixture.ErrorDiagnostic("INSERT INTO jo_stored(id,a) VALUES(5,'{odd}')", fixture.ControlsReferenceDiagnostic("2202E", "array must have even number of elements")))
		trun(fixture.Exec("ROLLBACK TO SAVEPOINT jo_save", 0))
		trun(fixture.Exec("INSERT INTO jo_stored(id,a,k,v) VALUES(5,'{c,3}','{c}','{3}')", 1))
		trun(jsonObjectSelect("SELECT json_object(a) AS v FROM jo_stored WHERE id=5", `{"c" : "3"}`))
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		jsonObjectBoth(t, p, b, c, jsonObjectSelect("SELECT g AS v FROM jo_stored WHERE id=5", `{"c" : "3"}`))
	})
}

func TestReferenceJSONObjectDeparse(t *testing.T) {
	jsonObjectScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		jsonObjectStored(t, p, b, c)
		for _, tc := range []struct {
			name string
			step fixture.Step
		}{
			{"defaults_generated", fixture.Select(
				`SELECT d.adnum,pg_get_expr(d.adbin,d.adrelid,false) AS expression FROM pg_catalog.pg_attrdef d JOIN pg_catalog.pg_class r ON d.adrelid=r.oid WHERE r.relname='jo_stored' ORDER BY d.adnum`,
				[]string{"adnum", "expression"}, []string{"INT2", "TEXT"}, [][]any{
					{int64(5), `"json_object"('[-2:-1]={d,0}'::text[])`},
					{int64(6), `"json_object"('[5:5]={e}'::text[], '[-1:-1]={0}'::text[])`},
					{int64(7), `"json_object"(a)`},
					{int64(8), `"json_object"(k, v)`},
				})},
			{"check", fixture.Select(
				`SELECT pg_get_expr(c.conbin,c.conrelid,false) AS expression FROM pg_catalog.pg_constraint c JOIN pg_catalog.pg_class r ON c.conrelid=r.oid WHERE r.relname='jo_stored' AND c.contype='c'`,
				[]string{"expression"}, []string{"TEXT"}, [][]any{{`(("json_object"(a))::text <> '{"blocked" : "yes"}'::text)`}})},
			{"view", fixture.Select(
				`SELECT pg_get_viewdef('jo_view'::regclass,false) AS definition`,
				[]string{"definition"}, []string{"TEXT"}, [][]any{{" SELECT id,\n    \"json_object\"(a) AS one,\n    \"json_object\"(k, v) AS two\n   FROM jo_stored;"}})},
			{"index", fixture.Select(
				`SELECT pg_get_indexdef('jo_stored_idx'::regclass,0,false) AS definition`,
				[]string{"definition"}, []string{"TEXT"}, [][]any{{`CREATE INDEX jo_stored_idx ON public.jo_stored USING btree ((("json_object"(k, v))::text))`}})},
		} {
			t.Run(tc.name, func(t *testing.T) { jsonObjectBoth(t, p, b, c, tc.step) })
		}
	})
}
