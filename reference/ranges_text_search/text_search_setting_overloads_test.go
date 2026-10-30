package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTextSearchSettingOverloads(t *testing.T) {
	forms := append(textSearchSettingForms(), textSearchSettingMatches()...)
	for _, f := range forms {
		t.Run(f.name, func(t *testing.T) {
			textSearchSettingScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				run(fixture.Exec("SET default_text_search_config='simple'", 0))
				for _, parameter := range []bool{false, true} {
					q, args := f.query(parameter, false, nil, f.input(), textSearchSettingFilter(f))
					h := textSearchSettingHold(t, p, c, q)
					defer h.Statement.Close()
					for _, english := range []bool{false, true, false} {
						setting := "simple"
						if english { setting = "english" }
						run(fixture.Exec("SET default_text_search_config='"+setting+"'", 0))
						textSearchSettingRoutes(t, p, b, c, h, f.step(q, english, false, args...))
						if !f.match {
							eq, ea := f.query(parameter, true, "english", f.input(), textSearchSettingFilter(f))
							eh := textSearchSettingHold(t, p, c, eq)
							textSearchSettingRoutes(t, p, b, c, eh, f.step(eq, true, false, ea...))
							eh.Statement.Close()
						}
					}
				}
			})
		})
	}
}

func textSearchSettingFilter(f textSearchSettingForm) any {
	if f.match { return "rat" }
	return `"string"`
}

func TestReferenceTextSearchSettingQualification(t *testing.T) {
	for _, schema := range []string{"ts_setting_a", "ts_setting_b", "Ts Schema"} { fixture.SchemaReferenceCleanup(t, schema) }
	textSearchSettingScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, q := range []string{
			"CREATE SCHEMA ts_setting_a", "CREATE SCHEMA ts_setting_b", `CREATE SCHEMA "Ts Schema"`,
			"CREATE TEXT SEARCH CONFIGURATION ts_setting_a.cfg (COPY=pg_catalog.simple)",
			"CREATE TEXT SEARCH CONFIGURATION ts_setting_b.cfg (COPY=pg_catalog.english)",
			`CREATE TEXT SEARCH CONFIGURATION "Ts Schema"."Cfg.Mixed" (COPY=pg_catalog.simple)`,
			"SET search_path=ts_setting_a,pg_catalog", "SET default_text_search_config='cfg'",
		} { run(fixture.Exec(q, 0)) }
		defer c.ExecContext(p.Ctx, "RESET search_path")
		f := textSearchSettingForms()[0]
		q, args := f.query(true, false, nil, f.input(), nil)
		h := textSearchSettingHold(t, p, c, q)
		defer h.Statement.Close()
		check := func(value string, english bool) {
			run(textSearchSettingShow(value))
			run(fixture.Select("SELECT current_setting('default_text_search_config') AS setting", []string{"setting"}, []string{"TEXT"}, [][]any{{value}}))
			textSearchSettingRoutes(t, p, b, c, h, f.step(q, english, false, args...))
		}
		check("ts_setting_a.cfg", false)
		run(fixture.Exec("SET search_path=ts_setting_b,pg_catalog", 0))
		check("ts_setting_a.cfg", false)
		run(fixture.Exec("SET default_text_search_config='ts_setting_b.cfg'", 0))
		check("ts_setting_b.cfg", true)
		quoted := `"Ts Schema"."Cfg.Mixed"`
		run(fixture.Select("SELECT set_config('default_text_search_config',$1,false) AS setting", []string{"setting"}, []string{"TEXT"}, [][]any{{quoted}}, quoted))
		check(quoted, false)
		for _, dict := range []string{"english_stem", "simple"} {
			run(fixture.Exec(`ALTER TEXT SEARCH CONFIGURATION "Ts Schema"."Cfg.Mixed" ALTER MAPPING FOR asciiword WITH pg_catalog.`+dict, 0))
			check(quoted, dict == "english_stem")
		}
		for _, value := range []string{"setting_absent", "absent_schema.cfg", "", `"unterminated`} {
			d := fixture.ControlsReferenceDiagnostic("22023", `invalid value for parameter "default_text_search_config": "`+value+`"`)
			s := fixture.ErrorDiagnostic("SELECT set_config('default_text_search_config',$1,false)", d, value)
			s.SelectRows, s.ErrorStage = true, "execute"
			run(s)
			run(fixture.Prepared(s))
			check(quoted, false)
		}
		for _, tc := range []struct{ value, code, message string }{
			{"a.b.c.d", "42601", "improper qualified name (too many dotted names): a.b.c.d"},
			{"foreign_db.pg_catalog.english", "0A000", "cross-database references are not implemented: foreign_db.pg_catalog.english"},
		} {
			s := fixture.ErrorDiagnostic("SET default_text_search_config='"+tc.value+"'", fixture.ControlsReferenceDiagnostic(tc.code, tc.message))
			run(s)
			run(fixture.Prepared(s))
			check(quoted, false)
		}
	})
}
