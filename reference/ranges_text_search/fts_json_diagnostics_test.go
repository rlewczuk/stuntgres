package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

const ftsJSONFlagHint = `Possible values are: "string", "numeric", "boolean", "key", and "all".`

func ftsJSONErrorRoutes(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, held fixture.HeldStatement, s fixture.Step) {
	t.Helper()
	s.SelectRows, s.ErrorStage = true, "execute"
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
	if held.Statement != nil {
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
	}
}

func TestReferenceFTSJSONDiagnostics(t *testing.T) {
	for _, form := range ftsJSONForms() {
		if !form.filtered {
			continue
		}
		t.Run(form.name+"_"+map[bool]string{false: "default", true: "explicit"}[form.explicit], func(t *testing.T) {
			ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				defer ftsConvertSetting(t, p, c)()
				query, _ := form.query(false, "english", `"cat"`, `{}`)
				stmt, err := c.PrepareContext(p.Ctx, query)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
				for _, tc := range []struct{ filter, code, message, hint string }{
					{`{}`, "22023", "wrong flag type, only arrays and scalars are allowed", ""},
					{`null`, "22023", "flag array element is not a string", ftsJSONFlagHint},
					{`true`, "22023", "flag array element is not a string", ftsJSONFlagHint},
					{`1`, "22023", "flag array element is not a string", ftsJSONFlagHint},
					{`[null]`, "22023", "flag array element is not a string", ftsJSONFlagHint},
					{`["all",1]`, "22023", "flag array element is not a string", ftsJSONFlagHint},
					{`""`, "22023", `wrong flag in flag array: ""`, ftsJSONFlagHint},
					{`"bogus"`, "22023", `wrong flag in flag array: "bogus"`, ftsJSONFlagHint},
					{`["string","bogus"]`, "22023", `wrong flag in flag array: "bogus"`, ftsJSONFlagHint},
					{`" string"`, "22023", `wrong flag in flag array: " string"`, ftsJSONFlagHint},
					{`[[]]`, "XX000", "unexpected end of flag array", ""},
					{`[{}]`, "XX000", "unexpected end of flag array", ""},
				} {
					d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
					d.Hint = tc.hint
					literal, _ := form.query(true, "english", `"cat"`, tc.filter)
					ftsJSONErrorRoutes(t, p, b, c, fixture.HeldStatement{}, fixture.ErrorDiagnostic(literal, d))
					_, args := form.query(false, "english", `"cat"`, tc.filter)
					ftsJSONErrorRoutes(t, p, b, c, held, fixture.ErrorDiagnostic(query, d, args...))
					_, good := form.query(false, "english", `"Rats"`, `"string"`)
					fixture.RunStep(t, p.Ctx, held, b.Recorder, ftsJSONValue(query, "'rat':1", good...))
				}
			})
		})
	}
	// JSON input accepts these escapes, but traversal must decode even values
	// and keys excluded by the filter. Filter validation precedes that decoding.
	ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, document := range []string{`"\u0000"`, `{"\u0000":1}`, `["cat","\u0000"]`} {
			form := ftsJSONForm{"json_to_tsvector", "json", true, true}
			query, args := form.query(false, "simple", document, `[]`)
			stmt, err := c.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
			d := fixture.ControlsReferenceDiagnostic("22P05", "unsupported Unicode escape sequence")
			d.Detail = `\u0000 cannot be converted to text.`
			switch document {
			case `"\u0000"`:
				d.Context = `JSON data, line 1: "\u0000...`
			case `{"\u0000":1}`:
				d.Context = `JSON data, line 1: {"\u0000...`
			default:
				d.Context = `JSON data, line 1: ["cat","\u0000...`
			}
			ftsJSONErrorRoutes(t, p, b, c, held, fixture.ErrorDiagnostic(query, d, args...))
			literal, _ := form.query(true, "simple", document, `[]`)
			ftsJSONErrorRoutes(t, p, b, c, fixture.HeldStatement{}, fixture.ErrorDiagnostic(literal, d))
			_, args = form.query(false, "simple", document, `{}`)
			ftsJSONErrorRoutes(t, p, b, c, held, fixture.ErrorDiagnostic(query, fixture.ControlsReferenceDiagnostic("22023", "wrong flag type, only arrays and scalars are allowed"), args...))
			_, args = form.query(false, "simple", `"cat"`, `"string"`)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, ftsJSONValue(query, "'cat':1", args...))
			stmt.Close()
		}
	})
}

func TestReferenceFTSJSONLazyConfiguration(t *testing.T) {
	for _, typ := range []string{"json", "jsonb"} {
		ftsJSONCases(t, []ftsJSONForm{{"to_tsvector", typ, true, false}}, []ftsJSONCase{
			{"0", `{}`, nil, ""}, {"0", `[]`, nil, ""}, {"0", `null`, nil, ""}, {"0", `123`, nil, ""},
		})
		form := ftsJSONForm{typ + "_to_tsvector", typ, true, true}
		ftsJSONCases(t, []ftsJSONForm{form}, []ftsJSONCase{
			{"0", `123`, `[]`, ""}, {"0", `"cat"`, `"numeric"`, ""}, {"0", `{"cat":"dog"}`, `[]`, ""},
		})
		ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			if typ == "json" {
				for _, tc := range []struct {
					document, filter string
					unicode          bool
				}{
					{`["cat","\u0000"]`, `"string"`, false},
					{`{"cat":"dog","later":"\u0000"}`, `"key"`, false},
					{`{"cat":"\u0000"}`, `"key"`, true},
				} {
					query, args := form.query(false, "0", tc.document, tc.filter)
					stmt, err := c.PrepareContext(p.Ctx, query)
					if err != nil {
						t.Fatal(err)
					}
					held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
					d := fixture.ControlsReferenceDiagnostic("XX000", "cache lookup failed for text search configuration 0")
					if tc.unicode {
						d = fixture.ControlsReferenceDiagnostic("22P05", "unsupported Unicode escape sequence")
						d.Detail = `\u0000 cannot be converted to text.`
						d.Context = `JSON data, line 1: {"cat":"\u0000...`
					}
					ftsJSONErrorRoutes(t, p, b, c, held, fixture.ErrorDiagnostic(query, d, args...))
					literal, _ := form.query(true, "0", tc.document, tc.filter)
					ftsJSONErrorRoutes(t, p, b, c, fixture.HeldStatement{}, fixture.ErrorDiagnostic(literal, d))
					stmt.Close()
				}
			}
			for _, f := range []ftsJSONForm{{"to_tsvector", typ, true, false}, form} {
				query, _ := f.query(false, "0", `""`, `"string"`)
				stmt, err := c.PrepareContext(p.Ctx, query)
				if err != nil {
					t.Fatal(err)
				}
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
				for _, document := range []string{`""`, `"the"`} {
					_, args := f.query(false, "0", document, `"string"`)
					d := fixture.ControlsReferenceDiagnostic("XX000", "cache lookup failed for text search configuration 0")
					ftsJSONErrorRoutes(t, p, b, c, held, fixture.ErrorDiagnostic(query, d, args...))
					literal, _ := f.query(true, "0", document, `"string"`)
					ftsJSONErrorRoutes(t, p, b, c, fixture.HeldStatement{}, fixture.ErrorDiagnostic(literal, d))
				}
				_, args := f.query(false, "english", `"cat"`, `"string"`)
				fixture.RunStep(t, p.Ctx, held, b.Recorder, ftsJSONValue(query, "'cat':1", args...))
				stmt.Close()
			}
			for _, tc := range []struct{ document, filter string }{{`"cat"`, `{}`}, {`{"":"cat"}`, `"key"`}} {
				q, args := form.query(false, "0", tc.document, tc.filter)
				d := fixture.ControlsReferenceDiagnostic("22023", "wrong flag type, only arrays and scalars are allowed")
				if tc.filter != `{}` {
					d = fixture.ControlsReferenceDiagnostic("XX000", "cache lookup failed for text search configuration 0")
				}
				ftsJSONErrorRoutes(t, p, b, c, fixture.HeldStatement{}, fixture.ErrorDiagnostic(q, d, args...))
			}
		})
	}
}
