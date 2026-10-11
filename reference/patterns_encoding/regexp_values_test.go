package reference_test

import (
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRegexpCountInstrLike(t *testing.T) {
	regexpScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, c := range []struct {
			expr, typ string
			value     any
		}{
			{`regexp_count('ABCABCAXYaxy','A.')`, "INT4", int64(3)},
			{`regexp_count('ABCABCAXYaxy','A.',1,'i')`, "INT4", int64(4)},
			{`regexp_count('abc','z')`, "INT4", int64(0)},
			{`regexp_count('abc','^',2)`, "INT4", int64(0)},
			{`regexp_count('abc','\A',2)`, "INT4", int64(0)},
			{`regexp_count('abc','(?<=a)b',2)`, "INT4", int64(1)},
			{`regexp_count('abc','\mb',2)`, "INT4", int64(0)},
			{`regexp_count('abc','a',2147483647)`, "INT4", int64(0)},
			{`regexp_instr('é🙂x','🙂',1,1,0)`, "INT4", int64(2)},
			{`regexp_instr('é🙂x','🙂',1,1,1)`, "INT4", int64(3)},
			{`regexp_instr('xxabyyab','(a)(b)',1,2,0,'',2)`, "INT4", int64(8)},
			{`regexp_instr('xxabyyab','(a)(b)',1,2,1,'',2)`, "INT4", int64(9)},
			{`regexp_instr('b','(a)?b',1,1,0,'',1)`, "INT4", int64(0)},
			{`regexp_instr('b','(a*)b',1,1,0,'',1)`, "INT4", int64(1)},
			{`regexp_instr('b','(a*)b',1,1,1,'',1)`, "INT4", int64(1)},
			// REL_18_0 whole-match fallback with no numbered groups: oracle priority.
			{`regexp_instr('abc','b',1,1,0,'',1)`, "INT4", int64(2)},
			{`regexp_instr('abc','b',1,1,0,'',2)`, "INT4", int64(0)},
			{`regexp_instr('a','(a)',1,2147483647,0,'',1)`, "INT4", int64(0)},
			{`regexp_instr('a','(a)',1,1,0,'',2147483647)`, "INT4", int64(0)},
			{`regexp_instr('abc','z')`, "INT4", int64(0)},
			{`regexp_like('Hello World','world')`, "BOOL", false},
			{`regexp_like('Hello World','world','i')`, "BOOL", true},
			{`regexp_like('a  '::char(3),'^a$')`, "BOOL", true},
		} {
			regexpRoutes(t, p, b, regexpOne(b, c.expr, c.typ, c.value))
		}
	})
}

func TestReferenceRegexpCaptureArrays(t *testing.T) {
	regexpScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, c := range []struct {
			source, pattern string
			value           any
		}{
			{"foobarbequebaz", "bar.*que", "{barbeque}"}, {"foobarbequebaz", "(bar)(beque)", "{bar,beque}"},
			{"x", "(a)|(x)", "{NULL,x}"}, {"b", "(a*)b", `{""}`}, {"b", "(a)?b", "{NULL}"},
			{"aba", "(a(b)?)+", "{a,NULL}"}, {"abc01234xyz", `(.*?)(\d+)(.*)`, `{abc,0,""}`},
			{"abc01234xyz", `(?:(.*?)(\d+)(.*)){1,1}`, "{abc,01234,xyz}"},
			{"abcdefghijk", "(a)(b)(c)(d)(e)(f)(g)(h)(i)(j)(k)", "{a,b,c,d,e,f,g,h,i,j,k}"},
			{"ab", "(?=(a))(a)(?<=a)(b)", "{a,b}"},
			{"a", "((a)|(b))", "{a,a,NULL}"}, {"abc", "z", nil}, {"é🙂", "(é)(🙂)", "{é,🙂}"},
		} {
			regexpRoutes(t, p, b, regexpOne(b, `regexp_match($1::text,$2::text)`, "_TEXT", c.value, c.source, c.pattern))
			rows := [][]any{{c.value}}
			if c.value == nil {
				rows = nil
			}
			regexpRoutes(t, p, b, regexpRows(b, `SELECT regexp_matches($1::text,$2::text) AS result`, []string{"result"}, []string{"_TEXT"}, rows, c.source, c.pattern))
		}
	})
}

func TestReferenceRegexpGlobalBoundaries(t *testing.T) {
	regexpScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, c := range []struct {
			expr string
			n    int64
		}{
			{`regexp_count('aaa','aa')`, 1}, {`regexp_count('é🙂x','')`, 4},
			{`regexp_count('é🙂x','',4)`, 1}, {`regexp_count('é🙂x','',5)`, 0},
			{`regexp_count('ab','^|$')`, 2}, {`regexp_count('','')`, 1},
			{`regexp_count('abc','(?=.)')`, 3}, {`regexp_count('aaa','(a*)')`, 2},
		} {
			regexpRoutes(t, p, b, regexpOne(b, c.expr, "INT4", c.n))
		}
		for _, c := range []struct {
			expr string
			rows [][]any
		}{
			{`regexp_matches('ababa','(aba|a)','g')`, [][]any{{"{aba}"}, {"{a}"}}},
			{`regexp_matches('é🙂','','g')`, [][]any{{`{""}`}, {`{""}`}, {`{""}`}}},
			{`regexp_matches('ab','^|$','g')`, [][]any{{`{""}`}, {`{""}`}}},
			{`regexp_matches('aaa','(a*)','g')`, [][]any{{"{aaa}"}, {`{""}`}}},
			{`regexp_matches('abc','z','g')`, nil},
		} {
			regexpRoutes(t, p, b, regexpRows(b, "SELECT "+c.expr+" AS result", []string{"result"}, []string{"_TEXT"}, c.rows))
		}
	})
}

func TestReferenceRegexpNullDemand(t *testing.T) {
	regexpScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, c := range regexpSignatures() {
			for i := range c.values {
				args := append([]string(nil), c.values...)
				args[i] = "NULL::" + c.sqltypes[i]
				rows := [][]any{{nil}}
				if c.set {
					rows = nil
				}
				regexpRoutes(t, p, b, regexpRows(b, "SELECT "+c.name+"("+strings.Join(args, ",")+") AS result", []string{"result"}, []string{c.typ}, rows))
			}
		}
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Exec(`CREATE TABLE demand(v TEXT,p TEXT)`, 0))
		for _, populated := range []bool{false, true} {
			if populated {
				run(fixture.Exec(`INSERT INTO demand VALUES('a','['),(NULL,'[')`, 2))
			}
			for _, c := range []struct {
				expr, typ string
				set       bool
			}{
				{`regexp_count(v,p)`, "INT4", false}, {`regexp_instr(v,p)`, "INT4", false}, {`regexp_like(v,p)`, "BOOL", false}, {`regexp_match(v,p)`, "_TEXT", false},
				{`regexp_replace(v,p,'x')`, "TEXT", false}, {`regexp_split_to_array(v,p)`, "_TEXT", false}, {`regexp_matches(v,p)`, "_TEXT", true}, {`regexp_split_to_table(v,p)`, "TEXT", true},
			} {
				q := "SELECT " + c.expr + " AS result FROM demand"
				s := regexpRows(b, q, []string{"result"}, []string{c.typ}, nil)
				if populated {
					s = similarReferenceRegexError(q, "brackets [] not balanced", "")
				}
				regexpRoutes(t, p, b, s)
				regexpRoutes(t, p, b, regexpRows(b, q+" WHERE false", []string{"result"}, []string{c.typ}, nil))
				nullRows := [][]any(nil)
				if populated && !c.set {
					nullRows = [][]any{{nil}}
				}
				regexpRoutes(t, p, b, regexpRows(b, q+" WHERE v IS NULL", []string{"result"}, []string{c.typ}, nullRows))
			}
			for _, expr := range []string{`regexp_count('a','[')`, `regexp_instr('a','[')`, `regexp_like('a','[')`, `regexp_match('a','[')`, `regexp_replace('a','[','x')`, `regexp_split_to_array('a','[')`} {
				regexpRoutes(t, p, b, similarReferenceRegexError("SELECT "+expr+" FROM demand", "brackets [] not balanced", ""))
			}
			for _, c := range []struct{ expr, typ string }{
				{`regexp_matches('a','[')`, "_TEXT"}, {`regexp_split_to_table('a','[')`, "TEXT"},
			} {
				q := "SELECT " + c.expr + " AS result FROM demand"
				s := regexpRows(b, q, []string{"result"}, []string{c.typ}, nil)
				if populated {
					s = similarReferenceRegexError(q, "brackets [] not balanced", "")
				}
				regexpRoutes(t, p, b, s)
				regexpRoutes(t, p, b, regexpRows(b, q+" WHERE false", []string{"result"}, []string{c.typ}, nil))
			}
			// Suppressing the set invocation does not suppress folding a scalar child.
			for _, expr := range []string{`regexp_matches(regexp_replace('a','[','x'),'a')`, `regexp_split_to_table(regexp_replace('a','[','x'),' ')`} {
				regexpRoutes(t, p, b, similarReferenceRegexError("SELECT "+expr+" FROM demand", "brackets [] not balanced", ""))
			}
			for _, c := range []struct{ name, typ string }{{"regexp_matches", "_TEXT"}, {"regexp_split_to_table", "TEXT"}} {
				q := "SELECT r.result FROM demand d," + c.name + "(d.v,'[') r(result) WHERE false"
				regexpRoutes(t, p, b, regexpRows(b, q, []string{"result"}, []string{c.typ}, nil))
			}
		}
		regexpRoutes(t, p, b, regexpOne(b, `CASE WHEN false THEN regexp_like('a','[') ELSE true END`, "BOOL", true))
		regexpRoutes(t, p, b, regexpOne(b, `COALESCE(true,regexp_like('a','['))`, "BOOL", true))
		regexpRoutes(t, p, b, regexpOne(b, `regexp_instr(NULL::text,'[',0,0,2,'z',-1)`, "INT4", nil))
		// NULL strictness does not license suppressing demanded sibling expressions.
		regexpRoutes(t, p, b, regexpRuntimeError(`SELECT regexp_count(NULL::text,'a',1/(length($1::text)-1))`, "22012", "division by zero", "", "a"))
		for _, name := range []string{"regexp_count", "regexp_instr"} {
			regexpRoutes(t, p, b, similarReferenceRegexError(fmt.Sprintf("SELECT %s('a','[',2147483647)", name), "brackets [] not balanced", ""))
		}
	})
}

func TestReferenceRegexpReplacement(t *testing.T) {
	regexpScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, c := range []struct{ expr, value string }{
			{`regexp_replace('foobarbaz','b..','X')`, "fooXbaz"}, {`regexp_replace('foobarbaz','b..','X','g')`, "fooXX"},
			{`regexp_replace('foobarbaz','b(..)','X\1Y','g')`, "fooXarYXazY"},
			{`regexp_replace('abcabc','b','X',4)`, "abcaXc"}, {`regexp_replace('abcabc','b','X',4,0)`, "abcaXc"},
			{`regexp_replace('abcabc','b','X',1,2,'g')`, "abcaXc"}, {`regexp_replace('abcabc','b','X',1,0)`, "aXcaXc"},
			{`regexp_replace('ab','','X','g')`, "XaXbX"}, {`regexp_replace('é🙂','','X','g')`, "XéX🙂X"},
			{`regexp_replace('b','(a)?b','<\1><\&>')`, "<><b>"},
			{`regexp_replace('a','(a)','\10/\9/\q/\0/$1')`, `a0//\q/\0/$1`},
			{`regexp_replace('a','a','\\')`, `\`}, {`regexp_replace('a','a','X\')`, `X\`},
			{`regexp_replace('abc','^','X',2)`, "abc"}, {`regexp_replace('abc','(?<=a)b','X',2)`, "aXc"},
			{`regexp_replace('abc','z','X')`, "abc"}, {`regexp_replace('abc','b','X',1,9)`, "abc"},
			{`regexp_replace('abc','','X',4,0)`, "abcX"}, {`regexp_replace('abc','','X',5,0)`, "abc"},
			{`regexp_replace('a','a','X',2147483647,0)`, "a"},
		} {
			regexpRoutes(t, p, b, regexpOne(b, c.expr, "TEXT", c.value))
		}
	})
}

func TestReferenceRegexpSplits(t *testing.T) {
	regexpScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, c := range []struct {
			source, pattern, array string
			pieces                 []string
		}{
			{"a,,b,", ",", `{a,"",b,""}`, []string{"a", "", "b", ""}},
			{",a", ",", `{"",a}`, []string{"", "a"}},
			{"a1b2c", "([0-9])", "{a,b,c}", []string{"a", "b", "c"}},
			{"é🙂x", "", "{é,🙂,x}", []string{"é", "🙂", "x"}},
			{"abc", "x*", "{a,b,c}", []string{"a", "b", "c"}},
			{"a b", `\s*`, "{a,b}", []string{"a", "b"}},
			{"", "", `{""}`, []string{""}}, {"", "x", `{""}`, []string{""}},
			{"abc", "z", "{abc}", []string{"abc"}},
			{"abc", "^|$", "{abc}", []string{"abc"}},
			{"ab", "a*", `{"",b}`, []string{"", "b"}},
			{"ab", "b*", `{a,""}`, []string{"a", ""}},
			{"a", "a|$", `{"",""}`, []string{"", ""}},
		} {
			regexpRoutes(t, p, b, regexpOne(b, `regexp_split_to_array($1::text,$2::text)`, "_TEXT", c.array, c.source, c.pattern))
			rows := make([][]any, len(c.pieces))
			for i, v := range c.pieces {
				rows[i] = []any{v}
			}
			regexpRoutes(t, p, b, regexpRows(b, `SELECT regexp_split_to_table($1::text,$2::text) AS result`, []string{"result"}, []string{"TEXT"}, rows, c.source, c.pattern))
		}
	})
}
