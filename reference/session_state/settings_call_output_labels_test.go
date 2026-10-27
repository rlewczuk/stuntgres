package reference_test

import (
	"database/sql"
	"errors"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceSettingsCallOutputLabelsSyntax(t *testing.T) {
	labels := []struct{ suffix, name string }{
		{"", ""}, {" AS value", "value"}, {" value", "value"},
		{" AS MiXeD", "mixed"}, {" MiXeD", "mixed"},
		{` AS "MiXeD"`, "MiXeD"}, {` "MiXeD"`, "MiXeD"},
		{` AS "Étiquette"`, "Étiquette"}, {` "Étiquette"`, "Étiquette"},
		{` AS "a""b"`, `a"b`},
		{" AS from", "from"}, {` "from"`, "from"},
		{" AS on", "on"}, {` "on"`, "on"},
		{" AS day", "day"}, {` "day"`, "day"},
		{" AS filter", "filter"}, {` "filter"`, "filter"},
		{" select", "select"}, {" check", "check"},
		{" like", "like"}, {" ilike", "ilike"}, {" similar", "similar"},
	}
	for _, call := range []struct{ name, arguments, want string }{
		{"current_setting", "'application_name'", "label-base"},
		{"current_setting", "'application_name',false", "label-base"},
		{"set_config", "'application_name','label-set',false", "label-set"},
	} {
		for _, schema := range []string{"", "pg_catalog."} {
			for i, label := range labels {
				t.Run(fmt.Sprintf("%s%s/%s/%d", schema, call.name, call.arguments, i), func(t *testing.T) {
					settingsLabelScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, owner, peer *sql.Conn) {
						fixture.RunStep(t, p.Ctx, owner, b.Recorder, fixture.Exec("SET application_name='label-base'", 0))
						query := "SELECT " + schema + call.name + "(" + call.arguments + ")" + label.suffix
						name := label.name
						if name == "" {
							name = call.name
						}
						fixture.RunStep(t, p.Ctx, owner, b.Recorder, settingsLabelValue(b.PG, query, name, call.want))
						fixture.RunStep(t, p.Ctx, owner, b.Recorder, fixture.Exec(query, 1))
						fixture.RunStep(t, p.Ctx, owner, b.Recorder, settingsLabelValue(b.PG, "SHOW application_name", "application_name", call.want))
					})
				})
			}
		}
	}
	// Representative parameterized spellings supplement the complete literal
	// matrix without multiplying every effect and diagnostic by every spelling.
	for _, route := range []string{"direct", "fresh", "held"} {
		t.Run(route, func(t *testing.T) {
			settingsLabelScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, owner, peer *sql.Conn) {
				for _, tc := range []struct {
					query, label string
					args         []any
					want         any
				}{
					{`SELECT current_setting($1) "Étiquette"`, "Étiquette", []any{"application_name"}, "label-base"},
					{`SELECT pg_catalog.current_setting($1,$2) MiXeD`, "mixed", []any{"application_name", false}, "label-base"},
					{`SELECT set_config($1,$2,$3) AS "a""b"`, `a"b`, []any{"application_name", "label-set", false}, "label-set"},
					{`SELECT current_setting($1) like`, "like", []any{"application_name"}, "label-base"},
					{`SELECT pg_catalog.current_setting($1,$2) ilike`, "ilike", []any{"application_name", false}, "label-base"},
					{`SELECT pg_catalog.set_config($1,$2,$3) similar`, "similar", []any{"application_name", "label-set", false}, "label-set"},
				} {
					fixture.RunStep(t, p.Ctx, owner, b.Recorder, fixture.Exec("SET application_name='label-base'", 0))
					run := settingsLabelRoute(t, p.Ctx, owner, b.Recorder, route, tc.query)
					run(settingsLabelValue(b.PG, tc.query, tc.label, tc.want, tc.args...))
					if route == "held" {
						run(settingsLabelValue(b.PG, tc.query, tc.label, tc.want, tc.args...))
					}
				}
			})
		})
	}
}

func TestReferenceSettingsCallOutputLabelsMetadataAndFreshness(t *testing.T) {
	for _, route := range []string{"direct", "fresh", "held"} {
		t.Run(route, func(t *testing.T) {
			settingsLabelScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, owner, peer *sql.Conn) {
				var capturedDefault, peerDefault string
				if err := owner.QueryRowContext(p.Ctx, "SHOW application_name").Scan(&capturedDefault); err != nil {
					t.Fatal(err)
				}
				if err := peer.QueryRowContext(p.Ctx, "SHOW application_name").Scan(&peerDefault); err != nil {
					t.Fatal(err)
				}
				read := `SELECT pg_catalog.current_setting($1) AS "ReadValue"`
				optional := `SELECT pg_catalog.current_setting($1,$2) value`
				write := `SELECT pg_catalog.set_config($1,$2,$3) AS "SetValue"`
				runRead := settingsLabelRoute(t, p.Ctx, owner, b.Recorder, route, read)
				runOptional := settingsLabelRoute(t, p.Ctx, owner, b.Recorder, route, optional)
				runWrite := settingsLabelRoute(t, p.Ctx, owner, b.Recorder, route, write)
				for _, value := range []string{"label-one", "label-two"} {
					fixture.RunStep(t, p.Ctx, owner, b.Recorder, fixture.Exec("SET application_name='"+value+"'", 0))
					runRead(settingsLabelValue(b.PG, read, "ReadValue", value, "application_name"))
				}
				runRead(settingsLabelValue(b.PG, read, "ReadValue", nil, nil))
				for _, tc := range []struct {
					args []any
					want any
				}{
					{[]any{"application_name", false}, "label-two"},
					{[]any{"label_missing", true}, nil},
					{[]any{"application_name", nil}, nil}, {[]any{nil, false}, nil},
				} {
					runOptional(settingsLabelValue(b.PG, optional, "value", tc.want, tc.args...))
				}
				for _, value := range []string{"label-one", "label-two"} {
					runWrite(settingsLabelValue(b.PG, write, "SetValue", value, "application_name", value, false))
					settingsLabelState(t, p, b, owner, peer, value, peerDefault)
				}
				runWrite(settingsLabelValue(b.PG, write, "SetValue", capturedDefault, "application_name", nil, nil))
				settingsLabelState(t, p, b, owner, peer, capturedDefault, peerDefault)
				// Wrong counts are shared adapter errors. Native rejects named
				// arguments; lib/pq instead ignores names when encoding values.
				stmt, err := owner.PrepareContext(p.Ctx, write)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				invalidArgs := [][]any{{"application_name"}, {"application_name", "x", false, false}}
				if !b.PG {
					invalidArgs = append(invalidArgs, []any{sql.Named("name", "application_name"), "x", false})
				}
				for _, args := range invalidArgs {
					rows, err := stmt.QueryContext(p.Ctx, args...)
					if rows != nil {
						rows.Close()
						t.Fatal("argument failure returned rows")
					}
					if err == nil {
						t.Fatal("invalid adapter arguments accepted")
					}
					if len(args) == 3 && err.Error() != "stuntgres: named parameters are not supported" {
						t.Fatal("named parameter diagnostic", err)
					}
					var state interface{ SQLState() string }
					if errors.As(err, &state) {
						t.Fatal("adapter error fabricated SQLSTATE", err)
					}
					settingsLabelState(t, p, b, owner, peer, capturedDefault, peerDefault)
				}
			})
		})
	}
}
