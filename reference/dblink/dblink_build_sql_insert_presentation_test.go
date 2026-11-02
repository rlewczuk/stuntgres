package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// Released get_sql_insert captures the relation spelling before source SQL,
// then renders column identifiers and SPI_getvalue output after source effects.
func TestReferenceDblinkBuildSQLInsertSourcePresentation(t *testing.T) {
	insertCase(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		defer func() {
			run(fixture.Exec("RESET DateStyle", 0))
			run(fixture.Exec("RESET quote_all_identifiers", 0))
		}()
		run(fixture.Exec("CREATE FUNCTION insert_presentation_value() RETURNS date LANGUAGE plpgsql VOLATILE AS $$BEGIN PERFORM set_config('DateStyle','SQL, DMY',false); PERFORM set_config('quote_all_identifiers','on',false); RETURN DATE '2024-03-09'; END$$", 0))
		run(fixture.Exec("CREATE VIEW insert_presentation AS SELECT 1 AS id,insert_presentation_value() AS copied_date", 0))
		held := insertHeld(t, p, c, insertCall)
		for _, tc := range []struct{ route, target, want string }{
			{"direct", "9", `INSERT INTO insert_presentation("id","copied_date") VALUES('9','09/03/2024')`},
			{"held-first", "10", `INSERT INTO insert_presentation("id","copied_date") VALUES('10','09/03/2024')`},
			{"held-reused", "11", `INSERT INTO insert_presentation("id","copied_date") VALUES('11','09/03/2024')`},
		} {
			t.Run(tc.route, func(t *testing.T) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				// Each invocation starts independently from the opposite presentation
				// settings; a held result cannot reuse the preceding generated text.
				run(fixture.Exec("SET DateStyle='ISO, MDY'", 0))
				run(fixture.Exec("SET quote_all_identifiers=off", 0))
				var x fixture.Executor = c
				if tc.route != "direct" {
					x = held
				}
				fixture.RunStep(t, p.Ctx, x, b.Recorder, insertSelect(insertCall, tc.want, "insert_presentation", "1", int64(1), "{1}", "{"+tc.target+"}"))
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT current_setting('DateStyle') AS date_style,current_setting('quote_all_identifiers') AS quote_all", []string{"date_style", "quote_all"}, []string{"TEXT", "TEXT"}, [][]any{{"SQL, DMY", "on"}}))
			})
		}
	})
}
