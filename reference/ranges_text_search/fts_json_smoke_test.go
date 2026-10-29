package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceFTSJSONFeatureSmoke(t *testing.T) {
	ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		defer ftsConvertSetting(t, p, c)()
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		natural := fixture.Select(`SELECT to_tsvector('english','{"aa":"The Fat Rats","b":"dog"}'::json)`, []string{"to_tsvector"}, []string{"TSVECTOR"}, [][]any{{"'dog':5 'fat':2 'rat':3"}})
		natural.CheckMetadata = func(t *testing.T, r *sql.Rows) { ftsConvertMetadata(t, r, b.PG, "to_tsvector", "TSVECTOR") }
		run(natural)
		run(fixture.Prepared(natural))
		for _, tc := range []struct {
			form             ftsJSONForm
			document, filter string
			want             any
		}{
			{ftsJSONForm{"to_tsvector", "json", true, false}, `{"aa":"The Fat Rats","b":"dog"}`, `"string"`, "'dog':5 'fat':2 'rat':3"},
			{ftsJSONForm{"to_tsvector", "jsonb", true, false}, `["cat","the","dog"]`, `"string"`, "'cat':1 'dog':4"},
			{ftsJSONForm{"json_to_tsvector", "json", true, true}, `{"cat":"The Fat Rats","dog":123}`, `"all"`, "'123':9 'cat':1 'dog':7 'fat':4 'rat':5"},
			{ftsJSONForm{"jsonb_to_tsvector", "jsonb", true, true}, `123`, `"numeric"`, "'123':1"},
			{ftsJSONForm{"json_to_tsvector", "json", true, true}, `123`, `[]`, ""},
		} {
			query, _ := tc.form.query(true, "english", tc.document, tc.filter)
			s := ftsJSONValue(query, tc.want)
			s.CheckMetadata = func(t *testing.T, r *sql.Rows) { ftsConvertMetadata(t, r, b.PG, "normalized", "TSVECTOR") }
			run(s)
			query, args := tc.form.query(false, "english", tc.document, tc.filter)
			stmt, err := c.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
			s = ftsJSONValue(query, tc.want, args...)
			s.CheckMetadata = func(t *testing.T, r *sql.Rows) { ftsConvertMetadata(t, r, b.PG, "normalized", "TSVECTOR") }
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			stmt.Close()
		}
		form := ftsJSONForm{"json_to_tsvector", "json", false, true}
		query, _ := form.query(false, nil, `"The Rats"`, `"string"`)
		stmt, err := c.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
		for _, tc := range []struct {
			setting                string
			document, filter, want any
		}{
			{"english", `"The Rats"`, `"string"`, "'rat':2"},
			{"simple", `"The Rats"`, `"string"`, "'rats':2 'the':1"},
			{"simple", `"The Rats"`, `[]`, ""},
			{"simple", `123`, `"numeric"`, "'123':1"},
			{"simple", nil, `{}`, nil},
		} {
			run(fixture.Exec("SET default_text_search_config='"+tc.setting+"'", 0))
			_, args := form.query(false, nil, tc.document, tc.filter)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, ftsJSONValue(query, tc.want, args...))
		}
		_, args := form.query(false, nil, `"cat"`, `{}`)
		ftsJSONErrorRoutes(t, p, b, c, held, fixture.ErrorDiagnostic(query, fixture.ControlsReferenceDiagnostic("22023", "wrong flag type, only arrays and scalars are allowed"), args...))
		_, args = form.query(false, nil, `"The Rats"`, `"string"`)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, ftsJSONValue(query, "'rats':2 'the':1", args...))
		run(ftsJSONValue(`SELECT to_tsvector('english','"The Rats"'::json) AS normalized`, "'rat':2"))
		run(ftsJSONValue(`SELECT to_tsvector(0::regconfig,'{}'::json) AS normalized`, ""))
		s := fixture.ErrorDiagnostic(`SELECT to_tsvector(0::regconfig,'""'::json)`, fixture.ControlsReferenceDiagnostic("XX000", "cache lookup failed for text search configuration 0"))
		ftsJSONErrorRoutes(t, p, b, c, fixture.HeldStatement{}, s)
	})
}
