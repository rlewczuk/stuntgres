package reference_test

import (
	"database/sql"
	"encoding/json"
	"testing"

	"stuntgres/reference/fixture"
)

const jsonpathDatetimeSQL = `SELECT jsonb_path_query_array($1::jsonb,$2::jsonpath,$3::jsonb,$4::boolean) AS changed`

func jsonpathDatetimeScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	jsonpathNavigateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		body(t, p, b, c)
	})
}

func jsonpathDatetimeString(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func jsonpathDatetimeArray(s string) string { return "[" + jsonpathDatetimeString(s) + "]" }

func jsonpathDatetimeValue(t *testing.T, doc, path, want string) {
	t.Helper()
	jsonpathDatetimeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		s := jsonpathNavigateSelect(jsonpathDatetimeSQL, want, doc, path, `{}`, false)
		fixture.JSONPathNavigateBoth(t, p, b, c, fixture.JSONBSetMetadataStep(s, b.PG))
		q := `SELECT jsonb_path_query_array(` + fixture.JSONReferenceQuote(doc) + `,` + fixture.JSONReferenceQuote(path) + `) AS changed`
		fixture.JSONPathNavigateBoth(t, p, b, c, fixture.JSONBSetMetadataStep(jsonpathNavigateSelect(q, want), b.PG))
	})
}

func jsonpathDatetimeDiagnostic(t *testing.T, doc, path string, d fixture.Diagnostic) {
	t.Helper()
	jsonpathDatetimeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		s := fixture.ErrorDiagnostic(jsonpathDatetimeSQL, d, doc, path, `{}`, false)
		s.SelectRows, s.ErrorStage = true, "execute"
		fixture.JSONPathNavigateBoth(t, p, b, c, s)
	})
}

func jsonpathDatetimeFormatError(method, input string) fixture.Diagnostic {
	d := fixture.ControlsReferenceDiagnostic("22031", method+` format is not recognized: "`+input+`"`)
	if method == "datetime" {
		d.Hint = `Use a datetime template argument to specify the input data format.`
	}
	return d
}

func jsonpathDatetimeZoneError(source, target string) fixture.Diagnostic {
	d := fixture.ControlsReferenceDiagnostic("0A000", "cannot convert value from "+source+" to "+target+" without time zone usage")
	d.Hint = `Use *_tz() function for time zone support.`
	return d
}
