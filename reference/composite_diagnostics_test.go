package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"
)

func TestReferenceCompositeInputDiagnostics(t *testing.T) {
	cases := []struct{ definition, input, code, message, detail string }{
		{"i integer,t text", "", "22P02", "", "Missing left parenthesis."},
		{"i integer,t text", "[]", "22P02", "", "Missing left parenthesis."},
		{"i integer,t text", "1,x)", "22P02", "", "Missing left parenthesis."},
		{"i integer,t text", "(1)", "22P02", "", "Too few columns."},
		{"i integer,t text", "()", "22P02", "", "Too few columns."},
		{"i integer,t text", "(1,x,y)", "22P02", "", "Too many columns."},
		{"i integer,t text", "(1,,)", "22P02", "", "Too many columns."},
		{"i integer,t text", `(1,"",)`, "22P02", "", "Too many columns."},
		{"i integer,t text", "(1,x", "22P02", "", "Unexpected end of input."},
		{"i integer,t text", `(1,"x)`, "22P02", "", "Unexpected end of input."},
		{"i integer,t text", `(1,x\`, "22P02", "", "Unexpected end of input."},
		{"i integer,t text", "(1,x) junk", "22P02", "", "Junk after right parenthesis."},
		{"i integer,t text", "(1,x),", "22P02", "", "Junk after right parenthesis."},
		{"i integer,t text", "(oops,x,y)", "22P02", `invalid input syntax for type integer: "oops"`, ""},
		{"i integer,t text", "(2147483648,x,y)", "22003", `value "2147483648" is out of range for type integer`, ""},
		{"i integer,t text", "(NULL,x)", "22P02", `invalid input syntax for type integer: "NULL"`, ""},
		{"i integer,t text", `("",x)`, "22P02", `invalid input syntax for type integer: ""`, ""},
		{"i integer,t text", "( ,x)", "22P02", `invalid input syntax for type integer: " "`, ""},
		{"b boolean", "(maybe)", "22P02", `invalid input syntax for type boolean: "maybe"`, ""},
		{"t varchar(2)", "(abcd)", "22001", `value too long for type character varying(2)`, ""},
		{"n numeric(4,1)", "(1000)", "22003", `numeric field overflow`, `A field with precision 4, scale 1 must round to an absolute value less than 10^3.`},
		{"p inner_record", `("(oops,x,y)")`, "22P02", `invalid input syntax for type integer: "oops"`, ""},
		{"p inner_record", `("(1)")`, "22P02", `malformed record literal: "(1)"`, `Too few columns.`},
		{"i positive_int", "(oops)", "22P02", `invalid input syntax for type integer: "oops"`, ""},
		{"i integer,t text", "\u00a0(1,x)", "22P02", "", "Missing left parenthesis."},
	}
	for i, tc := range cases {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			compositeReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
				compositeReferenceSetup(t, p, b, c, `CREATE TYPE inner_record AS (i integer,t text)`, `CREATE DOMAIN positive_int AS integer`, "CREATE TYPE tested AS ("+tc.definition+")", `CREATE TABLE holder(k integer,v tested)`)
				message := tc.message
				if message == "" {
					message = "malformed record literal: \"" + tc.input + "\""
				}
				want := compositeReferenceDiagnostic(tc.code, message)
				want.Detail = tc.detail
				literal := "'" + strings.ReplaceAll(tc.input, "'", "''") + "'"
				q := "/*é*/ INSERT INTO holder VALUES (1," + literal + ")"
				want.Position = compositeReferenceLiteralPosition(q)
				compositeReferenceError(t, p, c, q, "prepare", want)
				want.Position = ""
				want.Context = `unnamed portal parameter $2 = '...'`
				if tc.input == "" {
					want.Context = `unnamed portal parameter $2 = ''`
				}
				compositeReferenceError(t, p, c, `INSERT INTO holder VALUES ($1,$2)`, "execute", want, 1, tc.input)
				// Good first tuple must not leak if the later record input fails.
				q = "INSERT INTO holder VALUES (0,NULL),(1," + literal + ")"
				want.Context = ""
				want.Position = compositeReferenceLiteralPosition(q)
				compositeReferenceError(t, p, c, q, "prepare", want)
				compositeReferenceRows(t, p, b, c, `SELECT v FROM holder ORDER BY k`, [][]any{}, false)
			})
		})
	}
}

func TestReferenceCompositeTypedCoercion(t *testing.T) {
	compositeReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		compositeReferenceSetup(t, p, b, c, `CREATE TYPE first_record AS (i integer,t text)`, `CREATE TYPE second_record AS (i integer,t text)`, `CREATE TABLE holder(k integer,v first_record)`)
		for _, q := range []string{`INSERT INTO holder VALUES (1,'(7,x)'::first_record)`, `INSERT INTO holder VALUES (2,'(8,y)'::text::first_record)`} {
			referenceRunStep(t, p.ctx, c, b.recorder, referenceExec(q, 1))
		}
		compositeReferenceRows(t, p, b, c, `SELECT v FROM holder ORDER BY k`, [][]any{{"(7,x)"}, {"(8,y)"}}, true)
		for _, tc := range []struct{ q, code, message, hint, position string }{
			{`INSERT INTO holder VALUES (3,'(9,z)'::text)`, "42804", `column "v" is of type first_record but expression is of type text`, `You will need to rewrite or cast the expression.`, "30"},
			{`INSERT INTO holder VALUES (3,'(9,z)'::second_record)`, "42804", `column "v" is of type first_record but expression is of type second_record`, `You will need to rewrite or cast the expression.`, "30"},
			{`SELECT '(9,z)'::first_record::second_record`, "42846", `cannot cast type first_record to second_record`, "", "29"},
		} {
			want := compositeReferenceDiagnostic(tc.code, tc.message)
			want.Hint = tc.hint
			want.Position = tc.position
			compositeReferenceError(t, p, c, tc.q, "prepare", want)
		}
		referenceRunStep(t, p.ctx, c, b.recorder, referenceSelect(`SELECT v::text AS v FROM holder ORDER BY k`, []string{"v"}, []string{"TEXT"}, [][]any{{"(7,x)"}, {"(8,y)"}}))
	})
}

func TestReferenceCompositeConstrainedDomainBoundary(t *testing.T) {
	compositeReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		q := `CREATE DOMAIN positive_int AS integer CHECK(VALUE>0)`
		if !b.pg {
			want := compositeReferenceDiagnostic("0A000", "domain constraints and defaults are unsupported")
			want.Position = fmt.Sprint(strings.Index(q, "CHECK") + 1)
			compositeReferenceError(t, p, c, q, "execute", want)
			return
		}
		compositeReferenceSetup(t, p, b, c, q, `CREATE TYPE tested AS (i positive_int)`, `CREATE TABLE holder(k integer,v tested)`)
		want := compositeReferenceDiagnostic("23514", `value for domain positive_int violates check constraint "positive_int_check"`)
		want.Schema = "public"
		want.DataType = "positive_int"
		want.Constraint = "positive_int_check"
		literal := `/*é*/ INSERT INTO holder VALUES (1,'(-1)')`
		want.Position = compositeReferenceLiteralPosition(literal)
		compositeReferenceError(t, p, c, literal, "prepare", want)
		want.Position = ""
		want.Context = `unnamed portal parameter $2 = '...'`
		compositeReferenceError(t, p, c, `INSERT INTO holder VALUES ($1,$2)`, "execute", want, 1, "(-1)")
		compositeReferenceRows(t, p, b, c, `SELECT v FROM holder ORDER BY k`, [][]any{}, false)
	})
}

func TestReferenceCompositeNestedEnumDiagnostics(t *testing.T) {
	for _, naming := range []string{"visible", "invisible", "shadowed"} {
		for _, wrapping := range []string{"direct", "domain", "nested"} {
			t.Run(naming+"/"+wrapping, func(t *testing.T) {
				compositeReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
					compositeReferenceSetup(t, p, b, c, `CREATE SCHEMA diagnostic_enum`, `CREATE SCHEMA diagnostic_shadow`)
					defer c.ExecContext(p.ctx, `DROP SCHEMA diagnostic_shadow CASCADE`)
					defer c.ExecContext(p.ctx, `DROP SCHEMA diagnostic_enum CASCADE`)
					enumType, diagnosticName := "public.mood", "mood"
					if naming == "invisible" {
						enumType, diagnosticName = "diagnostic_enum.mood", "diagnostic_enum.mood"
					}
					compositeReferenceSetup(t, p, b, c, "CREATE TYPE "+enumType+" AS ENUM ('ok')")
					childType := enumType
					good, bad := "(ok)", "(nope)"
					if wrapping == "domain" {
						compositeReferenceSetup(t, p, b, c, "CREATE DOMAIN public.member_domain AS "+enumType)
						childType = "public.member_domain"
					} else if wrapping == "nested" {
						compositeReferenceSetup(t, p, b, c, "CREATE TYPE public.inner_record AS (m "+enumType+")")
						childType = "public.inner_record"
						good, bad = `("(ok)")`, `("(nope)")`
					}
					compositeReferenceSetup(t, p, b, c, "CREATE TYPE public.tested AS (m "+childType+")", `CREATE TABLE public.holder(k integer,v public.tested)`)
					if naming == "shadowed" {
						compositeReferenceSetup(t, p, b, c, `CREATE TYPE diagnostic_shadow.mood AS ENUM ('other')`, `SET search_path TO diagnostic_shadow,public`)
						diagnosticName = "public.mood"
					} else {
						compositeReferenceSetup(t, p, b, c, `SET search_path TO public`)
					}
					defer c.ExecContext(p.ctx, `RESET search_path`)
					referenceRunStep(t, p.ctx, c, b.recorder, referenceExec(`INSERT INTO holder VALUES (7,$1)`, 1, good))
					want := compositeReferenceDiagnostic("22P02", `invalid input value for enum `+diagnosticName+`: "nope"`)
					literal := "'" + strings.ReplaceAll(bad, "'", "''") + "'"
					q := "/*é*/ INSERT INTO holder VALUES (1," + literal + ")"
					want.Position = compositeReferenceLiteralPosition(q)
					compositeReferenceError(t, p, c, q, "prepare", want)
					want.Position = ""
					want.Context = `unnamed portal parameter $2 = '...'`
					compositeReferenceError(t, p, c, `INSERT INTO holder VALUES ($1,$2)`, "execute", want, 1, bad)
					// A valid earlier tuple cannot publish before the child failure.
					q = "/*é*/ INSERT INTO holder VALUES (0,NULL),(1," + literal + ")"
					want.Context = ""
					want.Position = compositeReferenceLiteralPosition(q)
					compositeReferenceError(t, p, c, q, "prepare", want)
					want.Position = ""
					want.Context = `unnamed portal parameter $2 = '...'`
					compositeReferenceError(t, p, c, `INSERT INTO holder VALUES (0,NULL),($1,$2)`, "execute", want, 1, bad)
					compositeReferenceRows(t, p, b, c, `SELECT v FROM holder ORDER BY k`, [][]any{{good}}, false)
				})
			})
		}
	}
}

func TestReferenceCompositeEmptyRecordDiagnostics(t *testing.T) {
	compositeReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		compositeReferenceSetup(t, p, b, c, `CREATE TYPE empty_record AS ()`, `CREATE TABLE holder(k integer,v empty_record)`)
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec(`INSERT INTO holder VALUES (7,'()')`, 1))
		want := compositeReferenceDiagnostic("22P02", `malformed record literal: "("`)
		want.Detail = "Too many columns."
		q := `/*é*/ INSERT INTO holder VALUES (1,'(')`
		want.Position = compositeReferenceLiteralPosition(q)
		compositeReferenceError(t, p, c, q, "prepare", want)
		want.Position = ""
		want.Context = `unnamed portal parameter $2 = '...'`
		compositeReferenceError(t, p, c, `INSERT INTO holder VALUES ($1,$2)`, "execute", want, 1, "(")
		q = `/*é*/ INSERT INTO holder VALUES (0,NULL),(1,'(')`
		want.Context = ""
		want.Position = compositeReferenceLiteralPosition(q)
		compositeReferenceError(t, p, c, q, "prepare", want)
		want.Position = ""
		want.Context = `unnamed portal parameter $2 = '...'`
		compositeReferenceError(t, p, c, `INSERT INTO holder VALUES (0,NULL),($1,$2)`, "execute", want, 1, "(")
		compositeReferenceRows(t, p, b, c, `SELECT v FROM holder ORDER BY k`, [][]any{{"()"}}, true)
	})
}

func TestReferenceCompositeRetainedDefaultDiagnostics(t *testing.T) {
	compositeReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		// LOWER accepts the existing NAME-producing schema identity and returns
		// TEXT without extending the unsupported general NAME→TEXT explicit cast.
		compositeReferenceSetup(t, p, b, c, `SET search_path TO public`,
			`CREATE SCHEMA diagnostic_bad`,
			`CREATE TYPE mood AS ENUM ('public')`,
			`CREATE TYPE tested AS (m mood)`,
			`CREATE TABLE holder(k integer,v tested DEFAULT (('('::text || lower(CURRENT_SCHEMA()) || ')'::text)::tested))`)
		defer c.ExecContext(p.ctx, `DROP SCHEMA diagnostic_bad CASCADE`)
		defer c.ExecContext(p.ctx, `RESET search_path`)
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec(`INSERT INTO holder(k) VALUES (7)`, 1))
		compositeReferenceRows(t, p, b, c, `SELECT v FROM holder ORDER BY k`, [][]any{{"(public)"}}, false)
		compositeReferenceSetup(t, p, b, c, `SET search_path TO diagnostic_bad,public`)
		for _, naming := range []string{"mood", "public.mood"} {
			if naming == "public.mood" {
				compositeReferenceSetup(t, p, b, c, `CREATE TYPE diagnostic_bad.mood AS ENUM ('public')`)
			}
			want := compositeReferenceDiagnostic("22P02", `invalid input value for enum `+naming+`: "diagnostic_bad"`)
			// The failing record is computed by the retained default, not imported
			// from SQL literal text or a portal parameter. Both provenance fields
			// must remain absent, even when the integer key is bound.
			compositeReferenceError(t, p, c, `INSERT INTO holder(k) VALUES (1)`, "execute", want)
			compositeReferenceError(t, p, c, `INSERT INTO holder(k) VALUES ($1)`, "execute", want, 1)
			compositeReferenceError(t, p, c, `INSERT INTO holder(k,v) VALUES (0,NULL),(1,DEFAULT)`, "execute", want)
			compositeReferenceError(t, p, c, `INSERT INTO holder(k,v) VALUES (0,NULL),($1,DEFAULT)`, "execute", want, 1)
			compositeReferenceRows(t, p, b, c, `SELECT v FROM holder ORDER BY k`, [][]any{{"(public)"}}, true)
		}
		// A fresh statement must reuse the same default successfully after the
		// path changes back, not a record text/name frozen at declaration time.
		compositeReferenceSetup(t, p, b, c, `SET search_path TO public`)
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec(`INSERT INTO holder(k) VALUES (8)`, 1))
		compositeReferenceRows(t, p, b, c, `SELECT v FROM holder ORDER BY k`, [][]any{{"(public)"}, {"(public)"}}, false)
	})
}
