package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTextSearchSettingMissing(t *testing.T) {
	for _, mutation := range []string{"drop", "rename", "move", "schema"} {
		for _, f := range append(textSearchSettingForms(), textSearchSettingMatches()...) {
			t.Run(mutation+"/"+f.name, func(t *testing.T) {
				fixture.SchemaReferenceCleanup(t, "ts_setting_missing")
				fixture.SchemaReferenceCleanup(t, "ts_setting_moved")
				textSearchSettingScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
					run(fixture.Exec("CREATE SCHEMA ts_setting_missing", 0))
					run(fixture.Exec("CREATE SCHEMA ts_setting_moved", 0))
					run(fixture.Exec("CREATE TEXT SEARCH CONFIGURATION ts_setting_missing.cfg (COPY=pg_catalog.simple)", 0))
					run(fixture.Exec("SET default_text_search_config='ts_setting_missing.cfg'", 0))
					for _, parameter := range []bool{false, true} {
						q, args := f.query(parameter, false, nil, f.input(), textSearchSettingFilter(f))
						h := textSearchSettingHold(t, p, c, q)
						defer h.Statement.Close()
						textSearchSettingRoutes(t, p, b, c, h, f.step(q, false, false, args...))
						ddl := map[string]string{
							"drop": "DROP TEXT SEARCH CONFIGURATION ts_setting_missing.cfg",
							"rename": "ALTER TEXT SEARCH CONFIGURATION ts_setting_missing.cfg RENAME TO renamed",
							"move": "ALTER TEXT SEARCH CONFIGURATION ts_setting_missing.cfg SET SCHEMA ts_setting_moved",
							"schema": "DROP SCHEMA ts_setting_missing CASCADE",
						}[mutation]
						run(fixture.Exec(ddl, 0))
						run(textSearchSettingShow("ts_setting_missing.cfg"))
						d := textSearchSettingMissingDiagnostic(mutation == "schema", f.debug)
						s := fixture.ErrorDiagnostic(q, d, args...)
						s.SelectRows, s.ErrorStage = true, "execute"
						textSearchSettingRoutes(t, p, b, c, h, s)
						if !f.match {
							eq, ea := f.query(parameter, true, "english", f.input(), textSearchSettingFilter(f))
							eh := textSearchSettingHold(t, p, c, eq)
							textSearchSettingRoutes(t, p, b, c, eh, f.step(eq, true, false, ea...))
							eh.Statement.Close()
						}
						run(fixture.Select("SELECT 'rat'::tsvector @@ 'rat'::tsquery AS matched", []string{"matched"}, []string{"BOOL"}, [][]any{{true}}))
						if mutation == "schema" { run(fixture.Exec("CREATE SCHEMA ts_setting_missing", 0)) }
						run(fixture.Exec("CREATE TEXT SEARCH CONFIGURATION ts_setting_missing.cfg (COPY=pg_catalog.english)", 0))
						textSearchSettingRoutes(t, p, b, c, h, f.step(q, true, false, args...))
						// Reset the fixture for the second, independently held route.
						run(fixture.Exec("ALTER TEXT SEARCH CONFIGURATION ts_setting_missing.cfg ALTER MAPPING FOR asciiword WITH pg_catalog.simple", 0))
						if mutation == "rename" { run(fixture.Exec("DROP TEXT SEARCH CONFIGURATION ts_setting_missing.renamed", 0)) }
						if mutation == "move" { run(fixture.Exec("DROP TEXT SEARCH CONFIGURATION ts_setting_moved.cfg", 0)) }
					}
				})
			})
		}
	}
}

func TestReferenceTextSearchSettingJSONResolution(t *testing.T) {
	for _, form := range ftsJSONForms() {
		if form.explicit { continue }
		for _, schemaMissing := range []bool{false, true} {
			label := form.name+"_"+form.typ+"_config"
			if schemaMissing { label = form.name+"_"+form.typ+"_schema" }
			t.Run(label, func(t *testing.T) {
				fixture.SchemaReferenceCleanup(t, "ts_setting_missing")
				textSearchSettingScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
					run(fixture.Exec("CREATE SCHEMA ts_setting_missing", 0))
					run(fixture.Exec("CREATE TEXT SEARCH CONFIGURATION ts_setting_missing.cfg (COPY=pg_catalog.simple)", 0))
					run(fixture.Exec("SET default_text_search_config='ts_setting_missing.cfg'", 0))
					q, _ := form.query(false, nil, `"The Rats"`, `"string"`)
					h := textSearchSettingHold(t, p, c, q)
					defer h.Statement.Close()
					ddl := "DROP TEXT SEARCH CONFIGURATION ts_setting_missing.cfg"
					if schemaMissing { ddl = "DROP SCHEMA ts_setting_missing CASCADE" }
					run(fixture.Exec(ddl, 0))
					cases := []struct{ document, filter string }{{`{}`, `"string"`}, {`[]`, `"string"`}, {`null`, `"string"`}, {`123`, `"string"`}, {`{"n":123}`, `"string"`}}
					if form.filtered { cases = append(cases, struct{ document, filter string }{`"The Rats"`, `[]`}, struct{ document, filter string }{`"The Rats"`, `"numeric"`}) }
					if form.typ == "json" { cases = append(cases, struct{ document, filter string }{`"\u0000"`, `[]`}) }
					for _, tc := range cases {
						t.Run(tc.document+tc.filter, func(t *testing.T) {
							for _, literal := range []bool{false, true} {
								query, args := form.query(literal, nil, tc.document, tc.filter)
								held := h
								if literal { held = textSearchSettingHold(t, p, c, query); defer held.Statement.Close() }
								s := fixture.ErrorDiagnostic(query, textSearchSettingMissingDiagnostic(schemaMissing, false), args...)
								s.SelectRows, s.ErrorStage = true, "execute"
								textSearchSettingRoutes(t, p, b, c, held, s)
							}
						})
					}
					if form.filtered {
						for _, flag := range []string{`{}`, `"bogus"`} {
							d := fixture.ControlsReferenceDiagnostic("22023", "wrong flag type, only arrays and scalars are allowed")
							if flag != `{}` { d.Message, d.Hint = `wrong flag in flag array: "bogus"`, ftsJSONFlagHint }
							_, args := form.query(false, nil, `"The Rats"`, flag)
							ftsJSONErrorRoutes(t, p, b, c, h, fixture.ErrorDiagnostic(q, d, args...))
						}
					}
					// Explicit zero keeps lazy acquisition despite the stale setting.
					explicit := form
					explicit.explicit = true
					for _, tc := range cases {
						if tc.document == `"\u0000"` { continue }
						eq, ea := explicit.query(false, "0", tc.document, tc.filter)
						eh := textSearchSettingHold(t, p, c, eq)
						textSearchSettingRoutes(t, p, b, c, eh, ftsJSONValue(eq, "", ea...))
						eh.Statement.Close()
					}
					eq, ea := explicit.query(false, "0", `""`, `"string"`)
					eh := textSearchSettingHold(t, p, c, eq)
					ftsJSONErrorRoutes(t, p, b, c, eh, fixture.ErrorDiagnostic(eq, fixture.ControlsReferenceDiagnostic("XX000", "cache lookup failed for text search configuration 0"), ea...))
					eh.Statement.Close()
					run(fixture.Exec("SET default_text_search_config='english'", 0))
					_, args := form.query(false, nil, `"The Rats"`, `"string"`)
					textSearchSettingRoutes(t, p, b, c, h, ftsJSONValue(q, "'rat':2", args...))
				})
			})
		}
	}
}
