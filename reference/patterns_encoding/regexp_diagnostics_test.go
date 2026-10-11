package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRegexpFlags(t *testing.T) {
	regexpScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, c := range []struct {
			s, pattern, flags string
			match             bool
		}{
			{"A", "a", "i", true}, {"A", "a", "ic", false}, {"A", "a", "ci", true},
			{"A", "(?c)a", "i", false}, {"A", "(?i)a", "c", true},
			{"(?i)a", "(?i)a", "q", true}, {"a", "(?i)a", "q", false},
			{"***:a", "***:a", "q", true}, {"***=a", "***=a", "q", true},
			{"a+b", "a+b", "b", true}, {"aaab", "a+b", "e", false},
			{"ab", "a(b)", "b", false}, {"a(b)", "a(b)", "b", true},
			{"ab", `a\(b\)`, "b", true}, {"ab", "***:a(b)", "b", true},
			{"A", "***:(?i)a", "e", true},
			{"a+b", "a+b", "q", true}, {"aaab", "a+b", "qe", false},
			{"a+b", "a+b", "eq", true}, {"a b", "a b", "x", false}, {"ab", "a b", "x", true},
			{"a b", "a b", "xt", true}, {"ab", "a b", "tx", true},
			{"ab", "a # comment\nb", "x", true}, {"ab", "***=a b", "x", false},
			{"a b", "***=a b", "x", true},
			// Pinned18.0 external e clears the composite REG_ADVANCED bit, including
			// REG_EXTENDED: effective BRE. Embedded (?e) is genuinely ERE.
			{"aaab", "(?e)a+b", "", true}, {"aaab", "***:a+b", "e", true},
			{"ab", `a\(b\)`, "e", true}, {"ab", "a(b)", "e", false},
			{"(?i)a", "(?i)a", "e", true}, {"a", "(?i)a", "e", false},
			{"\n", ".", "s", true}, {"\n", ".", "n", false}, {"\n", ".", "m", false},
			{"\n", ".", "p", false}, {"\n", ".", "w", true}, {"\n", "[^a]", "n", false},
			{"\n", "[^a]", "w", true}, {"a\nb", "^b", "n", true}, {"a\nb", "^b", "m", true},
			{"a\nb", "^b", "p", false}, {"a\nb", "^b", "w", true}, {"a\nb", "^b", "s", false},
			{"a\nb", "^b", "ns", false}, {"a\nb", "^b", "sn", true},
			{"a\nb", `\Ab`, "n", false}, {"a\nb", `a\Z`, "n", false},
			{"\r", ".", "n", true}, {"\u2028", ".", "n", true},
			{"a.b", "a.b", "qt", true}, {"a.b", "a.b", "qs", true},
			{"a b", "a b", "xqt", true}, {"\n", ".", "nqs", false},
		} {
			regexpRoutes(t, p, b, regexpOne(b, `regexp_like($1::text,$2::text,$3::text)`, "BOOL", c.match, c.s, c.pattern, c.flags))
		}
		for _, flags := range []string{"qx", "qn", "qw", "qp", "xq", "nq"} {
			regexpRoutes(t, p, b, similarReferenceRegexError(`SELECT regexp_like('a','a',$1::text)`, "invalid argument to regex function", "", flags))
		}
		// Both external b and released external e treat this prefix literally.
		for _, flags := range []string{"e", "b"} {
			regexpRoutes(t, p, b, regexpOne(b, `regexp_like('(?i)a','(?i)a',$1::text)`, "BOOL", true, flags))
			regexpRoutes(t, p, b, regexpOne(b, `regexp_like('a','(?i)a',$1::text)`, "BOOL", false, flags))
		}
	})
}

func TestReferenceRegexpDiagnostics(t *testing.T) {
	regexpScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, c := range []struct{ expr, msg, hint string }{
			{`regexp_count('a','a',0)`, `invalid value for parameter "start": 0`, ""},
			{`regexp_count('a','a',-2147483648)`, `invalid value for parameter "start": -2147483648`, ""},
			{`regexp_instr('a','a',0,0,2,'z',-1)`, `invalid value for parameter "start": 0`, ""},
			{`regexp_instr('a','a',1,0,2,'z',-1)`, `invalid value for parameter "n": 0`, ""},
			{`regexp_instr('a','a',1,-1)`, `invalid value for parameter "n": -1`, ""},
			{`regexp_instr('a','a',1,1,2,'z',-1)`, `invalid value for parameter "endoption": 2`, ""},
			{`regexp_instr('a','a',1,1,-1)`, `invalid value for parameter "endoption": -1`, ""},
			{`regexp_instr('a','[',1,1,0,'z',-1)`, `invalid value for parameter "subexpr": -1`, ""},
			{`regexp_replace('a','[','x',0,-1,'z')`, `invalid value for parameter "start": 0`, ""},
			{`regexp_replace('a','[','x',1,-1,'z')`, `invalid value for parameter "n": -1`, ""},
			{`regexp_like('a','[','gz')`, `invalid regular expression option: "z"`, ""},
			{`regexp_like('a','a','🙂z')`, `invalid regular expression option: "🙂"`, ""},
			{`regexp_like('a','a','I')`, `invalid regular expression option: "I"`, ""},
			{`regexp_like('a','a','a')`, `invalid regular expression option: "a"`, ""},
			{`regexp_replace('a','a','x','2')`, `invalid regular expression option: "2"`, `If you meant to use regexp_replace() with a start parameter, cast the fourth argument to integer explicitly.`},
			{`regexp_replace('a','a','x','-2')`, `invalid regular expression option: "-"`, ""},
		} {
			regexpRoutes(t, p, b, regexpRuntimeError("SELECT "+c.expr, "22023", c.msg, c.hint))
		}
		for _, name := range []string{"regexp_count", "regexp_instr", "regexp_like", "regexp_match", "regexp_split_to_array", "regexp_split_to_table"} {
			args := `'a','[','g'`
			if name == "regexp_count" {
				args = `'a','[',1,'g'`
			}
			if name == "regexp_instr" {
				args = `'a','[',1,1,0,'g'`
			}
			hint := ""
			if name == "regexp_match" {
				hint = "Use the regexp_matches function instead."
			}
			regexpRoutes(t, p, b, regexpRuntimeError("SELECT "+name+"("+args+")", "22023", name+`() does not support the "global" option`, hint))
		}
		for _, c := range []struct{ pattern, reason string }{
			{`[`, "brackets [] not balanced"}, {`(`, "parentheses () not balanced"}, {`)`, "parentheses () not balanced"}, {`a{2`, "braces {} not balanced"}, {`a{2,1}`, "invalid repetition count(s)"}, {`a{256}`, "invalid repetition count(s)"},
			{`[z-a]`, "invalid character range"}, {`[a-c-e]`, "invalid character range"}, {`[[:bogus:]]`, "invalid character class"}, {`[[.ch.]]`, "invalid collating element"},
			{`*a`, "quantifier operand invalid"}, {`a**`, "quantifier operand invalid"}, {`\q`, `invalid escape \ sequence`}, {`\`, `invalid escape \ sequence`},
			{`\1`, "invalid backreference number"}, {`(a\1)`, "invalid backreference number"}, {`(a)(?=\1)`, "invalid backreference number"}, {`(?z)a`, "invalid embedded option"}, {`a(?i)b`, "quantifier operand invalid"}, {`(?i:a)`, "invalid embedded option"},
		} {
			for _, name := range []string{"regexp_count", "regexp_instr", "regexp_like", "regexp_match", "regexp_matches", "regexp_replace", "regexp_split_to_array", "regexp_split_to_table"} {
				args := `'a',$1::text`
				if name == "regexp_replace" {
					args += `,'X'`
				}
				regexpRoutes(t, p, b, similarReferenceRegexError("SELECT "+name+"("+args+")", c.reason, "", c.pattern))
				if name == "regexp_matches" || name == "regexp_split_to_table" {
					regexpRoutes(t, p, b, similarReferenceRegexError("SELECT r.result FROM "+name+"("+args+") r(result)", c.reason, "", c.pattern))
				}
			}
		}
		for _, expr := range []string{`regexp_instr('a','[',1,99,0,'',99)`, `regexp_replace('a','[','x',99,99)`, `regexp_count('a','[',99)`} {
			regexpRoutes(t, p, b, similarReferenceRegexError("SELECT "+expr, "brackets [] not balanced", ""))
		}
		for _, c := range regexpSignatures() {
			q := fmt.Sprintf("SELECT /* é🙂 */ %s()", c.name)
			regexpRoutes(t, p, b, regexpBindError(q, "42883", "function "+c.name+"() does not exist", similarFunctionHint, c.name))
		}
	})
}

func TestReferenceRegexpCollation(t *testing.T) {
	regexpScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Exec(`CREATE TABLE coll_t(s TEXT COLLATE "C",p TEXT COLLATE "POSIX",r TEXT COLLATE "POSIX",f TEXT COLLATE "POSIX")`, 0))
		run(fixture.Exec(`INSERT INTO coll_t VALUES('a','a','x','')`, 1))
		conflict := fixture.Diagnostic{Code: "42P22", Message: "could not determine which collation to use for regular expression", Hint: "Use the COLLATE clause to set the collation explicitly."}
		for _, expr := range []string{`regexp_count(s,p)`, `regexp_instr(s,p)`, `regexp_like(s,p)`, `regexp_match(s,p)`, `regexp_matches(s,p)`, `regexp_replace(s,p,'x')`, `regexp_split_to_array(s,p)`, `regexp_split_to_table(s,p)`, `regexp_like(s,'a',f)`, `regexp_replace(s,'a',r)`} {
			regexpRoutes(t, p, b, similarReferenceError("SELECT "+expr+" FROM coll_t", conflict))
		}
		q := `SELECT regexp_like('a' COLLATE "C",'a' COLLATE "POSIX")`
		regexpRoutes(t, p, b, regexpBindError(q, "42P21", similarExplicitConflict, "", `COLLATE "POSIX"`))
		for _, coll := range []string{"C", "POSIX", "pg_c_utf8", "pg_unicode_fast"} {
			run(fixture.Exec(fmt.Sprintf(`CREATE COLLATION "regexp_copy_%s" FROM "%s"`, coll, coll), 0))
			unicode := coll == "pg_c_utf8" || coll == "pg_unicode_fast"
			for _, name := range []string{coll, "regexp_copy_" + coll} {
				for _, c := range []struct {
					s, p, flags string
					value       bool
				}{
					{"é", "[[:alpha:]]", "", unicode}, {"É", "é", "i", unicode}, {"Éé", `(É)\1`, "i", unicode}, {"字", "[[:upper:]]", "i", unicode}, {"ß", "SS", "i", false}, {"ς", "σ", "i", false}, {"\u0301", `\w`, "", false},
				} {
					regexpRoutes(t, p, b, regexpOne(b, fmt.Sprintf(`regexp_like($1::text,$2::text COLLATE "%s",$3::text)`, name), "BOOL", c.value, c.s, c.p, c.flags))
				}
			}
		}
	})
}
