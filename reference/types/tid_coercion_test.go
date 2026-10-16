package reference_test

import (
	"strconv"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTIDCoercion(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			for _, prepared := range []bool{false, true} {
				for _, source := range []string{"text", "varchar(12)", "char(12)"} {
					for _, tc := range []struct {
						expr string
						args []any
						want any
					}{
						{"'(12,34)'::" + source, nil, "(12,34)"},
						{"$1::" + source, []any{"(12,34)"}, "(12,34)"},
						{"NULL::" + source, nil, nil},
						{"$1::" + source, []any{nil}, nil},
					} {
						s := fixture.Select("SELECT CAST("+tc.expr+" AS tid) AS v", []string{"v"}, []string{"TID"}, [][]any{{tc.want}}, tc.args...)
						s.Prepared = prepared
						run(s)
					}
				}
				for _, target := range []struct {
					sql, database string
					want          any
				}{
					{"text", "TEXT", "(12,34)"},
					{"varchar(9)", "VARCHAR", "(12,34)"},
					{"varchar(7)", "VARCHAR", "(12,34)"},
					{"varchar(3)", "VARCHAR", "(12"},
					{"char(9)", "BPCHAR", "(12,34)  "},
					{"char(7)", "BPCHAR", "(12,34)"},
					{"char(3)", "BPCHAR", "(12"},
				} {
					for _, source := range []struct {
						expr string
						args []any
						want any
					}{
						{"'(12,34)'::tid", nil, target.want},
						{"$1::tid", []any{"(12,34)"}, target.want},
						{"NULL::tid", nil, nil},
						{"$1::tid", []any{nil}, nil},
					} {
						s := fixture.Select("SELECT CAST("+source.expr+" AS "+target.sql+") AS v", []string{"v"}, []string{target.database}, [][]any{{source.want}}, source.args...)
						s.Prepared = prepared
						run(s)
					}
				}
			}
			for _, target := range []struct{ name, sql, database, want, overflow string }{
				{"text", "text", "TEXT", "(12,34)", ""},
				{"varchar", "varchar(9)", "VARCHAR", "(12,34)", ""},
				{"char", "char(9)", "BPCHAR", "(12,34)  ", ""},
				{"varchar_short", "varchar(3)", "VARCHAR", "(12", "value too long for type character varying(3)"},
				{"char_short", "char(3)", "BPCHAR", "(12", "value too long for type character(3)"},
			} {
				table := "tid_coerce_" + target.name
				run(fixture.Exec("CREATE TABLE "+table+"(id integer,v "+target.sql+")", 0))
				for i, prepared := range []bool{false, true} {
					id := i * 10
					for j, source := range []struct {
						expr string
						args []any
					}{{"'(12,34)'::tid", nil}, {"$1::tid", []any{"(12,34)"}}} {
						query := "INSERT INTO " + table + " VALUES(" + strconv.Itoa(id+j+1) + "," + source.expr + ")"
						s := fixture.Exec(query, 1, source.args...)
						if target.overflow != "" {
							s = fixture.ErrorDiagnostic(query, fixture.ControlsReferenceDiagnostic("22001", target.overflow), source.args...)
							s.ErrorStage = "execute"
						}
						s.Prepared = prepared
						run(s)
					}
					// Explicit sizing truncates before assignment; the same unsized
					// TID above must fail rather than silently truncate on assignment.
					if target.overflow != "" {
						s := fixture.Exec("INSERT INTO "+table+" VALUES($1,CAST('(12,34)'::tid AS "+target.sql+"))", 1, id+3)
						s.Prepared = prepared
						run(s)
					}
					for j, source := range []struct {
						expr string
						args []any
					}{{"NULL::tid", nil}, {"$2::tid", []any{id + 5, nil}}} {
						query := "INSERT INTO " + table + " VALUES($1," + source.expr + ")"
						args := source.args
						if j == 0 {
							args = []any{id + 4}
						}
						s := fixture.Exec(query, 1, args...)
						s.Prepared = prepared
						run(s)
					}
				}
				var want [][]any
				for range 2 {
					if target.overflow == "" {
						want = append(want, []any{target.want}, []any{target.want})
					} else {
						want = append(want, []any{target.want})
					}
					want = append(want, []any{nil}, []any{nil})
				}
				for _, prepared := range []bool{false, true} {
					s := fixture.Select("SELECT v FROM "+table+" ORDER BY id", []string{"v"}, []string{target.database}, want)
					s.Prepared = prepared
					run(s)
				}
			}
			// Typed character input is explicit IO, never an implicit equality edge.
			for _, source := range []string{"text", "varchar(12)", "char(12)"} {
				name := source
				if source == "varchar(12)" {
					name = "character varying"
				}
				if source == "char(12)" {
					name = "character"
				}
				d := fixture.ControlsReferenceDiagnostic("42883", "operator does not exist: tid = "+name)
				d.Hint = "No operator matches the given name and argument types. You might need to add explicit type casts."
				d.Position = "23"
				for _, prepared := range []bool{false, true} {
					s := fixture.ErrorDiagnostic("SELECT '(12,34)'::tid = '(12,34)'::"+source, d)
					s.SelectRows = true
					s.Prepared = prepared
					if prepared {
						s.ErrorStage = "prepare"
					}
					run(s)
				}
			}
		})
	}
}
