package reference_test

import (
	"database/sql"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func TestReferenceLsegDiagnostics(t *testing.T) {
	lsegScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE lseg_errors(id integer,s lseg)", 0))
		cases := []struct{ input, code, message string }{}
		for _, input := range []string{"", " ", "bad", "1,2,3", "1,2,3,4,5", "[(1,2)]", "[(1;2),(3,4)]", "[(1,2),(3,4)]junk"} {
			cases = append(cases, struct{ input, code, message string }{input, "22P02", "invalid input syntax for type lseg: \"" + input + "\""})
		}
		for _, tc := range [][2]string{{"1e400,2,3,4", "1e400"}, {"1e-4000,2,3,4", "1e-4000"}, {"[(1e400;2),(3,4)]", "1e400"}} {
			cases = append(cases, struct{ input, code, message string }{tc[0], "22003", "\"" + tc[1] + "\" is out of range for type double precision"})
		}
		for _, tc := range cases {
			t.Run(tc.input, func(t *testing.T) {
				base := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				for _, q := range []string{
					"SELECT lseg " + boxQuote(tc.input), "SELECT " + boxQuote(tc.input) + "::lseg", "SELECT pg_catalog.lseg " + boxQuote(tc.input),
					"SELECT /*é*/ lseg " + boxQuote(tc.input), "SELECT CAST(" + boxQuote(tc.input) + " AS lseg)",
					"SELECT " + boxQuote(tc.input) + "::text::lseg", "SELECT " + boxQuote(tc.input) + "::lseg FROM lseg_errors WHERE FALSE",
					"INSERT INTO lseg_errors VALUES(1," + boxQuote(tc.input) + ")",
				} {
					runtime := strings.Contains(q, "::text::")
					for _, prepared := range []bool{false, true} {
						for _, queryMode := range []bool{false, true} {
							d := base
							if !runtime {
								d.Position = fixture.Position(1 + utf8.RuneCountInString(q[:strings.Index(q, "'")]))
							}
							s := fixture.ErrorDiagnostic(q, d)
							s.Prepared, s.SelectRows, s.ErrorStage = prepared, queryMode, "execute"
							if prepared && !runtime {
								s.ErrorStage = "prepare"
							}
							fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
						}
					}
				}
				q := "SELECT $1::lseg AS s"
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				d := base
				d.Context = "unnamed portal parameter $1 = '...'"
				if tc.input == "" {
					d.Context = "unnamed portal parameter $1 = ''"
				}
				for _, arg := range []any{tc.input, []byte(tc.input)} {
					for _, prepared := range []bool{false, true} {
						for _, queryMode := range []bool{false, true} {
							s := fixture.ErrorDiagnostic(q, d, arg)
							s.Prepared, s.SelectRows, s.ErrorStage = prepared, queryMode, "execute"
							fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
						}
					}
				}
				s := fixture.ErrorDiagnostic(q, d, tc.input)
				s.SelectRows, s.ErrorStage = true, "execute"
				fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, held, b.Recorder, boxSelect(b, q, []string{"s"}, []string{"LSEG"}, [][]any{{nil}}, nil))
				fixture.RunStep(t, p.Ctx, held, b.Recorder, boxSelect(b, q, []string{"s"}, []string{"LSEG"}, [][]any{{"[(3,2),(1,4)]"}}, "3,2,1,4"))
			})
		}
		run(fixture.Exec("INSERT INTO lseg_errors VALUES(1,'1,4,3,2')", 1))
		check := func() {
			run(boxSelect(b, "SELECT id,s FROM lseg_errors ORDER BY id", []string{"id", "s"}, []string{"INT4", "LSEG"}, [][]any{{int64(1), "[(1,4),(3,2)]"}}))
		}
		for _, prepared := range []bool{false, true} {
			d := fixture.ControlsReferenceDiagnostic("22P02", "invalid input syntax for type lseg: \"bad\"")
			d.Context = "unnamed portal parameter $2 = '...'"
			s := fixture.ErrorDiagnostic("INSERT INTO lseg_errors VALUES(2,$1),(3,$2)", d, "1,1,0,0", "bad")
			s.Prepared, s.ErrorStage = prepared, "execute"
			run(s)
			check()
			q := "INSERT INTO lseg_errors VALUES(2,'1,1,0,0'),(3,'bad')"
			run(boxError(q, "22P02", "invalid input syntax for type lseg: \"bad\"", fixture.Position(strings.Index(q, "'bad'")+1), "", prepared, false))
			check()
		}
		mismatch := "No operator matches the given name and argument types. You might need to add explicit type casts."
		for _, tc := range []struct{ q, code, message, token, hint string }{
			{"SELECT NULL::lseg ~= NULL::lseg", "42883", "operator does not exist: lseg ~= lseg", "~=", mismatch},
			{"SELECT NULL::lseg = 1", "42883", "operator does not exist: lseg = integer", "=", mismatch},
			{"SELECT NULL::lseg && NULL::lseg", "42883", "operator does not exist: lseg && lseg", "&&", mismatch},
			{"SELECT s FROM lseg_errors WHERE FALSE ORDER BY s", "42883", "could not identify an ordering operator for type lseg", "s", "Use an explicit ordering operator or modify the query."},
			{"SELECT DISTINCT s FROM lseg_errors WHERE FALSE", "42883", "could not identify an equality operator for type lseg", "s", ""},
			{"SELECT s FROM lseg_errors WHERE FALSE GROUP BY s", "42883", "could not identify an equality operator for type lseg", "s", ""},
			{"CREATE INDEX lseg_errors_idx ON lseg_errors(s)", "42704", "data type lseg has no default operator class for access method \"btree\"", "", "You must specify an operator class for the index or define a default operator class for the data type."},
			{"CREATE TABLE lseg_unique(s lseg UNIQUE)", "42704", "data type lseg has no default operator class for access method \"btree\"", "", "You must specify an operator class for the index or define a default operator class for the data type."},
		} {
			t.Run(tc.q, func(t *testing.T) {
				position := ""
				if tc.token != "" {
					index := strings.Index(tc.q, tc.token)
					if strings.Contains(tc.q, "ORDER BY") || strings.Contains(tc.q, "GROUP BY") {
						index = strings.LastIndex(tc.q, tc.token)
					}
					position = fixture.Position(index + 1)
				}
				// Utility statements defer default-opclass lookup to execution,
				// including when the statement was successfully prepared.
				runtime := tc.code == "42704"
				for _, prepared := range []bool{false, true} {
					fixture.RunStep(t, p.Ctx, c, b.Recorder, boxError(tc.q, tc.code, tc.message, position, tc.hint, prepared, runtime))
				}
			})
		}
	})
}
