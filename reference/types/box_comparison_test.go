package reference_test

import (
	"database/sql"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func TestReferenceBoxComparison(t *testing.T) {
	boxScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		ops := []string{"=", "<", "<=", ">", ">=", "~="}
		for _, tc := range []struct {
			left, right string
			want        []any
		}{
			{"2,2,0,0", "14,11,10,10", []any{true, false, true, false, true, false}},
			{"2,2,0,0", "0,0,2,2", []any{true, false, true, false, true, true}},
			{"2,2,0,0", "13,12,10,10", []any{false, true, true, false, false, false}},
			{"1,1,0,0", "1.0000005,1,0,0", []any{true, false, true, false, true, true}},
			{"1,1,0,0", "1.000002,1,0,0", []any{false, true, true, false, false, false}},
			{"NaN,1,0,0", "1,1,0,0", []any{false, false, false, false, false, false}},
			{"Infinity,1,0,0", "Infinity,1,0,0", []any{true, false, true, false, true, true}},
			{"Infinity,1,Infinity,0", "1,1,0,0", []any{false, false, false, false, false, false}},
		} {
			for i, op := range ops {
				q := "SELECT $1::box " + op + " $2::box AS v"
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				for _, args := range [][]any{{tc.left, tc.right}, {nil, tc.right}, {tc.left, nil}, {tc.left, tc.right}} {
					want := tc.want[i]
					if args[0] == nil || args[1] == nil {
						want = nil
					}
					s := fixture.Select(q, []string{"v"}, []string{"BOOL"}, [][]any{{want}}, args...)
					run(s)
					fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
				}
				stmt.Close()
				literal := "SELECT box " + boxQuote(tc.left) + " " + op + " box " + boxQuote(tc.right) + " AS v"
				run(fixture.Select(literal, []string{"v"}, []string{"BOOL"}, [][]any{{tc.want[i]}}))
			}
		}
		for _, q := range []string{
			"SELECT box '1e308,1,-1e308,0' = box '1,1,0,0'",
			"SELECT box '1e200,1e200,0,0' < box '1,1,0,0'",
		} {
			for _, prepared := range []bool{false, true} {
				run(boxError(q, "22003", "value out of range: overflow", "", "", prepared, true))
			}
		}
		for _, op := range []string{"<>", "!="} {
			q := "SELECT box '1,1,0,0' " + op + " box '1,1,0,0'"
			for _, prepared := range []bool{false, true} {
				run(boxError(q, "42883", "operator does not exist: box <> box", fixture.Position(strings.Index(q, op)+1), "No operator matches the given name and argument types. You might need to add explicit type casts.", prepared, false))
			}
		}
		q := "SELECT point '(1,2)' = point '(1,2)'"
		run(boxError(q, "42883", "operator does not exist: point = point", fixture.Position(strings.Index(q, "=")+1), "No operator matches the given name and argument types. You might need to add explicit type casts.", false, false))
		run(fixture.Exec("CREATE TABLE box_generic(b box)", 0))
		for _, populate := range []string{"", "INSERT INTO box_generic VALUES(NULL)", "INSERT INTO box_generic VALUES('1,1,0,0')"} {
			if populate != "" {
				run(fixture.Exec(populate, 1))
			}
			for _, tc := range []struct{ query, message, hint, token string }{
				{"SELECT b FROM box_generic ORDER BY b", "could not identify an ordering operator for type box", "Use an explicit ordering operator or modify the query.", "b"},
				{"SELECT DISTINCT b FROM box_generic", "could not identify an equality operator for type box", "", "b"},
				{"SELECT b FROM box_generic GROUP BY b", "could not identify an equality operator for type box", "", "b"},
			} {
				pos := strings.LastIndex(tc.query, tc.token)
				if strings.Contains(tc.query, "DISTINCT") {
					pos = strings.Index(tc.query, " b ") + 1
				}
				for _, prepared := range []bool{false, true} {
					run(boxError(tc.query, "42883", tc.message, fixture.Position(utf8.RuneCountInString(tc.query[:pos])+1), tc.hint, prepared, false))
				}
			}
		}
		for _, tc := range []struct{ query, code, message string }{
			{"SELECT (SELECT ROW(box '1,1,0,0'))=(SELECT ROW(box '1,1,0,0'))", "42883", "could not identify an equality operator for type box"},
			{"CREATE TABLE box_unique(b box UNIQUE)", "42704", "data type box has no default operator class for access method \"btree\""},
			{"CREATE INDEX box_generic_idx ON box_generic(b)", "42704", "data type box has no default operator class for access method \"btree\""},
		} {
			d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
			if tc.code == "42704" {
				d.Hint = "You must specify an operator class for the index or define a default operator class for the data type."
			}
			run(fixture.ErrorDiagnostic(tc.query, d))
		}
	})
}
