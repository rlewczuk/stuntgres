package reference_test

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceIntarraySortDiagnostics(t *testing.T) {
	// A NULL first argument does not hide a demanded conversion in the
	// second expression. Unlike a parameter input failure, this conversion
	// runs inside the expression and has no portal-parameter Context.
	t.Run("strict_demanded_child", func(t *testing.T) {
		intarrayScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			intarrayInstall(t, p, b, c)
			q := "SELECT ia_provider.sort($1::integer[],($2::text)::integer::text) AS v"
			want := fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type integer: "x"`)
			for _, prepared := range []bool{false, true} {
				intarrayError(t, p, b, c, q, prepared, "execute", want, nil, "x")
			}
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			bad := fixture.ErrorDiagnostic(q, want, nil, "x")
			bad.SelectRows, bad.ErrorStage = true, "execute"
			fixture.RunStep(t, p.Ctx, held, b.Recorder, bad)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, intarraySortResult(b, q, nil, nil, "1"))
		})
	})
	t.Run("matrix", func(t *testing.T) {
		intarrayScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			intarrayInstall(t, p, b, c)
			for _, tc := range intarraySortCases {
				t.Run(tc.label, func(t *testing.T) {
					q := "SELECT " + tc.expression(tc.parameters(true)) + " AS v"
					for i := range tc.args {
						for _, null := range []any{nil, []byte(nil)} {
							args := append([]any(nil), tc.args...)
							args[i] = null
							intarrayBoth(t, p, b, c, intarraySortResult(b, q, nil, args...))
						}
						args := append([]any(nil), tc.args...)
						args[i] = nil
						intarrayBoth(t, p, b, c, intarraySortResult(b, "SELECT "+tc.expression(tc.literals(args))+" AS v", nil))
						if i > 0 {
							args[0] = "{1,NULL}"
							intarrayBoth(t, p, b, c, intarraySortResult(b, q, nil, args...))
							intarrayBoth(t, p, b, c, intarraySortResult(b, "SELECT "+tc.expression(tc.literals(args))+" AS v", nil))
						}
					}
					for i, array := range []string{"{NULL}", "{1,NULL}", "{{1,NULL},{2,3}}"} {
						t.Run("null_elements_"+strconv.Itoa(i), func(t *testing.T) {
							args := append([]any(nil), tc.args...)
							args[0] = array
							if len(args) > 1 {
								args[1] = "invalid"
							}
							for _, prepared := range []bool{false, true} {
								intarrayError(t, p, b, c, q, prepared, "execute", intarraySetNullError(), args...)
								intarrayError(t, p, b, c, "SELECT "+tc.expression(tc.literals(args))+" AS v", prepared, "execute", intarraySetNullError())
							}
						})
					}
					for _, bad := range []struct{ value, code, message string }{
						{"{x}", "22P02", `invalid input syntax for type integer: "x"`},
						{"{2147483648}", "22003", `value "2147483648" is out of range for type integer`},
					} {
						t.Run("conversion_"+bad.code, func(t *testing.T) {
							args := append([]any(nil), tc.args...)
							args[0] = bad.value
							literal := "SELECT " + tc.expression(tc.literals(args)) + " AS v"
							for _, prepared := range []bool{false, true} {
								// Complete records and preparation stages confirmed
								// against the pinned PostgreSQL 18.6 oracle.
								want := fixture.ControlsReferenceDiagnostic(bad.code, bad.message)
								want.Position = strconv.Itoa(strings.Index(literal, "'"+bad.value+"'") + 1)
								stage := "execute"
								if prepared {
									stage = "prepare"
								}
								intarrayError(t, p, b, c, literal, prepared, stage, want)
								want.Position, want.Context = "", "unnamed portal parameter $1 = '...'"
								intarrayError(t, p, b, c, q, prepared, "execute", want, args...)
								if len(args) > 1 {
									args[1] = nil
									intarrayError(t, p, b, c, q, prepared, "execute", want, args...)
								}
							}
						})
					}
				})
			}
			for i, direction := range []string{"", " asc", "asc ", "desc\n", "ascending", "descending", "invalid", "ＡＳＣ", "deſc"} {
				t.Run("invalid_direction_"+strconv.Itoa(i), func(t *testing.T) {
					q := "SELECT ia_provider.sort($1::integer[],$2::text) AS v"
					literal := "SELECT ia_provider.sort('{7,6}'::integer[]," + intarraySetLiteral(direction, "text") + ") AS v"
					for _, prepared := range []bool{false, true} {
						intarrayError(t, p, b, c, q, prepared, "execute", intarraySortDirectionError(), "{7,6}", direction)
						intarrayError(t, p, b, c, literal, prepared, "execute", intarraySortDirectionError())
					}
					for _, input := range []string{"{}", "{7}"} {
						intarrayBoth(t, p, b, c, intarraySortResult(b, q, input, input, direction))
					}
				})
			}
			for i, tc := range []struct{ query, signature string }{
				{"SELECT ia_provider.sort()", "ia_provider.sort()"},
				{"SELECT ia_provider.sort(1)", "ia_provider.sort(integer)"},
				{"SELECT ia_provider.sort('{1}'::integer[],1)", "ia_provider.sort(integer[], integer)"},
				{"SELECT ia_provider.sort('{1}'::integer[],'asc'::text,1)", "ia_provider.sort(integer[], text, integer)"},
				{"SELECT ia_provider.sort_asc('{1}'::integer[],'asc'::text)", "ia_provider.sort_asc(integer[], text)"},
				{"SELECT ia_provider.sort_desc('x'::text)", "ia_provider.sort_desc(text)"},
				{"SELECT ia_provider.uniq('{1}'::integer[],1)", "ia_provider.uniq(integer[], integer)"},
			} {
				t.Run("wrong_overload_"+strconv.Itoa(i), func(t *testing.T) {
					want := fixture.ControlsReferenceDiagnostic("42883", "function "+tc.signature+" does not exist")
					want.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
					want.Position = "8"
					for _, prepared := range []bool{false, true} {
						stage := "execute"
						if prepared {
							stage = "prepare"
						}
						intarrayError(t, p, b, c, tc.query, prepared, stage, want)
					}
				})
			}
		})
	})
	t.Run("statement_atomicity", func(t *testing.T) {
		intarrayScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			intarraySortSeed(t, p, b, c)
			intarraySortDefaults(t, p, b, c)
			intarraySortAtomicity(t, p, b, c)
		})
	})
}

func intarraySortAtomicity(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	original := intarraySelect(b, "SELECT id,a FROM ia_sort_rows ORDER BY id", []string{"id", "a"}, []string{"INT4", "_INT4"}, []bool{false, true}, [][]any{{int64(1), intarraySortA}, {int64(2), "{}"}, {int64(3), nil}})
	for _, operation := range []string{"sort", "uniq"} {
		for _, insert := range []bool{false, true} {
			expr := "ia_provider." + operation + "((CASE WHEN id=1 THEN $1::text ELSE $2::text END)::integer[])"
			q := "UPDATE ia_sort_rows SET a=" + expr + " WHERE id<3"
			if insert {
				q = "INSERT INTO ia_sort_rows(id,a) SELECT id+10," + expr + " FROM ia_sort_rows WHERE id<3"
			}
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			for _, args := range [][]any{{"{NULL}", "{4,4,2}"}, {"{4,4,2}", "{NULL}"}} {
				for _, prepared := range []bool{false, true} {
					intarrayError(t, p, b, c, q, prepared, "execute", intarraySetNullError(), args...)
					intarrayBoth(t, p, b, c, original)
				}
				s := fixture.ErrorDiagnostic(q, intarraySetNullError(), args...)
				s.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
				intarrayBoth(t, p, b, c, original)
			}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 2, "{4,4,2}", "{8,8,1}"))
			first, second := "{2,4,4}", "{1,8,8}"
			if operation == "uniq" {
				first, second = "{4,2}", "{8,1}"
			}
			id1, id2, suffix := int64(1), int64(2), " WHERE id<3 ORDER BY id"
			if insert {
				id1, id2, suffix = 11, 12, " WHERE id>10 ORDER BY id"
			}
			intarrayBoth(t, p, b, c, intarraySelect(b, "SELECT id,a FROM ia_sort_rows"+suffix, []string{"id", "a"}, []string{"INT4", "_INT4"}, []bool{false, true}, [][]any{{id1, first}, {id2, second}}))
			reset := fixture.Exec("UPDATE ia_sort_rows SET a=CASE WHEN id=1 THEN '{3,1,1,2}'::integer[] ELSE '{}'::integer[] END WHERE id<3", 2)
			if insert {
				reset = fixture.Exec("DELETE FROM ia_sort_rows WHERE id>10", 2)
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, reset)
			if err := stmt.Close(); err != nil {
				t.Fatal(err)
			}
			intarrayBoth(t, p, b, c, original)
		}
	}
}
