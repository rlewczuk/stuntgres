package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSimilarDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		for _, c := range []struct{ pattern, reason string }{
			{`[`, "brackets [] not balanced"}, {`(`, "parentheses () not balanced"}, {`)`, "parentheses () not balanced"},
			{`a{2`, "invalid repetition count(s)"}, {`a{2,1}`, "invalid repetition count(s)"}, {`a{256}`, "invalid repetition count(s)"},
			{`[z-a]`, "invalid character range"}, {`[a-c-e]`, "invalid character range"}, {`[[:bogus:]]`, "invalid character class"},
			{`*a`, "quantifier operand invalid"}, {`a**`, "quantifier operand invalid"}, {`%*`, "quantifier operand invalid"},
			{`[[.ch.]]`, "invalid collating element"},
		} {
			similarReferenceRoutes(t, p, b, similarReferenceRegexError(`SELECT 'a' SIMILAR TO $1 ESCAPE '#'`, c.reason, "", c.pattern))
			similarReferenceRoutes(t, p, b, similarReferenceRegexError(`SELECT 'a' SIMILAR TO $1`, c.reason, "", c.pattern))
			similarReferenceRoutes(t, p, b, similarReferenceRegexError(`SELECT substring('a' SIMILAR $1 ESCAPE '#')`, c.reason, `SQL function "substring" statement 1`, c.pattern))
		}
		for _, c := range []struct{ pattern, reason string }{{`\q`, `invalid escape \ sequence`}, {`\1`, "invalid backreference number"}} {
			similarReferenceRoutes(t, p, b, similarReferenceRegexError(`SELECT 'a' SIMILAR TO $1`, c.reason, "", c.pattern))
			similarReferenceRoutes(t, p, b, fixture.Select(`SELECT 'a' SIMILAR TO $1 ESCAPE '#'`, []string{"?column?"}, []string{"BOOL"}, [][]any{{false}}, c.pattern))
			similarReferenceRoutes(t, p, b, fixture.Select(`SELECT substring('a' SIMILAR $1 ESCAPE '#')`, []string{"substring"}, []string{"TEXT"}, [][]any{{nil}}, c.pattern))
		}
		separator := fixture.Diagnostic{Code: "2200C", Message: "SQL regular expression may not contain more than two escape-double-quote separators"}
		similarReferenceRoutes(t, p, b, similarReferenceError(`SELECT 'a' SIMILAR TO $1 ESCAPE '#'`, separator, `#"a#"b#"c`))
		separator.Context = `SQL function "substring" statement 1`
		similarReferenceRoutes(t, p, b, similarReferenceError(`SELECT substring('a' SIMILAR $1 ESCAPE '#')`, separator, `#"a#"b#"c`))
		similarReferenceRoutes(t, p, b, fixture.Select(`SELECT 'a' SIMILAR TO $1`, []string{"?column?"}, []string{"BOOL"}, [][]any{{false}}, `#"a#"b#"c`))
		similarReferenceRoutes(t, p, b, similarReferenceError(`SELECT 'a' SIMILAR TO 'a' ESCAPE 'ab'`, fixture.Diagnostic{Code: "22025", Message: "invalid escape string", Hint: "Escape string must be empty or one character."}))
		for _, c := range []struct{ query, code, message, hint, position string }{
			{`SELECT 1 SIMILAR TO 'a'`, "42883", "operator does not exist: integer ~ text", similarOperatorHint, "10"},
			{`SELECT 1 NOT SIMILAR TO 'a'`, "42883", "operator does not exist: integer !~ text", similarOperatorHint, "10"},
			{`SELECT /* é漢 */ 1 SIMILAR TO 'a'`, "42883", "operator does not exist: integer ~ text", similarOperatorHint, "19"},
			{`SELECT 'a' SIMILAR TO 'a' ESCAPE 1`, "42883", "function pg_catalog.similar_to_escape(unknown, integer) does not exist", similarFunctionHint, "12"},
			{`SELECT 'a'::bytea SIMILAR TO 'a'`, "42883", "operator does not exist: bytea ~ text", similarOperatorHint, "19"},
			{`SELECT 'a' SIMILAR TO 'a'::bytea`, "42883", "function pg_catalog.similar_to_escape(bytea) does not exist", similarFunctionHint, "12"},
			{`SELECT substring('a','a','#',1)`, "42883", "function substring(unknown, unknown, unknown, integer) does not exist", similarFunctionHint, "8"},
			{`SELECT substring('a','a',1)`, "22P02", `invalid input syntax for type integer: "a"`, "", "22"},
			{`SELECT substring(source => 'a', pattern => 'a', escape => '#')`, "42883", "function substring(source => unknown, pattern => unknown, escape => unknown) does not exist", similarFunctionHint, "8"},
			{`SELECT nonexistent.substring('a','a','#')`, "3F000", `schema "nonexistent" does not exist`, "", "8"},
			{`SELECT 'a' SIMILAR TO 'a' COLLATE nonexistent`, "42704", `collation "nonexistent" for encoding "UTF8" does not exist`, "", "27"},
			{`SELECT 'a' COLLATE "C" SIMILAR TO 'a' COLLATE "POSIX"`, "42P21", similarExplicitConflict, "", "39"},
			{`SELECT NULL::text COLLATE "C" SIMILAR TO 'a' COLLATE "POSIX"`, "42P21", similarExplicitConflict, "", "46"},
			{`SELECT 'a' COLLATE "C" SIMILAR TO 'a' ESCAPE '#' COLLATE "POSIX"`, "42P21", similarExplicitConflict, "", "50"},
		} {
			similarReferenceRoutes(t, p, b, similarReferenceBindingError(c.query, fixture.Diagnostic{Code: c.code, Message: c.message, Hint: c.hint, Position: c.position}))
		}
	}
}

const similarOperatorHint = "No operator matches the given name and argument types. You might need to add explicit type casts."
const similarFunctionHint = "No function matches the given name and argument types. You might need to add explicit type casts."
const similarExplicitConflict = `collation mismatch between explicit collations "C" and "POSIX"`

func TestReferenceSimilarClassesGrammar(t *testing.T) {
	p := fixture.NewScenario(t)
	sources := []string{"a", "]", "^", "-", "abc", "\t", `"`}
	for _, b := range p.Backends() {
		for _, c := range []struct {
			pattern string
			matches [7]bool
		}{
			{`[]a]`, [7]bool{true, true, false, false, false, false, false}},
			{`[^]a]`, [7]bool{false, false, true, true, false, true, true}},
			{`[^^]`, [7]bool{true, true, false, true, false, true, true}},
			{`[[:ascii:]]`, [7]bool{true, true, true, true, false, true, true}},
			{`[[:word:]]`, [7]bool{true, false, false, false, false, false, false}},
			{`[[.a.]]`, [7]bool{true, false, false, false, false, false, false}},
			{`[[=a=]]`, [7]bool{true, false, false, false, false, false, false}},
			{`[[.tab.]]`, [7]bool{false, false, false, false, false, true, false}},
			{`[[:<:]]abc[[:>:]]`, [7]bool{false, false, false, false, true, false, false}},
			{`[\"]`, [7]bool{false, false, false, false, false, false, true}},
			{`[#"]`, [7]bool{false, false, false, false, false, false, true}},
		} {
			for i, source := range sources {
				similarReferenceRoutes(t, p, b, fixture.Select(`SELECT $1 SIMILAR TO $2 AS matched`, []string{"matched"}, []string{"BOOL"}, [][]any{{c.matches[i]}}, source, c.pattern))
			}
		}
		for _, collation := range []string{"C", "POSIX", "pg_c_utf8", "pg_unicode_fast"} {
			unicode := collation == "pg_c_utf8" || collation == "pg_unicode_fast"
			for _, c := range []struct {
				code  int
				class string
				match bool
			}{
				{160, "space", unicode}, {160, "graph", false}, {160, "print", unicode},
				{8232, "space", unicode}, {8232, "graph", false}, {8232, "print", false},
				{8203, "space", false}, {8203, "graph", unicode}, {8203, "print", unicode},
				{769, "word", false}, {95, "word", true}, {128, "cntrl", true}, {159, "cntrl", true},
			} {
				query := fmt.Sprintf(`SELECT chr(%d) SIMILAR TO '[[:%s:]]' COLLATE "%s" AS matched`, c.code, c.class, collation)
				similarReferenceRoutes(t, p, b, fixture.Select(query, []string{"matched"}, []string{"BOOL"}, [][]any{{c.match}}))
			}
		}
	}
}

func TestReferenceSimilarCollationDemand(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(`CREATE TABLE t(v TEXT COLLATE "C",p TEXT COLLATE "POSIX",e TEXT COLLATE "C")`, 0))
		for _, populated := range []bool{false, true} {
			if populated {
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(`INSERT INTO t VALUES('a','a','#'),(NULL,'a','#'),('','a','#')`, 3))
			}
			t.Run(fmt.Sprintf("%s/populated=%t", b.Name, populated), func(t *testing.T) {
				conflict := fixture.Diagnostic{Code: "42P22", Message: "could not determine which collation to use for regular expression", Hint: "Use the COLLATE clause to set the collation explicitly."}
				for _, query := range []string{
					`SELECT v SIMILAR TO p FROM t`, `SELECT v SIMILAR TO p ESCAPE e FROM t`,
					`SELECT '' SIMILAR TO p ESCAPE e FROM t`, `SELECT v SIMILAR TO p ESCAPE e FROM t WHERE v=''`,
					`SELECT 'a' SIMILAR TO p ESCAPE e FROM t`,
				} {
					step := fixture.Select(query, []string{"?column?"}, []string{"BOOL"}, nil)
					if populated {
						step = similarReferenceError(query, conflict)
					}
					similarReferenceRoutes(t, p, b, step)
				}
				similarReferenceRoutes(t, p, b, similarReferenceBindingError(`SELECT v SIMILAR TO 'a' COLLATE "C" ESCAPE e COLLATE "POSIX" FROM t WHERE FALSE`, fixture.Diagnostic{Code: "42P21", Message: similarExplicitConflict, Position: "46"}))
				for _, c := range []struct {
					query, name, typ string
					rows             [][]any
				}{
					{`SELECT NULL::text SIMILAR TO p ESCAPE e FROM t`, "?column?", "BOOL", [][]any{{nil}, {nil}, {nil}}},
					{`SELECT v SIMILAR TO p ESCAPE e FROM t WHERE v IS NULL`, "?column?", "BOOL", [][]any{{nil}}},
					{`SELECT substring(v SIMILAR p ESCAPE e) FROM t WHERE v IS NULL`, "substring", "TEXT", [][]any{{nil}}},
					{`SELECT v SIMILAR TO p COLLATE "C" ESCAPE e FROM t`, "?column?", "BOOL", [][]any{{true}, {nil}, {false}}},
					// SQL substring wrapper has a distinct matcher collation contract.
					{`SELECT substring(v SIMILAR p ESCAPE e) FROM t`, "substring", "TEXT", [][]any{{"a"}, {nil}, {nil}}},
				} {
					rows := c.rows
					if !populated {
						rows = nil
					}
					step := fixture.Select(c.query, []string{c.name}, []string{c.typ}, rows)
					step.Bag = true
					similarReferenceRoutes(t, p, b, step)
				}
			})
		}
	}
}
