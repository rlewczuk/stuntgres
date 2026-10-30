package reference_test

import (
	"database/sql"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func TestReferenceTextSearchSettingNullDiagnostics(t *testing.T) {
	for _, stale := range []bool{false, true} {
		for _, f := range append(textSearchSettingForms(), textSearchSettingMatches()...) {
			label := "valid/"+f.name
			if stale { label = "stale/"+f.name }
			t.Run(label, func(t *testing.T) {
				textSearchSettingScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
					run(fixture.Exec("SET default_text_search_config='simple'", 0))
					if stale {
						run(fixture.Exec("CREATE TEXT SEARCH CONFIGURATION public.setting_null (COPY=pg_catalog.simple)", 0))
						run(fixture.Exec("SET default_text_search_config='public.setting_null'", 0))
						run(fixture.Exec("DROP TEXT SEARCH CONFIGURATION public.setting_null", 0))
					}
					cases := []struct{ explicit bool; config, document, filter any }{{false, nil, nil, textSearchSettingFilter(f)}}
					if !f.match {
						cases = append(cases, struct{ explicit bool; config, document, filter any }{true, "english", nil, textSearchSettingFilter(f)}, struct{ explicit bool; config, document, filter any }{true, nil, f.input(), textSearchSettingFilter(f)})
					}
					if f.match { cases = append(cases, struct{ explicit bool; config, document, filter any }{false, nil, f.input(), nil}) }
					if f.json != nil && f.json.filtered {
						cases = append(cases, struct{ explicit bool; config, document, filter any }{false, nil, f.input(), nil}, struct{ explicit bool; config, document, filter any }{true, "english", f.input(), nil})
						cases = append(cases, struct{ explicit bool; config, document, filter any }{false, nil, nil, `{}`}, struct{ explicit bool; config, document, filter any }{true, nil, f.input(), `{}`})
					}
					for _, tc := range cases {
						for _, parameter := range []bool{false, true} {
							q, args := f.query(parameter, tc.explicit, tc.config, tc.document, tc.filter)
							h := textSearchSettingHold(t, p, c, q)
							textSearchSettingRoutes(t, p, b, c, h, f.step(q, false, true, args...))
							h.Statement.Close()
						}
					}
					if f.match { return }
					for _, parameter := range []bool{false, true} {
						q, args := f.query(parameter, false, nil, nil, textSearchSettingFilter(f))
						q = strings.TrimSuffix(q, " AS normalized")
						s := f.step(q, false, true, args...)
						if !f.debug {
							label := f.name
							if f.json != nil { label = f.json.name }
							s.Columns = []string{label}
						}
						h := textSearchSettingHold(t, p, c, q)
						textSearchSettingRoutes(t, p, b, c, h, s)
						h.Statement.Close()
						if !stale {
							document := any("")
							if f.json != nil { document = `""` }
							q, args = f.query(parameter, false, nil, document, textSearchSettingFilter(f))
							var empty fixture.Step
							if f.debug {
								empty = fixture.Select(q, ftsDebugColumns, ftsDebugTypes, nil, args...)
							} else {
								empty = fixture.Select(q, []string{"normalized"}, []string{f.typ}, [][]any{{""}}, args...)
							}
							h = textSearchSettingHold(t, p, c, q)
							textSearchSettingRoutes(t, p, b, c, h, empty)
							h.Statement.Close()
						}
					}
					bindQuery, _ := f.query(false, false, nil, nil, textSearchSettingFilter(f))
					if f.json != nil {
						bindQuery = strings.Replace(bindQuery, "NULL::"+f.json.typ, "setting_missing_column", 1)
					} else {
						bindQuery = strings.Replace(bindQuery, "NULL::text", "setting_missing_column", 1)
					}
					bindDiagnostic := fixture.ControlsReferenceDiagnostic("42703", `column "setting_missing_column" does not exist`)
					bindDiagnostic.Position = fixture.Position(strings.Index(bindQuery, "setting_missing_column")+1)
					bindStep := fixture.ErrorDiagnostic(bindQuery, bindDiagnostic)
					bindStep.SelectRows = true
					run(bindStep)
					bindStep.ErrorStage = "prepare"
					run(fixture.Prepared(bindStep))
					// Literal REGCONFIG analysis and bound REGCONFIG input
					// conversion precede strict runtime NULL handling.
					for _, parameter := range []bool{false, true} {
						q, args := f.query(parameter, true, "setting_absent", nil, textSearchSettingFilter(f))
						q = "/* é */ "+q
						d := fixture.ControlsReferenceDiagnostic("42704", `text search configuration "setting_absent" does not exist`)
						if parameter { d.Context = "unnamed portal parameter $1 = '...'" } else { d.Position = fixture.Position(utf8.RuneCountInString(q[:strings.Index(q, "'setting_absent'")])+1) }
						s := fixture.ErrorDiagnostic(q, d, args...)
						s.SelectRows = true
						if parameter {
							s.ErrorStage = "execute"
							h := textSearchSettingHold(t, p, c, q)
							textSearchSettingRoutes(t, p, b, c, h, s)
							h.Statement.Close()
						} else {
							run(s)
							s.ErrorStage = "prepare"
							run(fixture.Prepared(s))
						}
					}
					q, args := f.query(true, true, "setting_absent", nil, textSearchSettingFilter(f))
					q = strings.Replace(q, "$1::regconfig", "$1::text::regconfig", 1)
					h := textSearchSettingHold(t, p, c, q)
					defer h.Statement.Close()
					// Strict scalar constructors suppress this runtime cast for
					// a NULL document. The non-strict SQL SRF ts_debug still
					// evaluates its arguments before returning zero rows.
					nullStep := f.step(q, false, true, args...)
					if f.debug {
						nullStep = fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("42704", `text search configuration "setting_absent" does not exist`), args...)
						nullStep.SelectRows, nullStep.ErrorStage = true, "execute"
					}
					textSearchSettingRoutes(t, p, b, c, h, nullStep)
					// Demand that same cast with a non-NULL document: its error
					// has neither literal Position nor portal-input Context.
					_, args = f.query(true, true, "setting_absent", f.input(), textSearchSettingFilter(f))
					s := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("42704", `text search configuration "setting_absent" does not exist`), args...)
					s.SelectRows, s.ErrorStage = true, "execute"
					textSearchSettingRoutes(t, p, b, c, h, s)
					if stale {
						// Explicit REGCONFIG input intentionally does not use the
						// semantic default's missing-schema diagnostic.
						q, args = f.query(true, true, "setting_absent_schema.cfg", nil, textSearchSettingFilter(f))
						h = textSearchSettingHold(t, p, c, q)
						defer h.Statement.Close()
						d := fixture.ControlsReferenceDiagnostic("42704", `text search configuration "setting_absent_schema.cfg" does not exist`)
						d.Context = "unnamed portal parameter $1 = '...'"
						s = fixture.ErrorDiagnostic(q, d, args...)
						s.SelectRows, s.ErrorStage = true, "execute"
						textSearchSettingRoutes(t, p, b, c, h, s)
					}
				})
			})
		}
	}
}
