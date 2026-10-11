package reference_test

import (
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceNumericLiteralsSourceDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	step := func(s fixture.Step) { s.NonContext = true; p.Step(t, s) }
	step(fixture.Exec("CREATE TABLE t(x INT)", 0))
	step(fixture.Exec("INSERT INTO t VALUES (99)", 1))
	type failure struct{ query, code, message, position string }
	var cases []failure
	for _, c := range numericLiteralErrors() {
		cases = append(cases, failure{"INSERT INTO t VALUES (1),(" + c.token + ")", "42601", c.reason + ` at or near "` + c.token + `"`, "27"})
		cases = append(cases, failure{"INSERT INTO t VALUES (" + c.token + ")", "42601", c.reason + ` at or near "` + c.token + `"`, "23"})
	}
	cases = append(cases,
		failure{"INSERT INTO t VALUES (-0b2)", "42601", `trailing junk after numeric literal at or near "0b2"`, "24"},
		failure{"/*é*/ INSERT INTO t VALUES (0b2)", "42601", `trailing junk after numeric literal at or near "0b2"`, "29"},
		failure{"/*😀*/ INSERT INTO t VALUES (0x_)", "42601", `invalid hexadecimal integer at or near "0x_"`, "29"},
		failure{"INSERT INTO t VALUES (1); INSERT INTO t VALUES (0b2)", "42601", `trailing junk after numeric literal at or near "0b2"`, "49"})
	sentinel := fixture.Select("SELECT x FROM t ORDER BY x", []string{"x"}, []string{"INT4"}, [][]any{{int64(99)}})
	for _, c := range cases {
		for _, op := range []string{"exec", "query", "prepare"} {
			s := fixture.ImplicitError(c.query, fixture.Diagnostic{Code: c.code, Message: c.message, Position: c.position})
			s.SelectRows = op == "query"
			s.Prepared = op == "prepare"
			s.ErrorStage = "execute"
			if s.Prepared {
				s.ErrorStage = "prepare"
			}
			step(s)
			step(sentinel)
		}
	}
	for _, prepared := range []bool{false, true} {
		s := fixture.ImplicitError("INSERT INTO t VALUES (1),(0x8000_0000)", fixture.Diagnostic{Code: "22003", Message: "integer out of range"})
		s.Prepared = prepared
		s.ErrorStage = "execute"
		step(s)
		step(sentinel)
	}
	step(fixture.Select("SELECT x FROM t WHERE x=0x8000_0000", []string{"x"}, []string{"INT4"}, nil))
}

func TestReferenceNumericLiteralsSourceRepresentability(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			run := func(s fixture.Step) {
				s.NonContext = true
				fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
			}
			run(fixture.Exec("CREATE TABLE t(x BIGINT)", 0))
			for _, token := range []string{"9223372036854775808", "0x8000000000000000", "0o1000000000000000000000", "0b1" + strings.Repeat("0", 63), "-9223372036854775809", "-0x8000000000000001", "-0o1000000000000000000001", "-0b1" + strings.Repeat("0", 62) + "1", strings.Repeat("9", 200)} {
				for _, op := range []string{"exec", "query", "prepare"} {
					// PG resolves the oversized literal as NUMERIC and narrows at
					// Execute; native likewise accepts Prepare then rejects import.
					s := fixture.ImplicitError("INSERT INTO t VALUES ("+token+")", fixture.Diagnostic{Code: "22003", Message: "bigint out of range"})
					s.SelectRows = op == "query"
					s.Prepared = op == "prepare"
					s.ErrorStage = "execute"
					run(s)
				}
			}
			s := fixture.Select("SELECT x FROM t", []string{"x"}, []string{"INT8"}, nil)
			run(s)
			rows, err := backend.DB.Query(s.Query)
			if err != nil {
				t.Fatal(err)
			}
			fixture.LexicalRows(t, backend.PG, rows, s, nil)
		})
	}
}

func TestReferenceNumericLiteralsSourceSpellings(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := fixture.NewScenario(t)
			step := func(s fixture.Step) { s.NonContext = true; p.Step(t, s) }
			step(fixture.Exec("CREATE TABLE t(id INT,b BIGINT)", 0))
			var want [][]any
			for i, tc := range numericLiteralSpellings() {
				s := fixture.Exec(fmt.Sprintf("INSERT INTO t VALUES (%d,%s)", i, tc.literal), 1)
				s.Prepared = prepared
				step(s)
				want = append(want, []any{int64(i), tc.value})
			}
			s := fixture.Select("SELECT id,b FROM t ORDER BY id", []string{"id", "b"}, []string{"INT4", "INT8"}, want)
			step(s)
			for _, backend := range p.Backends() {
				rows, err := backend.DB.Query(s.Query)
				if err != nil {
					t.Fatal(err)
				}
				fixture.LexicalRows(t, backend.PG, rows, s, nil)
			}
		})
	}
}
