package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// Source-derived semantic records, grounded by the pinned PostgreSQL discovery
// in tests-1.md/tests-2.md. Unset fields remain empty and are compared in full;
// TEXT-call positions/context are not borrowed from alias-cast routes.
func callableDiagnostic(code, message string) fixture.Diagnostic {
	return fixture.ControlsReferenceDiagnostic(code, message)
}

func callableErrorStep(q string, want fixture.Diagnostic, args ...any) fixture.Step {
	s := fixture.ErrorDiagnostic(q, want, args...)
	s.SelectRows, s.ErrorStage = true, "execute"
	return s
}

func callableHardRoutes(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, name, input, valid, output, family string, want fixture.Diagnostic) {
	t.Helper()
	q := "SELECT " + name + "($1) AS value"
	held := regLookupHeld(t, p, c, q)
	defer held.Statement.Close()
	for _, route := range []struct {
		name, query    string
		prepared, held bool
		args           []any
	}{
		{"literal", "SELECT " + name + "(" + regObjectLiteral(input) + ") AS value", false, false, nil},
		{"literal_prepared", "SELECT " + name + "(" + regObjectLiteral(input) + ") AS value", true, false, nil},
		{"text", q, false, false, []any{input}},
		{"text_prepared", q, true, false, []any{input}},
		{"text_held", q, false, true, []any{input}},
	} {
		t.Run(route.name, func(t *testing.T) {
			s := callableErrorStep(route.query, want, route.args...)
			s.Prepared = route.prepared
			if route.held {
				regObjectRunStep(t, p, b, held, s)
			} else {
				regObjectRunStep(t, p, b, c, s)
			}
		})
	}
	// This execution happens even after a child subtest's diagnostic mismatch.
	for _, input := range []any{[]byte(valid), nil, valid} {
		var value any = output
		if input == nil {
			value = nil
		}
		regObjectRunStep(t, p, b, held, callableSelect(b, q, []string{"value"}, []string{family}, [][]any{{value}}, input))
	}
}

func TestReferenceRegLookupCallableDiagnostics(t *testing.T) {
	regLookupScenario(t, nil, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, kind := range callableKinds {
			t.Run(kind.name+"/soft", func(t *testing.T) {
				inputs := []string{"a..b", "", " \t ", "4294967296", "08"}
				switch kind.name {
				case "to_regproc":
					inputs = append(inputs, "call_missing", "call_missing_schema.boolin", "abs", "pg_catalog.abs")
				case "to_regoper":
					inputs = append(inputs, "@#@#", "call_missing_schema.||/", "+", "pg_catalog.+")
				case "to_regprocedure":
					inputs = append(inputs, "now", "now(", "abs(integer,)", "abs(call_missing_type)", "abs(text)", "call_missing_schema.now()", "abs(integer[)")
				case "to_regoperator":
					inputs = append(inputs, "+", "+(integer)", "+(integer,integer,integer)", "+(call_missing_type,integer)", "+(text,boolean)", "call_missing_schema.+(int,int)")
				}
				q := "SELECT " + kind.name + "($1) AS value"
				held := regLookupHeld(t, p, c, q)
				defer held.Statement.Close()
				for _, input := range inputs {
					t.Run(input, func(t *testing.T) {
						s := callableSelect(b, q, []string{"value"}, []string{kind.family}, [][]any{{nil}}, input)
						regObjectBoth(t, p, b, c, s)
						regObjectRunStep(t, p, b, held, s)
						regObjectBoth(t, p, b, c, callableSelect(b, "SELECT "+kind.name+"("+regObjectLiteral(input)+") AS value", []string{"value"}, []string{kind.family}, [][]any{{nil}}))
					})
				}
			})
			for _, tc := range []struct{ suffix, code, message string }{
				{"otherdb.public.target", "0A000", "cross-database references are not implemented: otherdb.public.target"},
				{"x.y.z.w", "42601", "improper qualified name (too many dotted names): x.y.z.w"},
			} {
				input := tc.suffix
				if kind.name == "to_regprocedure" {
					input += "()"
				}
				if kind.name == "to_regoperator" {
					input += "(integer,integer)"
				}
				t.Run(kind.name+"/"+tc.code, func(t *testing.T) {
					callableHardRoutes(t, p, b, c, kind.name, input, kind.input, kind.output, kind.family, callableDiagnostic(tc.code, tc.message))
				})
				if kind.family == "REGPROC" || kind.family == "REGPROCEDURE" {
					t.Run(kind.name+"/"+tc.code+"/shared_cast", func(t *testing.T) {
						alias := strings.ToLower(kind.family)
						want := callableDiagnostic(tc.code, tc.message)
						want.Position = "8"
						callableAnalysisError(t, p, b, c, "SELECT "+regObjectLiteral(input)+"::"+alias, want)
						want.Position = ""
						q := "SELECT $1::text::" + alias
						cast := regLookupHeld(t, p, c, q)
						defer cast.Statement.Close()
						s := callableErrorStep(q, want, input)
						regObjectBoth(t, p, b, c, s)
						regObjectRunStep(t, p, b, cast, s)
					})
				}
			}
		}
		for _, tc := range []struct{ name, input, family, valid, output, code, message, position, context string }{
			{"to_regprocedure", "abs(integer(4))", "REGPROCEDURE", "abs(integer)", "abs(integer)", "42601", `syntax error at or near "("`, "8", `invalid type name "integer(4)"`},
			{"to_regprocedure", "abs(numeric(0))", "REGPROCEDURE", "abs(integer)", "abs(integer)", "22023", "NUMERIC precision 0 must be between 1 and 1000", "", ""},
			{"to_regoperator", "+(numeric(0),integer)", "REGOPERATOR", "+(int,int)", "+(integer,integer)", "22023", "NUMERIC precision 0 must be between 1 and 1000", "", ""},
			{"to_regoperator", "+(integer(4),integer)", "REGOPERATOR", "+(int,int)", "+(integer,integer)", "42601", `syntax error at or near "("`, "8", `invalid type name "integer(4)"`},
		} {
			t.Run(tc.name+"/"+tc.input, func(t *testing.T) {
				want := callableDiagnostic(tc.code, tc.message)
				// Both callable kinds expose the decoded type's character offset
				// and context identically on all five observed TEXT-call routes.
				want.Position, want.Context = tc.position, tc.context
				callableHardRoutes(t, p, b, c, tc.name, tc.input, tc.valid, tc.output, tc.family, want)
			})
		}
		// Throwing aliases retain their ambiguity diagnostics; NULL precedence
		// belongs only to the lookup builtins.
		for _, tc := range []struct{ alias, input, message string }{
			{"regproc", "abs", `more than one function named "abs"`},
			{"regoper", "+", "more than one operator named +"},
		} {
			t.Run("cast/"+tc.alias, func(t *testing.T) {
				q := "SELECT " + regObjectLiteral(tc.input) + "::" + tc.alias
				want := fixture.ControlsReferenceDiagnostic("42725", tc.message)
				want.Position = "8"
				callableAnalysisError(t, p, b, c, q, want)
				want.Position = ""
				q = "SELECT $1::text::" + tc.alias
				regObjectBoth(t, p, b, c, callableErrorStep(q, want, tc.input))
			})
		}
		// Child evaluation occurs outside destination-input recovery. The
		// integer conversion is runtime TEXT input, not a portal alias cast.
		for _, kind := range callableKinds {
			t.Run(kind.name+"/child", func(t *testing.T) {
				q := "SELECT " + kind.name + "(($1::text::integer)::text) AS value"
				held := regLookupHeld(t, p, c, q)
				defer held.Statement.Close()
				want := fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type integer: "call_bad_integer"`)
				for _, x := range []fixture.Executor{c, held} {
					regObjectRunStep(t, p, b, x, callableErrorStep(q, want, "call_bad_integer"))
					regObjectRunStep(t, p, b, x, callableSelect(b, q, []string{"value"}, []string{kind.family}, [][]any{{kind.output}}, kind.oid))
				}
			})
		}
		regObjectExec(t, p, b, c, "CREATE TABLE call_atomic(p regprocedure)")
		insert := "INSERT INTO call_atomic VALUES(to_regprocedure($1)),(to_regprocedure($2))"
		for _, prepared := range []bool{false, true} {
			t.Run(map[bool]string{false: "atomic_direct", true: "atomic_prepared"}[prepared], func(t *testing.T) {
				s := fixture.ErrorDiagnostic(insert, callableDiagnostic("22023", "NUMERIC precision 0 must be between 1 and 1000"), "abs(integer)", "abs(numeric(0))")
				s.Prepared, s.ErrorStage = prepared, "execute"
				regObjectRunStep(t, p, b, c, s)
			})
			regObjectBoth(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM call_atomic", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		}
		t.Run("transaction", func(t *testing.T) {
			tx, err := c.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			regObjectRunStep(t, p, b, tx, callableSelect(b, "SELECT to_regproc('abs') AS value", []string{"value"}, []string{"REGPROC"}, [][]any{{nil}}))
			regObjectRunStep(t, p, b, tx, callableSelect(b, "SELECT to_regproc('boolin') AS value", []string{"value"}, []string{"REGPROC"}, [][]any{{"boolin"}}))
			// Keep the aborted-transaction control reachable if discovery needs
			// to correct the hard error's still-unknown optional fields.
			t.Run("hard", func(t *testing.T) {
				regObjectRunStep(t, p, b, tx, callableErrorStep("SELECT to_regprocedure($1)", callableDiagnostic("22023", "NUMERIC precision 0 must be between 1 and 1000"), "abs(numeric(0))"))
			})
			regObjectRunStep(t, p, b, tx, callableErrorStep("SELECT to_regproc('boolin')", fixture.ControlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")))
		})
		if !b.PG {
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			rows, err := c.QueryContext(ctx, "SELECT to_regproc($1)", "boolin")
			if rows != nil {
				rows.Close()
				t.Error("pre-cancel exposed rows")
			}
			var state interface{ SQLState() string }
			if !errors.Is(err, context.Canceled) || errors.As(err, &state) {
				t.Fatalf("native cancellation must remain ordinary context error: %T %v", err, err)
			}
		}
	})
}

// Public regression is present before the shared parser's operand101 correction.
func TestReferenceRegLookupCallableParserPrecedence(t *testing.T) {
	regLookupScenario(t, nil, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		prefix := "now(" + strings.Repeat("integer,", 100)
		for _, tc := range []struct{ name, tail, code, message string }{
			{"hard_modifier", "numeric(0)", "22023", "NUMERIC precision 0 must be between 1 and 1000"},
			{"valid_limit", "integer", "54023", "too many arguments"},
			{"missing_type", "call_missing_type", "42704", `type "call_missing_type" does not exist`},
		} {
			t.Run(tc.name, func(t *testing.T) {
				input := prefix + tc.tail + ")"
				if tc.code == "22023" {
					callableHardRoutes(t, p, b, c, "to_regprocedure", input, "now()", "now()", "REGPROCEDURE", callableDiagnostic(tc.code, tc.message))
				} else {
					q := "SELECT to_regprocedure($1) AS value"
					held := regLookupHeld(t, p, c, q)
					defer held.Statement.Close()
					regObjectBoth(t, p, b, c, callableSelect(b, "SELECT to_regprocedure("+regObjectLiteral(input)+") AS value", []string{"value"}, []string{"REGPROCEDURE"}, [][]any{{nil}}))
					s := callableSelect(b, q, []string{"value"}, []string{"REGPROCEDURE"}, [][]any{{nil}}, input)
					regObjectBoth(t, p, b, c, s)
					regObjectRunStep(t, p, b, held, s)
				}
				// The throwing cast resolves operand101 before enforcing the count
				// too. Literal positions are already owned by alias-input tests.
				want := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				want.Position = "8"
				t.Run("cast_literal", func(t *testing.T) {
					callableAnalysisError(t, p, b, c, "SELECT "+regObjectLiteral(input)+"::regprocedure", want)
				})
				want.Position = ""
				q := "SELECT $1::text::regprocedure"
				held := regLookupHeld(t, p, c, q)
				defer held.Statement.Close()
				for _, route := range []struct {
					name     string
					executor fixture.Executor
					prepared bool
				}{
					{"cast_text", c, false}, {"cast_prepared", c, true}, {"cast_held", held, false},
				} {
					t.Run(route.name, func(t *testing.T) {
						s := callableErrorStep(q, want, input)
						s.Prepared = route.prepared
						regObjectRunStep(t, p, b, route.executor, s)
					})
				}
			})
		}
		// At exactly 100 operands parsing succeeds and ordinary lookup misses.
		input := "now(" + strings.TrimSuffix(strings.Repeat("integer,", 100), ",") + ")"
		regObjectBoth(t, p, b, c, callableSelect(b, "SELECT to_regprocedure($1) AS value", []string{"value"}, []string{"REGPROCEDURE"}, [][]any{{nil}}, input))
	})
}
