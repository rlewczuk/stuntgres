package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func jsonpathMethodsStored(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) fixture.Step {
	t.Helper()
	for _, s := range []fixture.Step{
		fixture.Exec(`CREATE TABLE jpm_stored(id int PRIMARY KEY,doc jsonb,path jsonpath DEFAULT '$[*].string()',fallback jsonb DEFAULT jsonb_path_query_array('[true,1.2300]','$[*].string()'),stored jsonb GENERATED ALWAYS AS (jsonb_path_query_array(doc,path)) STORED,virtual jsonb GENERATED ALWAYS AS (jsonb_path_query_array(doc,path)) VIRTUAL,CONSTRAINT jpm_nonempty CHECK(jsonb_path_query_array(doc,path)<>'[]'::jsonb))`, 0),
		fixture.Exec(`INSERT INTO jpm_stored(id,doc) VALUES(1,'[1.2300,false]'),(2,NULL)`, 2),
		fixture.Exec(`CREATE VIEW jpm_view AS SELECT id,jsonb_path_query_array(path=>path,target=>doc) AS changed FROM jpm_stored`, 0),
		fixture.Exec(`CREATE INDEX jpm_expr ON jpm_stored(jsonb_path_query_array(doc,path)) WHERE jsonb_path_query_array(doc,path) IS NOT NULL`, 0),
	} {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	}
	check := fixture.Select(`SELECT s.id,s.fallback,s.stored,s.virtual,v.changed FROM jpm_stored s JOIN jpm_view v ON s.id=v.id ORDER BY s.id`, []string{"id", "fallback", "stored", "virtual", "changed"}, []string{"INT4", "JSONB", "JSONB", "JSONB", "JSONB"}, [][]any{{int64(1), `["true", "1.2300"]`, `["1.2300", "false"]`, `["1.2300", "false"]`, `["1.2300", "false"]`}, {int64(2), `["true", "1.2300"]`, nil, nil, nil}})
	fixture.JSONPathNavigateBoth(t, p, b, c, check)
	return check
}

func TestReferenceJSONPathMethodsStored(t *testing.T) {
	jsonpathNavigateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		jsonpathMethodsStored(t, p, b, c)
		for _, s := range []fixture.Step{
			fixture.Select(`SELECT jsonb_path_query_array(doc,path) AS changed,count(*) AS n FROM jpm_stored GROUP BY jsonb_path_query_array(doc,path) ORDER BY changed`, []string{"changed", "n"}, []string{"JSONB", "INT8"}, [][]any{{`["1.2300", "false"]`, int64(1)}, {nil, int64(1)}}),
			jsonpathNavigateSelect(`SELECT CASE WHEN id=1 THEN jsonb_path_query_array(doc,path) ELSE NULL END AS changed FROM jpm_stored WHERE id=1`, `["1.2300", "false"]`),
			fixture.Select(`SELECT s.id,q.value,q.n FROM jpm_stored s CROSS JOIN LATERAL jsonb_path_query(s.doc,s.path) WITH ORDINALITY AS q(value,n) ORDER BY s.id,q.n`, []string{"id", "value", "n"}, []string{"INT4", "JSONB", "INT8"}, [][]any{{int64(1), `"1.2300"`, int64(1)}, {int64(1), `"false"`, int64(2)}}),
		} {
			fixture.JSONPathNavigateBoth(t, p, b, c, s)
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`UPDATE jpm_stored SET doc='[true]' WHERE id=1`, 1))
		fixture.JSONPathNavigateBoth(t, p, b, c, fixture.Select(`SELECT stored,virtual FROM jpm_stored WHERE jsonb_path_query_array(doc,path)='["true"]'::jsonb`, []string{"stored", "virtual"}, []string{"JSONB", "JSONB"}, [][]any{{`["true"]`, `["true"]`}}))
		fixture.JSONPathNavigateBoth(t, p, b, c, jsonpathNavigateSelect(`SELECT changed FROM jpm_view WHERE id=1`, `["true"]`))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.ErrorDiagnostic(`INSERT INTO jpm_stored(id,doc) VALUES(3,'[null]')`, fixture.ControlsReferenceDiagnostic("22036", jsonpathMethodsStringError)))
		d := fixture.ControlsReferenceDiagnostic("23514", `new row for relation "jpm_stored" violates check constraint "jpm_nonempty"`)
		d.Detail = `Failing row contains (3, [], $[*].string(), ["true", "1.2300"], [], virtual).`
		d.Schema, d.Table, d.Constraint = "public", "jpm_stored", "jpm_nonempty"
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.ErrorDiagnostic(`INSERT INTO jpm_stored(id,doc) VALUES(3,'[]')`, d))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`CREATE DOMAIN jpm_doc AS jsonb CONSTRAINT jpm_array CHECK(jsonb_typeof(VALUE)='array')`, 0))
		domain := fixture.ControlsReferenceDiagnostic("23514", `value for domain jpm_doc violates check constraint "jpm_array"`)
		domain.Schema, domain.DataType, domain.Constraint = "public", "jpm_doc", "jpm_array"
		s := fixture.ErrorDiagnostic(`SELECT jsonb_path_query_array('1'::jsonb::jpm_doc,'$.type()',silent=>true)`, domain)
		s.SelectRows = true
		fixture.JSONPathNavigateBoth(t, p, b, c, s)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select(`SELECT count(*) AS n FROM jpm_stored`, []string{"n"}, []string{"INT8"}, [][]any{{int64(2)}}))
	})
}

func TestReferenceJSONPathMethodsControls(t *testing.T) {
	jsonpathNavigateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		check := jsonpathMethodsStored(t, p, b, c)
		future := []fixture.Step{
			fixture.Exec(`INSERT INTO jpm_stored(id,doc) VALUES(3,'[true]')`, 1),
			jsonpathNavigateSelect(`SELECT changed FROM jpm_view WHERE id=3`, `["true"]`),
			fixture.Exec(`UPDATE jpm_stored SET doc='[2.00]' WHERE id=3`, 1),
			fixture.Select(`SELECT stored,virtual FROM jpm_stored WHERE id=3`, []string{"stored", "virtual"}, []string{"JSONB", "JSONB"}, [][]any{{`["2.00"]`, `["2.00"]`}}),
		}
		dump := fixture.JSONPathNavigateReplay(t, p, b, c, check, future...)
		if !b.PG {
			unchanged := func() {
				var after bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(dump, after.Bytes()) {
					t.Fatal("failed Restore published catalog")
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, check)
			}
			script := `CREATE TABLE jpm_bad(v jsonb); INSERT INTO jpm_bad VALUES(jsonb_path_query_array('[1]','$[*].string()')); INSERT INTO jpm_bad VALUES(jsonb_path_query_array('[1,null]','$[*].string()'));`
			d := fixture.ControlsReferenceDiagnostic("22036", jsonpathMethodsStringError)
			fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)), &d)
			unchanged()
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			fixture.LexicalOrdinary(t, stuntgres.Restore(ctx, p.Native, bytes.NewReader(dump)), context.Canceled)
			unchanged()
			duringRead, cancelRead := context.WithCancel(p.Ctx)
			fixture.LexicalOrdinary(t, stuntgres.Restore(duringRead, p.Native, &fixture.CatalogControlCancelReader{Cancel: cancelRead}), context.Canceled)
			cancelRead()
			unchanged()
		}
		for _, s := range future {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
	})
}

func TestReferenceJSONPathMethodsFeatureSmoke(t *testing.T) {
	jsonpathNavigateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct{ doc, path, want string }{
			{jsonpathMethodsA, `$[*].type()`, `["null", "number", "boolean", "string", "array", "array", "array", "object", "object"]`},
			{`[1,2,3]`, `$.size()`, `[3]`}, {`7`, `$.size()`, `[1]`},
			{jsonpathMethodsB, `$[*].boolean()`, `[true, false, false, true, true, true, false, false, true]`},
			{jsonpathMethodsC, `$[*].string()`, `["1.2300", "xyz", "false"]`},
		} {
			fixture.JSONPathNavigateBoth(t, p, b, c, fixture.JSONBSetMetadataStep(jsonpathNavigateSelect(`SELECT jsonb_path_query_array($1,$2) AS changed`, tc.want, tc.doc, tc.path), b.PG))
		}
		jsonpathMethodsIdentityRoutes(t, p, c, `[{"a":1,"b":2},{"a":1,"b":2}]`, `$[0,0,1].keyvalue()`, `{}`, []string{"a", "b", "a", "b", "a", "b"}, []string{`1`, `2`, `1`, `2`, `1`, `2`}, []int{0, 0, 0, 0, 1, 1})
		fixture.JSONPathNavigateBoth(t, p, b, c, jsonpathNavigateError(`SELECT jsonb_path_query_array('7','strict $.size()')`, "22039", jsonpathMethodsSizeError))
		fixture.JSONPathNavigateBoth(t, p, b, c, jsonpathNavigateError(`SELECT jsonb_path_query_array('[1,"bad",0]','$[*].boolean()')`, "22036", `argument "bad" of jsonpath item method .boolean() is invalid for type boolean`))
		fixture.JSONPathNavigateBoth(t, p, b, c, jsonpathNavigateSelect(`SELECT jsonb_path_query_array('[1,"bad",0]','$[*].boolean()',silent=>true) AS changed`, `[true]`))
		q := `SELECT jsonb_path_query_array($1,$2,$3,$4) AS changed`
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, s := range []fixture.Step{
			jsonpathNavigateError(q, "22036", `argument "bad" of jsonpath item method .boolean() is invalid for type boolean`, `[1,"bad",0]`, `$[*].boolean()`, `{}`, false),
			jsonpathNavigateSelect(q, `[true]`, `[1,"bad",0]`, `$[*].boolean()`, `{}`, true),
			jsonpathNavigateSelect(q, `["1.2300", "xyz", "false"]`, jsonpathMethodsC, `$[*].string()`, `{}`, false),
		} {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		jsonpathMethodsPairs(t, p, held, q, []any{jsonpathMethodsO, `$.keyvalue()`, `{}`, false}, []string{"x", "y", "z"}, []string{`"20"`, `32`, `null`}, []int{0, 0, 0})
		for _, s := range []fixture.Step{
			fixture.Exec(`CREATE TABLE jpm_smoke(doc jsonb)`, 0),
			fixture.Exec(`INSERT INTO jpm_smoke VALUES('[1.2300,false]')`, 1),
			fixture.Exec(`CREATE VIEW jpm_smoke_view AS SELECT jsonb_path_query_array(path=>'$[*].string()',target=>doc) AS changed FROM jpm_smoke`, 0),
		} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		check := fixture.JSONPathNavigateViewMetadataStep(jsonpathNavigateSelect(`SELECT changed FROM jpm_smoke_view`, `["1.2300", "false"]`), b.PG)
		fixture.JSONPathNavigateBoth(t, p, b, c, check)
		fixture.JSONPathNavigateReplay(t, p, b, c, check, fixture.Exec(`UPDATE jpm_smoke SET doc='[true]'`, 1), jsonpathNavigateSelect(`SELECT changed FROM jpm_smoke_view`, `["true"]`))
	})
}
