package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePosixDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			for _, c := range []struct{ p, reason string }{
				{`[`, "brackets [] not balanced"}, {`(`, "parentheses () not balanced"}, {`)`, "parentheses () not balanced"}, {`a{2`, "braces {} not balanced"}, {`a{2,1}`, "invalid repetition count(s)"}, {`a{256}`, "invalid repetition count(s)"},
				{`[z-a]`, "invalid character range"}, {`[a-c-e]`, "invalid character range"}, {`[[:alpha:]-z]`, "invalid character range"}, {`[[=a=]-z]`, "invalid character range"}, {`[[:bogus:]]`, "invalid character class"}, {`[[.ch.]]`, "invalid collating element"}, {`[[.bogus.]]`, "invalid collating element"},
				{`*a`, "quantifier operand invalid"}, {`a**`, "quantifier operand invalid"}, {`^*`, "quantifier operand invalid"}, {`\q`, `invalid escape \ sequence`}, {`\`, `invalid escape \ sequence`}, {`[\A]`, `invalid escape \ sequence`},
				{`\1`, "invalid backreference number"}, {`(a\1)`, "invalid backreference number"}, {`(a)(?=\1)`, "invalid backreference number"}, {`(?z)a`, "invalid embedded option"}, {`a(?i)b`, "quantifier operand invalid"}, {`(?i:a)`, "invalid embedded option"},
			} {
				for _, q := range []string{`SELECT 'a' ~ $1::text`, `SELECT 'a' !~* $1::text`, `SELECT substring('a',$1::text)`} {
					similarReferenceRoutes(t, p, b, posixError(q, c.reason, c.p))
				}
			}
			for _, c := range []struct{ q, code, msg, hint, pos string }{
				{`SELECT 1 ~ 'a'`, "42883", "operator does not exist: integer ~ unknown", similarOperatorHint, "10"},
				{`SELECT /* é漢 */ 1 ~ 'a'`, "42883", "operator does not exist: integer ~ unknown", similarOperatorHint, "19"},
				{`SELECT true ~ 'a'`, "42883", "operator does not exist: boolean ~ unknown", similarOperatorHint, "13"},
				{`SELECT 'a'::bytea ~ 'a'`, "42883", "operator does not exist: bytea ~ unknown", similarOperatorHint, "19"},
				{`SELECT 'a' ~ 1`, "42883", "operator does not exist: unknown ~ integer", similarOperatorHint, "12"},
				{`SELECT substr('a','a')`, "22P02", `invalid input syntax for type integer: "a"`, "", "19"},
				{`SELECT substr('a'::text,'a'::text)`, "42883", "function substr(text, text) does not exist", similarFunctionHint, "8"},
				{`SELECT nonexistent.substring('a','a')`, "3F000", `schema "nonexistent" does not exist`, "", "8"},
				{`SELECT 'a' ~ 'a' COLLATE nonexistent`, "42704", `collation "nonexistent" for encoding "UTF8" does not exist`, "", "18"},
				{`SELECT 'a' COLLATE "C" ~ 'a' COLLATE "POSIX"`, "42P21", similarExplicitConflict, "", "30"},
				{`SELECT NULL::text COLLATE "C" ~ 'a' COLLATE "POSIX"`, "42P21", similarExplicitConflict, "", "37"},
			} {
				similarReferenceRoutes(t, p, b, similarReferenceBindingError(c.q, fixture.Diagnostic{Code: c.code, Message: c.msg, Hint: c.hint, Position: c.pos}))
			}
		})
	}
}

func TestReferencePosixTypeCollationDemand(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			run(fixture.Exec(`CREATE TABLE t(v TEXT COLLATE "C",p TEXT COLLATE "POSIX")`, 0))
			conflict := fixture.Diagnostic{Code: "42P22", Message: "could not determine which collation to use for regular expression", Hint: "Use the COLLATE clause to set the collation explicitly."}
			for _, populated := range []bool{false, true} {
				if populated {
					run(fixture.Exec(`INSERT INTO t VALUES('a','a'),(NULL,'['),('','a')`, 3))
				}
				for _, q := range []string{`SELECT v ~ p AS m FROM t`, `SELECT substring(v,p) AS m FROM t`} {
					typ := "BOOL"
					if q == `SELECT substring(v,p) AS m FROM t` {
						typ = "TEXT"
					}
					s := posixSelect(q, []string{"m"}, []string{typ}, nil)
					if populated {
						s = similarReferenceError(q, conflict)
					}
					similarReferenceRoutes(t, p, b, s)
				}
				for _, c := range []struct {
					q, typ string
					rows   [][]any
				}{
					{`SELECT v ~ p COLLATE "C" AS m FROM t ORDER BY v`, "BOOL", [][]any{{false}, {true}, {nil}}},
					{`SELECT substring(v,p COLLATE "C") AS m FROM t ORDER BY v`, "TEXT", [][]any{{nil}, {"a"}, {nil}}},
					{`SELECT v ~ p AS m FROM t WHERE v IS NULL`, "BOOL", [][]any{{nil}}},
					{`SELECT substring(v,p) AS m FROM t WHERE v IS NULL`, "TEXT", [][]any{{nil}}},
				} {
					rows := c.rows
					if !populated {
						rows = nil
					}
					similarReferenceRoutes(t, p, b, posixSelect(c.q, []string{"m"}, []string{c.typ}, rows))
				}
				for _, q := range []string{`SELECT v ~ '[' AS m FROM t WHERE false`, `SELECT substring(v,'[') AS m FROM t WHERE false`} {
					typ := "BOOL"
					if q == `SELECT substring(v,'[') AS m FROM t WHERE false` {
						typ = "TEXT"
					}
					similarReferenceRoutes(t, p, b, posixSelect(q, []string{"m"}, []string{typ}, nil))
				}
				// Pinned18.0 demands malformed invariant constants even on an empty relation;
				// these errors occur at Execute, not Prepare.
				similarReferenceRoutes(t, p, b, posixError(`SELECT 'a' ~ '[' FROM t`, "brackets [] not balanced"))
				similarReferenceRoutes(t, p, b, posixError(`SELECT substring('a','[') FROM t`, "brackets [] not balanced"))
			}
			for _, q := range []string{`SELECT CASE WHEN false THEN 'a' ~ '[' ELSE true END AS m`, `SELECT COALESCE(true,'a' ~ '[') AS m`, `SELECT NULL::text ~ '[' AS m`} {
				var value any = true
				if q == `SELECT NULL::text ~ '[' AS m` {
					value = nil
				}
				similarReferenceRoutes(t, p, b, posixSelect(q, []string{"m"}, []string{"BOOL"}, [][]any{{value}}))
			}
			for _, typ := range []string{"text", "varchar(8)", "char(3)", "name"} {
				m := typ != "char(3)"
				similarReferenceRoutes(t, p, b, posixSelect(fmt.Sprintf(`SELECT 'a'::%s ~ '^a$' AS m,substring('a'::%s,'^a$') AS piece`, typ, typ), []string{"m", "piece"}, []string{"BOOL", "TEXT"}, [][]any{{m, "a"}}))
			}
			for _, coll := range []string{"C", "POSIX", "pg_c_utf8", "pg_unicode_fast"} {
				unicode := coll == "pg_c_utf8" || coll == "pg_unicode_fast"
				run(fixture.Exec(fmt.Sprintf(`CREATE COLLATION "posix_copy_%s" FROM "%s"`, coll, coll), 0))
				for _, name := range []string{coll, "posix_copy_" + coll} {
					for _, c := range []struct {
						s, pat string
						m      bool
					}{
						{"é", `[[:alpha:]]`, unicode}, {"É", `(?i)é`, unicode}, {"é", `(?i)[É]`, unicode}, {"é", `(?i)[[:upper:]]`, unicode}, {"Éé", `(?i)(É)\1`, unicode}, {"ß", `(?i)SS`, false}, {"ς", `(?i)σ`, false}, {"\u0301", `\w`, false},
						// REL_18_0 cclasscvec remaps insensitive lower/upper to alpha,
						// including alphabetic characters with no case mapping.
						{"字", `(?i)[[:upper:]]`, unicode}, {"字", `(?i)[[:lower:]]`, unicode},
					} {
						similarReferenceRoutes(t, p, b, posixSelect(fmt.Sprintf(`SELECT $1::text ~ $2::text COLLATE "%s" AS m`, name), []string{"m"}, []string{"BOOL"}, [][]any{{c.m}}, c.s, c.pat))
					}
				}
			}
		})
	}
}
