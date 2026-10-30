package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTextSearchSettingFeatureSmoke(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "ts_setting_missing")
	textSearchSettingScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE SCHEMA ts_setting_missing", 0))
		run(fixture.Exec("CREATE TEXT SEARCH CONFIGURATION ts_setting_missing.cfg (COPY=pg_catalog.simple)", 0))
		run(fixture.Exec("SET default_text_search_config='ts_setting_missing.cfg'", 0))
		run(textSearchSettingShow("ts_setting_missing.cfg"))
		held := make([]fixture.HeldStatement, 0)
		forms := textSearchSettingForms()
		for _, f := range forms {
			q, args := f.query(true, false, nil, f.input(), textSearchSettingFilter(f))
			h := textSearchSettingHold(t, p, c, q)
			defer h.Statement.Close()
			held = append(held, h)
			textSearchSettingRoutes(t, p, b, c, h, f.step(q, false, false, args...))
		}
		run(fixture.Exec("SET default_text_search_config='english'", 0))
		for i, f := range forms {
			_, args := f.query(true, false, nil, f.input(), textSearchSettingFilter(f))
			textSearchSettingRoutes(t, p, b, c, held[i], f.step(held[i].SQL, true, false, args...))
		}
		run(ftsConvertValue("SELECT to_tsvector('simple'::regconfig,'The Rats') AS normalized", "to_tsvector", "'rats':2 'the':1"))
		run(fixture.Exec("SET default_text_search_config='ts_setting_missing.cfg'", 0))
		run(fixture.Exec("DROP TEXT SEARCH CONFIGURATION ts_setting_missing.cfg", 0))
		for _, q := range []string{"SELECT to_tsvector('The Rats'::text)", "SELECT to_tsvector('{}'::json)", "SELECT jsonb_to_tsvector('{}'::jsonb,'[]'::jsonb)"} {
			h := textSearchSettingHold(t, p, c, q)
			s := fixture.ErrorDiagnostic(q, textSearchSettingMissingDiagnostic(false, false))
			s.SelectRows, s.ErrorStage = true, "execute"
			textSearchSettingRoutes(t, p, b, c, h, s)
			h.Statement.Close()
		}
		run(ftsConvertValue("SELECT to_tsvector('english'::regconfig,'The Rats') AS normalized", "to_tsvector", "'rat':2"))
		run(fixture.Select("SELECT to_tsvector(NULL::text)", []string{"to_tsvector"}, []string{"TSVECTOR"}, [][]any{{nil}}))
		run(fixture.Select("SELECT * FROM ts_debug(NULL::text)", ftsDebugColumns, ftsDebugTypes, nil))
		run(fixture.Select("SELECT to_tsvector(0::regconfig,'{}'::json)", []string{"to_tsvector"}, []string{"TSVECTOR"}, [][]any{{""}}))
		d := fixture.ControlsReferenceDiagnostic("22023", "wrong flag type, only arrays and scalars are allowed")
		s := fixture.ErrorDiagnostic("SELECT json_to_tsvector('{}'::json,'{}'::jsonb)", d)
		s.SelectRows, s.ErrorStage = true, "execute"
		run(s)
		run(fixture.Exec("CREATE TEXT SEARCH CONFIGURATION ts_setting_missing.cfg (COPY=pg_catalog.english)", 0))
		for i, f := range forms {
			_, args := f.query(true, false, nil, f.input(), textSearchSettingFilter(f))
			textSearchSettingRoutes(t, p, b, c, held[i], f.step(held[i].SQL, true, false, args...))
		}
		run(fixture.Exec("DROP SCHEMA ts_setting_missing CASCADE", 0))
		q := held[0].SQL
		s = fixture.ErrorDiagnostic(q, textSearchSettingMissingDiagnostic(true, false), "The Rats")
		s.SelectRows, s.ErrorStage = true, "execute"
		textSearchSettingRoutes(t, p, b, c, held[0], s)
	})
}
