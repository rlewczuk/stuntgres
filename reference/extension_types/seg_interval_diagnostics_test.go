package reference_test

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func segIntervalPosition(prefix string) string {
	return strconv.Itoa(utf8.RuneCountInString(prefix) + 1)
}

var segIntervalFailures = []struct{ input, code, message, detail string }{
	{"ABC", "42601", "bad seg representation", `syntax error at or near "A"`},
	{"5 .. 2", "22023", "swapped boundaries: 5 is greater than 2", ""},
	{"1e700", "22003", `"1e700" is out of range for type real`, ""},
}

func TestReferenceSegIntervalDiagnostics(t *testing.T) {
	for _, tc := range segIntervalFailures {
		t.Run(tc.code, func(t *testing.T) {
			segScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				for _, op := range segIntervalOperators {
					for _, prepared := range []bool{false, true} {
						for _, other := range []string{"'1'::seg", "NULL::seg"} {
							for _, right := range []bool{false, true} {
								prefix := "/*é*/ SELECT "
								if right {
									prefix += other + " " + op + " "
								}
								q := prefix + queryIntLiteral(tc.input) + "::seg"
								if !right {
									q += " " + op + " " + other
								}
								want := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
								want.Detail = tc.detail
								want.Position = segIntervalPosition(prefix)
								queryIntError(t, p, b, c, q, prepared, true, true, want)
							}
						}
						for _, args := range [][2]any{{tc.input, "1"}, {"1", tc.input}, {tc.input, nil}, {nil, tc.input}} {
							parameter := 1
							if args[1] == tc.input {
								parameter = 2
							}
							want := queryIntParameterDiagnostic(tc.code, tc.message, tc.input, parameter)
							want.Detail = tc.detail
							queryIntError(t, p, b, c, "SELECT $1::seg "+op+" $2::seg", prepared, true, false, want, args[0], args[1])
						}
					}
				}
			})
		})
	}
	t.Run("signatures", func(t *testing.T) {
		segScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			functionHint := "No function matches the given name and argument types. You might need to add explicit type casts."
			operatorHint := "No operator matches the given name and argument types. You might need to add explicit type casts."
			for _, tc := range []struct{ q, code, message, hint, token string }{
				{"SELECT '1'::seg && 1", "42883", "operator does not exist: seg && integer", operatorHint, "&&"},
				{"SELECT NULL::seg && 1", "42883", "operator does not exist: seg && integer", operatorHint, "&&"},
				{"SELECT '1'::seg && '1'::text", "42883", "operator does not exist: seg && text", operatorHint, "&&"},
				{"SELECT seg_overlap('1'::seg)", "42883", "function seg_overlap(seg) does not exist", functionHint, "seg_overlap"},
				{"SELECT seg_overlap('1'::text,'1'::seg)", "42883", "function seg_overlap(text, seg) does not exist", functionHint, "seg_overlap"},
				{"SELECT seg_interval_missing.seg_overlap('1'::seg,'1'::seg)", "3F000", `schema "seg_interval_missing" does not exist`, "", "seg_interval_missing"},
			} {
				want := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				want.Hint = tc.hint
				want.Position = segIntervalPosition(tc.q[:strings.Index(tc.q, tc.token)])
				for _, prepared := range []bool{false, true} {
					queryIntError(t, p, b, c, tc.q, prepared, true, true, want)
				}
			}
			for i, op := range segIntervalOperators {
				if op == "&&" {
					continue
				} // The complete && records are specified above.
				q := "SELECT NULL::seg " + op + " 1"
				want := fixture.ControlsReferenceDiagnostic("42883", "operator does not exist: seg "+op+" integer")
				want.Hint = operatorHint
				want.Position = segIntervalPosition("SELECT NULL::seg ")
				for _, prepared := range []bool{false, true} {
					queryIntError(t, p, b, c, q, prepared, true, true, want)
				}
				q = "SELECT " + segIntervalRoutines[i] + "('1'::seg)"
				want = fixture.ControlsReferenceDiagnostic("42883", "function "+segIntervalRoutines[i]+"(seg) does not exist")
				want.Hint = functionHint
				want.Position = "8"
				for _, prepared := range []bool{false, true} {
					queryIntError(t, p, b, c, q, prepared, true, true, want)
				}
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET search_path=pg_catalog", 0))
			for _, tc := range []struct{ q, message, token, hint string }{
				{"SELECT seg_overlap('1'::public.seg,'1'::public.seg)", "function seg_overlap(public.seg, public.seg) does not exist", "seg_overlap", functionHint},
				{"SELECT '1'::public.seg && '1'::public.seg", "operator does not exist: public.seg && public.seg", "&&", operatorHint},
			} {
				want := fixture.ControlsReferenceDiagnostic("42883", tc.message)
				want.Hint = tc.hint
				want.Position = segIntervalPosition(tc.q[:strings.Index(tc.q, tc.token)])
				for _, prepared := range []bool{false, true} {
					queryIntError(t, p, b, c, tc.q, prepared, true, true, want)
				}
			}
		})
	})
}

func TestReferenceSegIntervalAtomicity(t *testing.T) {
	for _, tc := range segIntervalFailures {
		t.Run(tc.code, func(t *testing.T) {
			segScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				run(fixture.Exec("CREATE TABLE seg_interval_atomic(id integer,hit boolean)", 0))
				run(fixture.Exec("INSERT INTO seg_interval_atomic VALUES (1,true),(2,false)", 2))
				baseline := fixture.Select("SELECT id,hit FROM seg_interval_atomic ORDER BY id", []string{"id", "hit"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}, {int64(2), false}})
				for _, prepared := range []bool{false, true} {
					prefix := "/*é*/ INSERT INTO seg_interval_atomic VALUES (3,'1'::seg && '1'::seg),(4,"
					want := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
					want.Detail = tc.detail
					want.Position = segIntervalPosition(prefix)
					queryIntError(t, p, b, c, prefix+queryIntLiteral(tc.input)+"::seg && '1'::seg)", prepared, false, true, want)
					run(baseline)
					want = queryIntParameterDiagnostic(tc.code, tc.message, tc.input, 2)
					want.Detail = tc.detail
					queryIntError(t, p, b, c, "INSERT INTO seg_interval_atomic VALUES (3,$1::seg && '1'::seg),(4,$2::seg && '1'::seg)", prepared, false, false, want, "1", tc.input)
					run(baseline)
					queryIntError(t, p, b, c, "UPDATE seg_interval_atomic SET hit=(CASE WHEN id=1 THEN $1::seg ELSE $2::seg END) && '1'::seg", prepared, false, false, want, "1", tc.input)
					run(baseline)
				}
			})
		})
	}
}
