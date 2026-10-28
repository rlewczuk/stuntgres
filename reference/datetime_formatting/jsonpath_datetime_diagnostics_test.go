package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceJSONPathDatetimeGrammarBinding(t *testing.T) {
	for _, tc := range []struct{ path, token string }{
		{`$.date(1)`, `1`}, {`$.datetime(1)`, `1`}, {`$.datetime("YYYY", "MM")`, `,`},
		{`$.time(-1)`, `-`}, {`$.time(1.5)`, `1.5`}, {`$.time_tz(+1)`, `+`},
		{`$.timestamp(null)`, `null`}, {`$.timestamp_tz($x)`, `$x`},
		{`$."date"()`, `(`},
	} {
		t.Run(tc.path, func(t *testing.T) {
			jsonpathDatetimeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				message := `syntax error at or near "` + tc.token + `" of jsonpath input`
				if tc.path == `$.timestamp(null)` {
					message = `syntax error at end of jsonpath input`
				}
				for _, prefix := range []string{"SELECT ", "/*é🙂*/ SELECT "} {
					literal := fixture.JSONReferenceQuote(tc.path)
					q := prefix + `jsonb_path_query_array(NULL::jsonb,` + literal + `::jsonpath,silent=>true)`
					d := fixture.ControlsReferenceDiagnostic("42601", message)
					d.Position = fixture.Position(len([]rune(q[:strings.Index(q, literal)])) + 1)
					s := fixture.ErrorDiagnostic(q, d)
					s.SelectRows = true
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					s = fixture.Prepared(s)
					s.ErrorStage = "prepare"
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				}
				d := fixture.ControlsReferenceDiagnostic("42601", message)
				d.Context = "unnamed portal parameter $2 = '...'"
				s := fixture.ErrorDiagnostic(`SELECT jsonb_path_query_array($1,$2)`, d, `"2023-08-15"`, tc.path)
				s.SelectRows = true
				s.ErrorStage = "execute"
				fixture.JSONPathNavigateBoth(t, p, b, c, s)
			})
		})
	}
	jsonpathDatetimeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, q := range []string{
			`SELECT pg_catalog.jsonb_path_query_array(path=>'$.date()',target=>'"2023-08-15"') AS changed`,
			`SELECT jsonb_path_query_array($1,$2) AS changed`,
		} {
			args := []any(nil)
			if strings.Contains(q, "$1") {
				args = []any{`"2023-08-15"`, `$.date()`}
			}
			fixture.JSONPathNavigateBoth(t, p, b, c, jsonpathNavigateSelect(q, `["2023-08-15"]`, args...))
		}
		for _, tc := range []struct{ q, message string }{
			{`SELECT jsonb_path_query_array(1,'$.date()')`, `function jsonb_path_query_array(integer, unknown) does not exist`},
			{`SELECT jsonb_path_query_array('1','$.date()','{}',false,true)`, `function jsonb_path_query_array(unknown, unknown, unknown, boolean, boolean) does not exist`},
		} {
			d := fixture.ControlsReferenceDiagnostic("42883", tc.message)
			d.Hint = `No function matches the given name and argument types. You might need to add explicit type casts.`
			d.Position = "8"
			s := fixture.ErrorDiagnostic(tc.q, d)
			s.SelectRows = true
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			s = fixture.Prepared(s)
			s.ErrorStage = "prepare"
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		q := `SELECT jsonb_path_query_array($1,$1)`
		d := fixture.ControlsReferenceDiagnostic("42P08", `inconsistent types deduced for parameter $1`)
		d.Detail = "jsonb versus jsonpath"
		d.Position = "34"
		s := fixture.Prepared(fixture.ErrorDiagnostic(q, d, `"2023-08-15"`))
		s.SelectRows = true
		s.ErrorStage = "prepare"
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`CREATE DOMAIN jpdt_array AS jsonb CONSTRAINT jpdt_isarray CHECK(jsonb_typeof(VALUE)='array')`, 0))
		d = fixture.ControlsReferenceDiagnostic("23514", `value for domain jpdt_array violates check constraint "jpdt_isarray"`)
		d.Schema, d.DataType, d.Constraint = "public", "jpdt_array", "jpdt_isarray"
		s = fixture.ErrorDiagnostic(`SELECT jsonb_path_query_array('1'::jsonb::jpdt_array,'$.date()',silent=>true)`, d)
		s.SelectRows = true
		fixture.JSONPathNavigateBoth(t, p, b, c, s)
	})
}

func TestReferenceJSONPathDatetimeMetadata(t *testing.T) {
	jsonpathDatetimeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range jsonpathDatetimeISO {
			for _, fn := range []string{"jsonb_path_query_array", "jsonb_path_query_first", "jsonb_path_query"} {
				for _, empty := range []bool{false, true} {
					q := `SELECT ` + fn + `($1,$2) AS changed`
					if empty {
						q += ` WHERE false`
					}
					want := jsonpathDatetimeString(tc.output)
					if fn == "jsonb_path_query_array" {
						want = jsonpathDatetimeArray(tc.output)
					}
					rows := [][]any{{want}}
					if empty {
						rows = nil
					}
					s := fixture.JSONBSetMetadataStep(fixture.Select(q, []string{"changed"}, []string{"JSONB"}, rows, jsonpathDatetimeString(tc.input), `$.`+tc.method+`()`), b.PG)
					fixture.JSONPathNavigateBoth(t, p, b, c, s)
				}
			}
		}
		for _, fn := range []string{"jsonb_path_query_array", "jsonb_path_query_first"} {
			fixture.JSONPathNavigateBoth(t, p, b, c, fixture.JSONBSetMetadataStep(jsonpathNavigateSelect(`SELECT `+fn+`(NULL::jsonb,'$.date()') AS changed`, nil), b.PG))
		}
		fixture.JSONPathNavigateBoth(t, p, b, c, fixture.Select(`SELECT q.value,q.n FROM jsonb_path_query('["2023-08-15","2023-08-16"]','$[*].date()') WITH ORDINALITY AS q(value,n) ORDER BY n`, []string{"value", "n"}, []string{"JSONB", "INT8"}, [][]any{{`"2023-08-15"`, int64(1)}, {`"2023-08-16"`, int64(2)}}))
		for _, fn := range []string{"jsonb_path_exists", "jsonb_path_match"} {
			path := `$.date()`
			if fn == "jsonb_path_match" {
				path = `$.date() == $.date()`
			}
			fixture.JSONPathNavigateBoth(t, p, b, c, fixture.Select(`SELECT `+fn+`('"2023-08-15"',$1) AS changed`, []string{"changed"}, []string{"BOOL"}, [][]any{{true}}, path))
		}
	})
}
