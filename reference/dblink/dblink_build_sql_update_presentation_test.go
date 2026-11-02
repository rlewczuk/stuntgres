package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// Released get_sql_update captures the relation spelling before source SQL,
// then renders column identifiers and SPI_getvalue output after source effects.
func TestReferenceDblinkBuildSQLUpdateSourcePresentation(t *testing.T) {
	t.Run("date-and-identifiers", func(t *testing.T) {
		updateCase(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			defer func() {
				run(fixture.Exec("RESET DateStyle", 0))
				run(fixture.Exec("RESET quote_all_identifiers", 0))
			}()
			run(fixture.Exec("CREATE FUNCTION update_presentation_value() RETURNS date LANGUAGE plpgsql VOLATILE AS $$BEGIN PERFORM set_config('DateStyle','SQL, DMY',false); PERFORM set_config('quote_all_identifiers','on',false); RETURN DATE '2024-03-09'; END$$", 0))
			run(fixture.Exec("CREATE VIEW update_presentation AS SELECT 1 AS id,update_presentation_value() AS copied_date", 0))
			held := updateHeld(t, p, c, updateCall)
			for _, tc := range []struct{ route, target, want string }{
				{"direct", "9", `UPDATE update_presentation SET "id" = '9', "copied_date" = '09/03/2024' WHERE "id" = '9'`},
				{"held-first", "10", `UPDATE update_presentation SET "id" = '10', "copied_date" = '09/03/2024' WHERE "id" = '10'`},
				{"held-reused", "11", `UPDATE update_presentation SET "id" = '11', "copied_date" = '09/03/2024' WHERE "id" = '11'`},
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
					fixture.RunStep(t, p.Ctx, x, b.Recorder, updateSelect(updateCall, tc.want, "update_presentation", "1", int64(1), "{1}", "{"+tc.target+"}"))
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT current_setting('DateStyle') AS date_style,current_setting('quote_all_identifiers') AS quote_all", []string{"date_style", "quote_all"}, []string{"TEXT", "TEXT"}, [][]any{{"SQL, DMY", "on"}}))
				})
			}
		})
	})
	t.Run("search-path-effect", func(t *testing.T) {
		updateCase(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			defer c.ExecContext(p.Ctx, "RESET search_path")
			for _, q := range []string{
				"CREATE SCHEMA dbupd_left", "CREATE SCHEMA dbupd_right",
				"CREATE FUNCTION public.update_path_value() RETURNS text LANGUAGE plpgsql VOLATILE AS $$BEGIN PERFORM set_config('search_path','dbupd_right, public',false); RETURN 'local'; END$$",
				"CREATE VIEW dbupd_left.update_path AS SELECT 1 AS id,public.update_path_value() AS payload",
				"CREATE TABLE dbupd_right.update_path(id integer,payload text)",
			} {
				run(fixture.Exec(q, 0))
			}
			run(fixture.Exec("INSERT INTO dbupd_right.update_path VALUES(1,'decoy')", 1))
			held := updateHeld(t, p, c, updateCall)
			for _, route := range []string{"direct", "prepared", "held-first", "held-reused"} {
				run(fixture.Exec("SET search_path=dbupd_left,dbupd_right,public", 0))
				s := updateSelect(updateCall, "UPDATE update_path SET id = '9', payload = 'local' WHERE id = '9'", "update_path", "1", int64(1), "{1}", "{9}")
				s.Prepared = route == "prepared"
				var x fixture.Executor = c
				if route == "held-first" || route == "held-reused" {
					x = held
				}
				fixture.RunStep(t, p.Ctx, x, b.Recorder, s)
				run(fixture.Select("SELECT current_setting('search_path') AS path", []string{"path"}, []string{"TEXT"}, [][]any{{"dbupd_right, public"}}))
				run(fixture.Select("SELECT id,payload FROM dbupd_right.update_path ORDER BY id", []string{"id", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "decoy"}}))
			}
		})
	})
}
