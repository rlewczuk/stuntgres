package reference_test

import (
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func inetOperatorError(t *testing.T, p *fixture.Pair, b fixture.Backend, q string, d fixture.Diagnostic, args ...any) {
	t.Helper()
	s := fixture.ErrorDiagnostic(q, d, args...)
	t.Run("direct", func(t *testing.T) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) })
	s = fixture.Prepared(s)
	// Immutable value errors occur at execution, unlike literal/signature analysis.
	s.ErrorStage = "execute"
	t.Run("prepared", func(t *testing.T) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) })
}

func TestReferenceINETOperatorDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		for _, tc := range []struct{ expression, code, message string }{
			{"inet '1.2.3.4' & inet '::1'", "22023", "cannot AND inet values of different sizes"},
			{"inet '::1' | inet '1.2.3.4'", "22023", "cannot OR inet values of different sizes"},
			{"inet '1.2.3.4' - inet '::1'", "22023", "cannot subtract inet values of different sizes"},
			{"inet '1.2.3.4' & inet '::ffff:1.2.3.4'", "22023", "cannot AND inet values of different sizes"},
			{"inet '::ffff:1.2.3.4' | inet '1.2.3.4'", "22023", "cannot OR inet values of different sizes"},
			{"inet '::ffff:1.2.3.4' - inet '1.2.3.4'", "22023", "cannot subtract inet values of different sizes"},
			{"inet '0.0.0.0' - 1", "22003", "result is out of range"},
			{"inet '255.255.255.255' + 1", "22003", "result is out of range"},
			{"inet '::' - 1", "22003", "result is out of range"},
			{"inet 'ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff' + 1", "22003", "result is out of range"},
			{"inet '1.2.3.4' + 10000000000::bigint", "22003", "result is out of range"},
			{"inet '1.2.3.4' + (-10000000000)::bigint", "22003", "result is out of range"},
			{"1::bigint + inet '255.255.255.255'", "22003", "result is out of range"},
		} {
			t.Run(b.Name+"/"+tc.expression, func(t *testing.T) {
				d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				if tc.expression == "1::bigint + inet '255.255.255.255'" {
					d.Context = "SQL function \"int8pl_inet\" statement 1"
				}
				inetOperatorError(t, p, b, "/*é*/ SELECT "+tc.expression, d)
			})
		}
		for _, tc := range []struct{ left, op, right, signature string }{
			{"1::bigint", "-", "inet '::1'", "bigint - inet"},
			{"inet '::1'", "+", "inet '::2'", "inet + inet"},
			{"inet '::1'", "+", "1::numeric", "inet + numeric"},
			{"inet '::1'", "&", "1::integer", "inet & integer"},
			{"inet '::1'", "|", "'::1'::text", "inet | text"},
			{"inet '::1'", "#", "inet '::2'", "inet # inet"},
			{"inet '::1'", "<<", "1", "inet << integer"},
			{"inet '::1'", ">>", "1", "inet >> integer"},
			{"", "+", "inet '::1'", "+ inet"},
		} {
			t.Run(b.Name+"/signature/"+tc.signature, func(t *testing.T) {
				prefix := "/*é*/ SELECT "
				if tc.left != "" {
					prefix += tc.left + " "
				}
				q := prefix + tc.op + " " + tc.right
				d := fixture.ControlsReferenceDiagnostic("42883", "operator does not exist: "+tc.signature)
				d.Position = fixture.Position(utf8.RuneCountInString(prefix) + 1)
				d.Hint = "No operator matches the given name and argument types. You might need to add explicit type casts."
				if tc.left == "" {
					d.Hint = "No operator matches the given name and argument type. You might need to add an explicit type cast."
				}
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.ErrorDiagnostic(q, d))
				s := fixture.Prepared(fixture.ErrorDiagnostic(q, d))
				s.ErrorStage = "prepare"
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			})
		}
		for _, tc := range []struct {
			expression, typ, code, message string
			good, bad                      []any
			value                          any
		}{
			{"$1::inet & $2::inet", "INET", "22023", "cannot AND inet values of different sizes", []any{"::3", "::1"}, []any{"1.2.3.4", "::1"}, "::1"},
			{"$1::inet | $2::inet", "INET", "22023", "cannot OR inet values of different sizes", []any{"::2", "::1"}, []any{"1.2.3.4", "::1"}, "::3"},
			{"$1::inet - $2::inet", "INT8", "22023", "cannot subtract inet values of different sizes", []any{"::3", "::1"}, []any{"1.2.3.4", "::1"}, int64(2)},
			{"$1::inet - $2::inet", "INT8", "22003", "result is out of range", []any{"::3", "::1"}, []any{"::8000:0:0:0", "::"}, int64(2)},
			{"$2::bigint + $1::inet", "INET", "22003", "result is out of range", []any{"::1", int64(1)}, []any{"::", int64(-1)}, "::2"},
		} {
			t.Run(b.Name+"/held/"+tc.expression+"/"+tc.code, func(t *testing.T) {
				q := "SELECT " + tc.expression + " AS v"
				stmt, err := b.DB.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}
				good := fixture.Select(q, []string{"v"}, []string{tc.typ}, [][]any{{tc.value}}, tc.good...)
				fixture.RunStep(t, p.Ctx, held, b.Recorder, good)
				d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				if tc.expression == "$2::bigint + $1::inet" {
					d.Context = "SQL function \"int8pl_inet\" statement 1"
				}
				inetOperatorError(t, p, b, q, d, tc.bad...)
				fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.ErrorDiagnostic(q, d, tc.bad...))
				fixture.RunStep(t, p.Ctx, held, b.Recorder, good)
			})
		}
		t.Run(b.Name+"/held-recovery-and-atomicity", func(t *testing.T) {
			q := "SELECT $1::inet + $2::bigint AS h"
			stmt, err := b.DB.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}
			for _, s := range []fixture.Step{
				fixture.Select(q, []string{"h"}, []string{"INET"}, [][]any{{"::2"}}, "::1", int64(1)),
				fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("22003", "result is out of range"), "::", int64(-1)),
				fixture.Select(q, []string{"h"}, []string{"INET"}, [][]any{{"::3"}}, []byte("::2"), int64(1)),
				fixture.Select(q, []string{"h"}, []string{"INET"}, [][]any{{nil}}, nil, int64(1)),
			} {
				fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			}
			d := inetDiagnostic("inet", "bad", false)
			d.Context = "unnamed portal parameter $1 = '...'"
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.ErrorDiagnostic(q, d, "bad", int64(1)))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"h"}, []string{"INET"}, [][]any{{"::4"}}, "::3", int64(1)))
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE operator_atomic(id INTEGER,h INET)", 0))
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO operator_atomic VALUES(1,'::1')", 1))
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic("INSERT INTO operator_atomic VALUES(2,$1::inet + 1),(3,$2::inet + 1)", fixture.ControlsReferenceDiagnostic("22003", "result is out of range"), "::1", "ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff")
				if prepared {
					s = fixture.Prepared(s)
					s.ErrorStage = "execute"
				}
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT id,h FROM operator_atomic ORDER BY id", []string{"id", "h"}, []string{"INT4", "INET"}, [][]any{{int64(1), "::1"}}))
			}
		})
	}
}

func TestReferenceINETOperatorReleaseEdges(t *testing.T) {
	p := fixture.NewScenario(t)
	// Source-derived A/B expectations confirmed by the pinned PostgreSQL18.0 oracle.
	for _, b := range p.Backends() {
		for i, tc := range []struct {
			expression, typ string
			value           any
			fails           bool
		}{
			{"inet '::' - (-9223372036854775808)::bigint", "INET", nil, true},
			{"inet '::8000:0:0:0' - (-9223372036854775808)::bigint", "INET", "::", false},
			{"inet '::8000:0:0:0' + (-9223372036854775808)::bigint", "INET", "::", false},
			{"(-9223372036854775808)::bigint + inet '::8000:0:0:0'", "INET", "::", false},
			{"inet '::' + (-9223372036854775808)::bigint", "INET", nil, true},
			{"inet '::' - (-9223372036854775807)::bigint", "INET", "::7fff:ffff:ffff:ffff", false},
			{"inet '0.0.0.0' - (-9223372036854775808)::bigint", "INET", nil, true},
			{"inet '255.255.255.255' + (-9223372036854775808)::bigint", "INET", nil, true},
			{"inet 'ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff' - inet '::'", "INT8", int64(-1), false},
			{"inet '::' - inet 'ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff'", "INT8", int64(1), false},
			{"inet 'ffff:ffff:ffff:ffff:ffff:ffff:ffff:fffe' - inet '::'", "INT8", int64(-2), false},
			{"inet '::' - inet 'ffff:ffff:ffff:ffff:ffff:ffff:ffff:fffe'", "INT8", int64(2), false},
			{"inet '::7fff:ffff:ffff:ffff' - inet '::'", "INT8", int64(9223372036854775807), false},
			{"inet '::' - inet '::8000:0:0:0'", "INT8", int64(-9223372036854775808), false},
			{"inet '::8000:0:0:0' - inet '::'", "INT8", nil, true},
			{"inet '::' - inet '::8000:0:0:1'", "INT8", nil, true},
		} {
			t.Run(fmt.Sprintf("%s/%02d", b.Name, i), func(t *testing.T) {
				if tc.fails {
					inetOperatorError(t, p, b, "SELECT "+tc.expression+" AS v", fixture.ControlsReferenceDiagnostic("22003", "result is out of range"))
				} else {
					inetOperatorScalar(t, p, b, tc.expression, tc.typ, tc.value)
				}
			})
		}
		for _, op := range []string{"+", "-"} {
			t.Run(b.Name+"/held-min/"+op, func(t *testing.T) {
				q := "SELECT $1::inet " + op + " $2::bigint AS h"
				stmt, err := b.DB.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}
				fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"h"}, []string{"INET"}, [][]any{{"::"}}, "::8000:0:0:0", int64(-9223372036854775808)))
				fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("22003", "result is out of range"), "::", int64(-9223372036854775808)))
				want := "::3"
				if op == "-" {
					want = "::1"
				}
				fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"h"}, []string{"INET"}, [][]any{{want}}, "::2", int64(1)))
			})
		}
	}
}

func TestReferenceINETOperatorErrorFrontier(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE inet_operator_empty(h INET)", 0))
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE inet_operator_rows(h INET)", 0))
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO inet_operator_rows VALUES('1.2.3.4')", 1))
		for _, tc := range []struct{ expression, typ, code, message string }{
			{"inet '1.2.3.4' & inet '::1'", "INET", "22023", "cannot AND inet values of different sizes"},
			{"inet '1.2.3.4' | inet '::1'", "INET", "22023", "cannot OR inet values of different sizes"},
			{"inet '255.255.255.255' + 1::bigint", "INET", "22003", "result is out of range"},
			{"inet '::8000:0:0:0' - inet '::'", "INT8", "22003", "result is out of range"},
		} {
			for _, tail := range []string{" WHERE FALSE", " FROM inet_operator_empty", " FROM inet_operator_rows WHERE FALSE"} {
				t.Run(b.Name+"/constant/"+tc.expression+tail, func(t *testing.T) {
					inetOperatorError(t, p, b, "SELECT "+tc.expression+" AS v"+tail, fixture.ControlsReferenceDiagnostic(tc.code, tc.message))
				})
			}
		}
		for _, expression := range []string{"h & inet '::1'", "h | inet '::1'", "h + 10000000000::bigint", "h - inet '::'"} {
			for _, tail := range []string{" FROM inet_operator_empty", " FROM inet_operator_rows WHERE FALSE"} {
				t.Run(b.Name+"/row/"+expression+tail, func(t *testing.T) {
					typ := "INET"
					if expression == "h - inet '::'" {
						typ = "INT8"
					}
					s := fixture.Select("SELECT "+expression+" AS v"+tail, []string{"v"}, []string{typ}, nil)
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(s))
				})
			}
		}
		for _, tc := range []struct {
			expression, typ, code, message string
			args                           []any
		}{
			{"$1::inet & inet '::1'", "INET", "22023", "cannot AND inet values of different sizes", []any{"1.2.3.4"}},
			{"$1::inet | inet '::1'", "INET", "22023", "cannot OR inet values of different sizes", []any{"1.2.3.4"}},
			{"$1::inet + 1::bigint", "INET", "22003", "result is out of range", []any{"255.255.255.255"}},
			{"$1::inet - inet '::'", "INT8", "22003", "result is out of range", []any{"::8000:0:0:0"}},
		} {
			for _, tail := range []string{" WHERE FALSE", " FROM inet_operator_empty"} {
				t.Run(b.Name+"/parameter/"+tc.expression+tail, func(t *testing.T) {
					q := "SELECT " + tc.expression + " AS v" + tail
					d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
					inetOperatorError(t, p, b, q, d, tc.args...)
					t.Run("held", func(t *testing.T) {
						stmt, err := b.DB.PrepareContext(p.Ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
						held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}
						good := fixture.Select(q, []string{"v"}, []string{tc.typ}, nil, "::1")
						fixture.RunStep(t, p.Ctx, held, b.Recorder, good)
						fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.ErrorDiagnostic(q, d, tc.args...))
						fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"v"}, []string{tc.typ}, nil, nil))
						fixture.RunStep(t, p.Ctx, held, b.Recorder, good)
					})
				})
			}
		}
		for _, tc := range []struct{ expression, typ string }{
			{"NULL::inet & inet '::1'", "INET"}, {"inet '1.2.3.4' | NULL::inet", "INET"},
			{"inet '255.255.255.255' + NULL::bigint", "INET"}, {"NULL::inet - inet '::8000:0:0:0'", "INT8"},
		} {
			t.Run(b.Name+"/null/"+tc.expression, func(t *testing.T) { inetOperatorScalar(t, p, b, tc.expression, tc.typ, nil) })
		}
		for _, expression := range []string{"NULL::inet & inet 'bad'", "inet 'bad' | NULL::inet"} {
			t.Run(b.Name+"/malformed/"+expression, func(t *testing.T) {
				q := "/*é*/ SELECT " + expression
				d := inetDiagnostic("inet", "bad", false)
				d.Position = fixture.Position(utf8.RuneCountInString(q[:strings.Index(q, "'bad'")]) + 1)
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.ErrorDiagnostic(q, d))
				s := fixture.Prepared(fixture.ErrorDiagnostic(q, d))
				s.ErrorStage = "prepare"
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			})
		}
		t.Run(b.Name+"/null-child-error", func(t *testing.T) {
			inetOperatorError(t, p, b, "SELECT NULL::inet + (1::bigint / 0::bigint)", fixture.ControlsReferenceDiagnostic("22012", "division by zero"))
		})
	}
}
