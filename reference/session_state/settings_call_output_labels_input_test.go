package reference_test

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func TestReferenceSettingsCallOutputLabelsCompleteInput(t *testing.T) {
	// Character positions are specified from the literal source, never native
	// diagnostics. The pinned oracle resolves any release-source contradiction.
	for _, call := range []string{
		`current_setting('application_name')`,
		`current_setting('application_name',false)`,
		`set_config('application_name','x',false)`,
	} {
		for _, schema := range []string{"", "pg_catalog."} {
			for _, suffix := range []struct{ text, token, message string }{
				{" AS", "", "syntax error at end of input"},
				{" AS 1", "1", `syntax error at or near "1"`},
				{" day", "day", `syntax error at or near "day"`},
				{" on", "on", `syntax error at or near "on"`},
				{" filter", "", "syntax error at end of input"},
				{" from", "", "syntax error at end of input"},
				{` AS ""`, `""`, `zero-length delimited identifier at or near """"`},
				{" AS value trailing", "trailing", `syntax error at or near "trailing"`},
				{" like )", ")", `syntax error at or near ")"`},
				{" ilike )", ")", `syntax error at or near ")"`},
				{" similar )", ")", `syntax error at or near ")"`},
			} {
				for _, prefix := range []string{"", "/*é*/ "} {
					query := prefix + "SELECT " + schema + call + suffix.text
					position := utf8.RuneCountInString(query) + 1
					if suffix.token != "" {
						position = utf8.RuneCountInString(query[:strings.LastIndex(query, suffix.token)]) + 1
					}
					t.Run(query, func(t *testing.T) {
						settingsLabelScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, owner, peer *sql.Conn) {
							fixture.RunStep(t, p.Ctx, owner, b.Recorder, fixture.Exec("SET application_name='label-sentinel'", 0))
							fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Exec("SET application_name='label-peer'", 0))
							for _, prepared := range []bool{false, true} {
								s := settingsLabelFailure(query, fixture.Diagnostic{Code: "42601", Message: suffix.message, Position: strconv.Itoa(position)})
								s.Prepared = prepared
								if prepared {
									s.ErrorStage = "prepare"
								}
								fixture.RunStep(t, p.Ctx, owner, b.Recorder, s)
								settingsLabelState(t, p, b, owner, peer, "label-sentinel", "label-peer")
							}
						})
					})
				}
			}
		}
	}
	t.Run("whole-message", func(t *testing.T) {
		settingsLabelScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, owner, peer *sql.Conn) {
			fixture.RunStep(t, p.Ctx, owner, b.Recorder, fixture.Exec("SET application_name='label-sentinel'", 0))
			fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Exec("SET application_name='label-peer'", 0))
			bad := `SELECT set_config('application_name','x',false) AS value; SELECT )`
			for _, prepared := range []bool{false, true} {
				s := settingsLabelFailure(bad, fixture.Diagnostic{Code: "42601", Message: `syntax error at or near ")"`, Position: strconv.Itoa(utf8.RuneCountInString(bad))})
				s.Prepared = prepared
				if prepared {
					s.ErrorStage = "prepare"
				}
				fixture.RunStep(t, p.Ctx, owner, b.Recorder, s)
				settingsLabelState(t, p, b, owner, peer, "label-sentinel", "label-peer")
			}
			batch := `SELECT set_config('application_name','batch',false) AS first; SELECT current_setting('application_name') AS second`
			rows, err := owner.QueryContext(p.Ctx, batch)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			for i, label := range []string{"first", "second"} {
				if i != 0 && !rows.NextResultSet() {
					t.Fatal("missing result set", rows.Err())
				}
				settingsLabelMetadata(t, rows, b.PG, label, true)
				fixture.BatchReadSet(t, rows, fixture.BatchSet{Columns: []string{label}, Types: []string{"TEXT"}, Rows: [][]any{{"batch"}}})
				if rows.Err() != nil {
					t.Fatal(rows.Err())
				}
			}
			if rows.NextResultSet() || rows.Err() != nil {
				t.Fatal("extra batch result", rows.Err())
			}
			if err := rows.Close(); err != nil {
				t.Fatal(err)
			}
			settingsLabelState(t, p, b, owner, peer, "batch", "label-peer")
			for i, tc := range []struct {
				query    string
				args     []any
				prepared bool
			}{
				{batch, nil, true},
				{`SELECT set_config($1,$2,$3) AS first; SELECT current_setting('application_name') AS second`, []any{"application_name", "lost", false}, false},
			} {
				t.Run(fmt.Sprint("single-command/", i), func(t *testing.T) {
					s := settingsLabelFailure(tc.query, fixture.Diagnostic{Code: "42601", Message: "cannot insert multiple commands into a prepared statement"}, tc.args...)
					s.Prepared = tc.prepared
					if tc.prepared {
						s.ErrorStage = "prepare"
					}
					fixture.RunStep(t, p.Ctx, owner, b.Recorder, s)
					settingsLabelState(t, p, b, owner, peer, "batch", "label-peer")
				})
			}
		})
	})
}
