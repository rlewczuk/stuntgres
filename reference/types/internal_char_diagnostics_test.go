package reference_test

import (
	"strconv"
	"testing"

	"stuntgres/reference/fixture"
)

func internalCharEncodingDiagnostic(octet string, parameter int) fixture.Diagnostic {
	d := fixture.ControlsReferenceDiagnostic("22021", `invalid byte sequence for encoding "UTF8": `+octet)
	d.Context = "unnamed portal parameter $" + strconv.Itoa(parameter)
	return d
}

func TestReferenceInternalCharDiagnostics(t *testing.T) {
	internalCharScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Exec(`CREATE TABLE internal_char_errors(id integer,v "char")`, 0))
		run(fixture.Exec(`INSERT INTO internal_char_errors VALUES(1,'r')`, 1))
		for _, tc := range []struct{ input, octet string }{
			{"\xff", "0xff"}, {"a\xff", "0xff"}, {"\x00", "0x00"}, {"a\x00b", "0x00"},
		} {
			for _, arg := range []any{tc.input, []byte(tc.input)} {
				for _, prepared := range []bool{false, true} {
					for _, query := range []bool{false, true} {
						q := `INSERT INTO internal_char_errors VALUES(2,$1)`
						if query {
							q = `SELECT $1::pg_catalog."char" AS v`
						}
						s := fixture.ErrorDiagnostic(q, internalCharEncodingDiagnostic(tc.octet, 1), arg)
						s.SelectRows, s.Prepared, s.ErrorStage = query, prepared, "execute"
						run(s)
					}
					s := fixture.ErrorDiagnostic(`INSERT INTO internal_char_errors VALUES(2,$1),(3,$2)`, internalCharEncodingDiagnostic(tc.octet, 2), "a", arg)
					s.Prepared, s.ErrorStage = prepared, "execute"
					run(s)
					run(internalCharSelect(`SELECT v FROM internal_char_errors ORDER BY id`, [][]any{{"r"}}))
				}
			}
		}
		run(fixture.Exec(`CREATE TABLE internal_char_notnull(id integer,v "char" NOT NULL)`, 0))
		run(fixture.Exec(`INSERT INTO internal_char_notnull VALUES(1,'')`, 1))
		d := fixture.ControlsReferenceDiagnostic("23502", `null value in column "v" of relation "internal_char_notnull" violates not-null constraint`)
		d.Detail = "Failing row contains (2, null)."
		d.Schema, d.Table, d.Column = "public", "internal_char_notnull", "v"
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic(`INSERT INTO internal_char_notnull VALUES(2,$1)`, d, nil)
			s.Prepared, s.ErrorStage = prepared, "execute"
			run(s)
		}
		run(internalCharSelect(`SELECT v FROM internal_char_notnull ORDER BY id`, [][]any{{""}}))
		// Source-derived parse diagnostics. Positions count characters, including
		// the multibyte comment; the pinned oracle must resolve exact type spelling.
		for _, tc := range []struct{ q, message, pos string }{
			{`SELECT 'r'::"char"(2)`, `type modifier is not allowed for type "char"`, "13"},
			{`/*é*/ SELECT 'r'::pg_catalog."char"(2)`, `type modifier is not allowed for type "pg_catalog.char"`, "19"},
			{`SELECT true::"char"`, `cannot cast type boolean to "char"`, "12"},
		} {
			code := "42601"
			if tc.pos == "12" {
				code = "42846"
			}
			d := fixture.ControlsReferenceDiagnostic(code, tc.message)
			d.Position = tc.pos
			s := fixture.ErrorDiagnostic(tc.q, d)
			s.SelectRows, s.ErrorStage = true, "execute"
			run(s)
			s.Prepared, s.ErrorStage = true, "prepare"
			run(s)
		}
	})
}

func TestReferenceInternalCharCasts(t *testing.T) {
	internalCharScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		for _, tc := range []struct{ input int64; output string }{
			{-128, `\200`}, {-1, `\377`}, {0, ""}, {65, "A"}, {127, "\x7f"},
		} {
			for _, prepared := range []bool{false, true} {
				s := internalCharSelect(`SELECT $1::int4::"char" AS v`, [][]any{{tc.output}}, tc.input)
				s.Prepared = prepared
				run(s)
				s = fixture.Select(`SELECT $1::"char"::int4 AS n`, []string{"n"}, []string{"INT4"}, [][]any{{tc.input}}, tc.output)
				s.Prepared = prepared
				run(s)
			}
		}
		q := `SELECT $1::int4::"char" AS v`
		stmt, err := b.DB.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}
		d := fixture.ControlsReferenceDiagnostic("22003", `"char" out of range`)
		for _, n := range []int64{-129, 128} {
			s := fixture.ErrorDiagnostic(q, d, n)
			s.SelectRows, s.ErrorStage = true, "execute"
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, internalCharSelect(q, [][]any{{"A"}}, int64(65)))
		// Integer constant casts are runtime conversion, not unknown-literal I/O.
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic(`SELECT 128::int4::"char"`, d)
			s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
			run(s)
		}
		run(fixture.Exec(`CREATE TABLE internal_char_casts(v "char",t text,b char(1),c varchar(8))`, 0))
		run(fixture.Exec(`INSERT INTO internal_char_casts(v) VALUES('abc'::text),('é'::varchar),('xy'::char(2))`, 3))
		s := internalCharSelect(`SELECT v FROM internal_char_casts`, [][]any{{"a"}, {`\303`}, {"x"}})
		s.Bag = true
		run(s)
		run(fixture.Exec(`INSERT INTO internal_char_casts(t,b,c) VALUES('r'::"char",'r'::"char",'r'::"char")`, 1))
		run(fixture.Select(`SELECT t,b,c FROM internal_char_casts WHERE t IS NOT NULL`, []string{"t", "b", "c"}, []string{"TEXT", "BPCHAR", "VARCHAR"}, [][]any{{"r", "r", "r"}}))
		run(fixture.Select(`SELECT '\377'::"char"::text AS t,'\200'::"char"::varchar AS c,octet_length(''::"char"::bpchar) AS z,octet_length('\377'::"char"::bpchar) AS h`, []string{"t", "c", "z", "h"}, []string{"TEXT", "VARCHAR", "INT4", "INT4"}, [][]any{{`\377`, `\200`, int64(1), int64(1)}}))
		// The explicit int4 -> CHAR edge does not permit assignment conversion.
		d = fixture.ControlsReferenceDiagnostic("42804", `column "v" is of type "char" but expression is of type integer`)
		d.Hint = "You will need to rewrite or cast the expression."
		d.Position = "43"
		s = fixture.ErrorDiagnostic(`INSERT INTO internal_char_casts(v) VALUES(65)`, d)
		run(s)
		s.Prepared, s.ErrorStage = true, "prepare"
		run(s)
	})
}
