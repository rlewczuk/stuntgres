package reference_test

import (
	"database/sql"
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTimezoneViewsParameterBoundaries(t *testing.T) {
	timezoneViewsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor) {
		for _, view := range []string{"pg_timezone_names", "pg_timezone_abbrevs"} {
			label := "abbrev"
			if view == "pg_timezone_names" {
				label = "name"
			}
			for _, tc := range []struct {
				predicate string
				args      []any
			}{
				{label + "=$1::text", []any{nil}},
				{"utc_offset=$1::interval", []any{nil}},
				{"is_dst=$1::boolean", []any{nil}},
				{"utc_offset=NULL::interval", nil},
				{"is_dst=NULL::boolean", nil},
			} {
				query := "SELECT * FROM pg_catalog." + view + " WHERE " + tc.predicate
				s := timezoneViewsAbbrevs(query, [][]any{}, tc.args...)
				if view == "pg_timezone_names" {
					s = timezoneViewsNames(query, [][]any{}, tc.args...)
				}
				s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { timezoneViewsMetadata(t, rows, b.PG) }
				for _, prepared := range []bool{false, true} {
					t.Run(query+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
						s.Prepared = prepared
						fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					})
				}
			}
			for _, column := range []string{label, "is_dst"} {
				query := "SELECT * FROM pg_catalog." + view + " WHERE " + column + "=$1::integer"
				typeName := "text"
				if column == "is_dst" {
					typeName = "boolean"
				}
				want := fixture.Diagnostic{Code: "42883", Message: "operator does not exist: " + typeName + " = integer", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts.", Position: strconv.Itoa(strings.Index(query, "=") + 1)}
				for _, prepared := range []bool{false, true} {
					t.Run(query+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
						s := fixture.ImplicitError(query, want, 1)
						s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
						if prepared {
							s.ErrorStage = "prepare"
						}
						fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					})
				}
			}
		}
	})
}
