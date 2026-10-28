package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONPathNavigateConsumers(t *testing.T) {
	jsonpathNavigateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec(`CREATE TABLE jpn_cons(id int,doc jsonb,path jsonpath,vars jsonb,silent boolean)`, 0))
		run(fixture.Exec(`INSERT INTO jpn_cons VALUES(1,'[1,2]','$[*]','{}',false),(2,'[1,2]','$[*]','{}',false),(3,'[3]','$[*]','{}',false)`, 3))
		// GROUP matching happens before named arguments/defaults are normalized.
		// Pin both the observed rejection and independently predicted nearby forms.
		for _, tc := range []struct {
			name, selected, grouped string
			accepted                bool
		}{
			{"original_named_omitted_vs_positional_full", `target=>doc,path=>path`, `doc,path,'{}',false`, false},
			{"positional_omitted_same", `doc,path`, `doc,path`, true},
			{"positional_full_same", `doc,path,'{}',false`, `doc,path,'{}',false`, true},
			{"named_omitted_same", `target=>doc,path=>path`, `target=>doc,path=>path`, true},
			{"named_assignment_spelling", `target=>doc,path=>path`, `target:=doc,path:=path`, true},
			{"positional_omitted_vs_full", `doc,path`, `doc,path,'{}',false`, false},
			{"named_vs_positional_same_arity", `target=>doc,path=>path`, `doc,path`, false},
			{"named_reordered", `target=>doc,path=>path`, `path=>path,target=>doc`, false},
			{"named_full_vs_positional_full", `target=>doc,path=>path,vars=>'{}',silent=>false`, `doc,path,'{}',false`, false},
		} {
			t.Run(tc.name, func(t *testing.T) {
				q := `SELECT jsonb_path_query_array(` + tc.selected + `) AS changed,count(*) AS n FROM jpn_cons GROUP BY jsonb_path_query_array(` + tc.grouped + `) ORDER BY changed`
				if tc.accepted {
					fixture.JSONPathNavigateBoth(t, p, b, c, fixture.Select(q, []string{"changed", "n"}, []string{"JSONB", "INT8"}, [][]any{{`[3]`, int64(1)}, {`[1, 2]`, int64(2)}}))
					return
				}
				d := fixture.ControlsReferenceDiagnostic("42803", `column "jpn_cons.doc" must appear in the GROUP BY clause or be used in an aggregate function`)
				d.Position = fixture.Position(strings.Index(q, "doc") + 1)
				s := fixture.ErrorDiagnostic(q, d)
				s.SelectRows = true
				s.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				s = fixture.Prepared(s)
				s.ErrorStage = "prepare"
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			})
		}
		fixture.JSONPathNavigateBoth(t, p, b, c, fixture.Select(`SELECT count(jsonb_path_query_array(doc,path,vars,silent)) AS n FROM jpn_cons`, []string{"n"}, []string{"INT8"}, [][]any{{int64(3)}}))
		fixture.JSONPathNavigateBoth(t, p, b, c, jsonpathNavigateSelect(`SELECT CASE WHEN id=1 THEN jsonb_path_query_array(doc,path,vars,silent) ELSE jsonb_path_query_array(doc,'$v',vars,silent) END AS changed FROM jpn_cons WHERE id=1`, `[1, 2]`))
		// An immutable constant arm can fail at planning even when no row demands it.
		fixture.JSONPathNavigateBoth(t, p, b, c, jsonpathNavigateError(`SELECT CASE WHEN id=1 THEN jsonb_path_query_array(doc,path,vars,silent) ELSE jsonb_path_query_array('1','$v') END AS changed FROM jpn_cons WHERE id=1`, "42704", `could not find jsonpath variable "v"`))
		fixture.JSONPathNavigateBoth(t, p, b, c, jsonpathNavigateError(`SELECT CASE WHEN id=2 THEN jsonb_path_query_array(doc,'$v',vars,silent) ELSE '[]'::jsonb END FROM jpn_cons WHERE id=2`, "42704", `could not find jsonpath variable "v"`))
		run(fixture.Exec(`CREATE TABLE jpn_left(id int,doc jsonb)`, 0))
		run(fixture.Exec(`CREATE TABLE jpn_right(id int,path jsonpath,vars jsonb,silent boolean)`, 0))
		run(fixture.Exec(`INSERT INTO jpn_left VALUES(1,'[1,2]'),(2,'[3]')`, 2))
		run(fixture.Exec(`INSERT INTO jpn_right VALUES(1,'$[1]','{}',false),(3,'$','{}',false)`, 2))
		fixture.JSONPathNavigateBoth(t, p, b, c, fixture.Select(`SELECT COALESCE(l.id,r.id) AS id,jsonb_path_query_array(l.doc,r.path,r.vars,r.silent) AS changed FROM jpn_left l FULL JOIN jpn_right r ON l.id=r.id ORDER BY id`, []string{"id", "changed"}, []string{"INT4", "JSONB"}, [][]any{{int64(1), `[2]`}, {int64(2), nil}, {int64(3), nil}}))
	})
}

func jsonpathNavigateStored(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) fixture.Step {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
	run(fixture.Exec(`CREATE TABLE jpn_stored(id int PRIMARY KEY,doc jsonb,path jsonpath DEFAULT '$.a[*]',fallback jsonb DEFAULT jsonb_path_query_array('[1,2]','$[*]'),stored jsonb GENERATED ALWAYS AS (jsonb_path_query_array(doc,path)) STORED,virtual jsonb GENERATED ALWAYS AS (jsonb_path_query_array(doc,path)) VIRTUAL,CONSTRAINT jpn_nonempty CHECK (jsonb_path_query_array(doc,path) <> '[]'::jsonb))`, 0))
	run(fixture.Exec(`INSERT INTO jpn_stored(id,doc) VALUES(1,'{"a":[1,2]}'),(2,NULL)`, 2))
	run(fixture.Exec(`CREATE VIEW jpn_view AS SELECT id,jsonb_path_query_array(silent=>false,path=>path,target=>doc) AS changed FROM jpn_stored`, 0))
	run(fixture.Exec(`CREATE INDEX jpn_expr ON jpn_stored(jsonb_path_query_array(doc,path)) WHERE jsonb_path_query_array(doc,path) IS NOT NULL`, 0))
	run(fixture.Exec(`CREATE DOMAIN jpn_recipe_doc AS jsonb CONSTRAINT jpn_recipe_array CHECK(jsonb_typeof(VALUE)='array')`, 0))
	run(fixture.Exec(`CREATE TABLE jpn_recipe(doc jsonb,k text,n numeric)`, 0))
	run(fixture.Exec(`INSERT INTO jpn_recipe VALUES('[1,2]','$[1]extra',12.340)`, 1))
	run(fixture.Exec(`CREATE VIEW jpn_boundary AS SELECT jsonb_path_query_array(target=>((doc::json)::jsonb)::jpn_recipe_doc,path=>(k::varchar(4))::jsonpath) AS bound,jsonb_path_query_array(((n::numeric(4,1))::float8)::text::jsonb,'$') AS rounded FROM jpn_recipe`, 0))
	check := fixture.Select(`SELECT s.id,s.fallback,s.stored,s.virtual,v.changed,b.bound,b.rounded FROM jpn_stored s JOIN jpn_view v ON s.id=v.id CROSS JOIN jpn_boundary b ORDER BY s.id`, []string{"id", "fallback", "stored", "virtual", "changed", "bound", "rounded"}, []string{"INT4", "JSONB", "JSONB", "JSONB", "JSONB", "JSONB", "JSONB"}, [][]any{{int64(1), `[1, 2]`, `[1, 2]`, `[1, 2]`, `[1, 2]`, `[2]`, `[12.3]`}, {int64(2), `[1, 2]`, nil, nil, nil, `[2]`, `[12.3]`}})
	fixture.JSONPathNavigateBoth(t, p, b, c, check)
	fixture.JSONPathNavigateBoth(t, p, b, c, fixture.Select(`SELECT id,changed FROM jpn_view ORDER BY id`, []string{"id", "changed"}, []string{"INT4", "JSONB"}, [][]any{{int64(1), `[1, 2]`}, {int64(2), nil}}))
	return check
}

func TestReferenceJSONPathNavigateStored(t *testing.T) {
	jsonpathNavigateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		check := jsonpathNavigateStored(t, p, b, c)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		d := fixture.ControlsReferenceDiagnostic("23514", `new row for relation "jpn_stored" violates check constraint "jpn_nonempty"`)
		d.Detail = `Failing row contains (3, {}, $."a"[*], [1, 2], [], virtual).`
		d.Schema = "public"
		d.Table = "jpn_stored"
		d.Constraint = "jpn_nonempty"
		run(fixture.ErrorDiagnostic(`INSERT INTO jpn_stored(id,doc) VALUES(3,'{}')`, d))
		run(check)
		run(fixture.Exec(`UPDATE jpn_stored SET path='$.a[1]' WHERE id=1`, 1))
		fixture.JSONPathNavigateBoth(t, p, b, c, fixture.Select(`SELECT id,changed FROM jpn_view WHERE changed='[2]'::jsonb`, []string{"id", "changed"}, []string{"INT4", "JSONB"}, [][]any{{int64(1), `[2]`}}))
		run(fixture.Select(`SELECT stored,virtual FROM jpn_stored WHERE id=1`, []string{"stored", "virtual"}, []string{"JSONB", "JSONB"}, [][]any{{`[2]`, `[2]`}}))
		run(fixture.Exec(`UPDATE jpn_recipe SET doc='{}'`, 1))
		domain := fixture.ControlsReferenceDiagnostic("23514", `value for domain jpn_recipe_doc violates check constraint "jpn_recipe_array"`)
		domain.Schema, domain.DataType, domain.Constraint = "public", "jpn_recipe_doc", "jpn_recipe_array"
		s := fixture.ErrorDiagnostic(`SELECT bound FROM jpn_boundary`, domain)
		s.SelectRows = true
		fixture.JSONPathNavigateBoth(t, p, b, c, s)
		run(fixture.Exec(`UPDATE jpn_recipe SET doc='[1,2]'`, 1))
		for _, q := range []string{
			`CREATE TABLE jpn_volatile(v jsonb GENERATED ALWAYS AS (jsonb_path_query_array(JSON_SCALAR(random())::jsonb,'$')) STORED)`,
			`CREATE INDEX jpn_volatile ON jpn_stored(jsonb_path_query_array(JSON_SCALAR(random())::jsonb,path))`,
		} {
			message := "generation expression is not immutable"
			if q[7] == 'I' {
				message = "functions in index expression must be marked IMMUTABLE"
			}
			run(fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("42P17", message)))
		}
	})
}
