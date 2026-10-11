package reference_test

import (
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePosixSearchCase(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			for _, c := range []struct {
				s, p                   string
				sensitive, insensitive bool
			}{
				{"AbC", "abc", false, true}, {"abc", "b", true, true}, {"abc", "^b", false, false}, {"", "", true, true}, {"abc", "", true, true}, {"AbC", "(?c)abc", false, false}, {"AbC", "(?i)abc", true, true},
			} {
				similarReferenceRoutes(t, p, b, posixSelect(`SELECT $1::text ~ $2::text AS s,$1::text ~* $2::text AS i,$1::text !~ $2::text AS n,$1::text !~* $2::text AS ni`, []string{"s", "i", "n", "ni"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{c.sensitive, c.insensitive, !c.sensitive, !c.insensitive}}, c.s, c.p))
			}
			for _, args := range [][]any{{nil, "["}, {"a", nil}, {nil, nil}} {
				similarReferenceRoutes(t, p, b, posixSelect(`SELECT $1::text ~ $2::text AS s,$1::text ~* $2::text AS i,$1::text !~ $2::text AS n,$1::text !~* $2::text AS ni`, []string{"s", "i", "n", "ni"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{nil, nil, nil, nil}}, args...))
			}
			similarReferenceRoutes(t, p, b, posixSelect(`SELECT substring('AbC' FROM '.*') OPERATOR(pg_catalog.~*) pg_catalog.substring('abc','.*') AS m`, []string{"m"}, []string{"BOOL"}, [][]any{{true}}))
			for _, c := range []struct {
				q       string
				matched bool
			}{
				{`SELECT pg_catalog.substring('AbC','.*') ~* 'abc' AS m`, true},
				{`SELECT 'AbC' ~* pg_catalog.substring('abc','.*') AS m`, true},
				{`SELECT substring('AbC' FROM '.*') !~ 'abc' AS m`, true},
				{`SELECT 'AbC' !~* pg_catalog.substring('abc','.*') AS m`, false},
			} {
				similarReferenceRoutes(t, p, b, posixSelect(c.q, []string{"m"}, []string{"BOOL"}, [][]any{{c.matched}}))
			}
		})
	}
}

func TestReferencePosixModes(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			for _, c := range []struct {
				s, p string
				m    bool
			}{
				{"5", `\d`, true}, {"d", `(?e)\d`, true}, {"5", `(?e)\d`, false}, {")", `(?e))`, true},
				{"()+?|{}", `(?b)()+?|{}`, true}, {"aaaa", `(?b)^\(aa\)\1$`, true}, {"aaa", `(?b)^a\{3\}$`, true}, {"*a", `(?b)^*a$`, true}, {"a+b", `(?b)a+b`, true},
				{"a^b", `(?b)a^b`, true}, {"a$b", `(?b)a$b`, true}, {"a.b", `***=a.b`, true}, {"axb", `***=a.b`, false}, {"7", `***:\d`, true},
			} {
				posixBool(t, p, b, c.s, c.p, c.m)
			}
		})
	}
}

func TestReferencePosixAtomsQuantifiers(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			for _, c := range []struct {
				s, p string
				m    bool
			}{
				{"", `a|`, true}, {"", `|a`, true}, {"", `()`, true}, {"🙂", `^.$`, true}, {"🙂x", `^.$`, false},
				{"a{z", `a{z`, true}, {"aa", `^a+$`, true}, {"", `^a?$`, true}, {"aaa", `^a{2,3}$`, true}, {"a", `^a{2,}$`, false},
				{strings.Repeat("a", 255), `^a{255}$`, true}, {"aaa", `^a*?$`, true}, {"aaa", `^a+?$`, true}, {"a", `^a??$`, true}, {"aaa", `^a{2,3}?$`, true},
			} {
				posixBool(t, p, b, c.s, c.p, c.m)
			}
		})
	}
}

func TestReferencePosixBracketsEscapes(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			for _, c := range []struct {
				s, p string
				m    bool
			}{
				{"]", `[]a]`, true}, {"a", `[^]a]`, false}, {"-", `[-a]`, true}, {"-", `[a-]`, true}, {"b", `[a-c]`, true}, {"d", `[a-c]`, false},
				{"a", `[[.a.]]`, true}, {"a", `[[=a=]]`, true}, {"\t", `[[.tab.]]`, true}, {"]", `[\u005d]`, true}, {"-", `[\x2d]`, true}, {"[", `[\133]`, true},
				{"5", `[\d]`, true}, {"a", `[\D]`, true}, {"\n", `[\s]`, true}, {"a", `[\S]`, true}, {"_", `[\w]`, true}, {"!", `[\W]`, true},
				{"\a", `\a`, true}, {"\b", `\b`, true}, {"\\", `\B`, true}, {"\x01", `\cA`, true}, {"\x1b", `\e`, true}, {"\f", `\f`, true}, {"\n", `\n`, true}, {"\r", `\r`, true}, {"\t", `\t`, true}, {"\v", `\v`, true},
				{"é", `\u00e9`, true}, {"🙂", `\U0001f642`, true}, {"A", `\x41`, true}, {"A", `\101`, true}, {"A", `\0101`, false}, {"\b1", `\0101`, true}, {".", `\.`, true}, {"#", `\#`, true},
			} {
				posixBool(t, p, b, c.s, c.p, c.m)
			}
			for _, c := range []struct{ class, s string }{
				{"alnum", "5"}, {"alpha", "a"}, {"blank", "\t"}, {"cntrl", "\n"}, {"digit", "7"}, {"graph", "!"}, {"lower", "a"}, {"print", " "}, {"punct", "!"}, {"space", "\n"}, {"upper", "A"}, {"xdigit", "F"}, {"ascii", "a"}, {"word", "_"},
			} {
				posixBool(t, p, b, c.s, "[[:"+c.class+":]]", true)
			}
		})
	}
}

func TestReferencePosixConstraintsLookaround(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			for _, c := range []struct {
				s, p string
				m    bool
			}{
				{"abc", `\Aabc\Z`, true}, {"xabc", `\Aabc\Z`, false}, {" abc!", `\mabc\M`, true}, {"xabc", `\mabc`, false}, {"abc", `\yabc\y`, true}, {"xabcx", `\Yabc\Y`, true}, {"_abc_", `[[:<:]]abc[[:>:]]`, false},
				{"ab", `a(?=b)`, true}, {"ac", `a(?!b)`, true}, {"ab", `a(?!b)`, false}, {"aab", `(?<=a+)b`, true}, {"cb", `(?<!a+)b`, true}, {"aab", `(?<!a+)b`, false},
				{"ab", `(a)(?=b(b)?)(b)\2?`, true}, {"aa", `(a)\1`, true}, {"ab", `(a)\1`, false}, {"abcdefghijj", `(a)(b)(c)(d)(e)(f)(g)(h)(i)(j)\10`, true}, {"a\b", `(a)\10`, true},
			} {
				posixBool(t, p, b, c.s, c.p, c.m)
			}
		})
	}
}

func TestReferencePosixOptionsNewlines(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			for _, mode := range []string{"", "s", "n", "m", "p", "w"} {
				prefix := ""
				if mode != "" {
					prefix = "(?" + mode + ")"
				}
				stop := mode == "n" || mode == "m" || mode == "p"
				anchors := mode == "n" || mode == "m" || mode == "w"
				for _, pat := range []string{`a.b`, `a[^x]b`} {
					posixBool(t, p, b, "a\nb", prefix+pat, !stop)
				}
				posixBool(t, p, b, "a\nb", prefix+`^b$`, anchors)
				posixBool(t, p, b, "a\n", prefix+`^a$`, anchors)
				posixBool(t, p, b, "a\nb", prefix+`\Ab\Z`, false)
				for _, pat := range []string{`\D`, `\W`} {
					posixBool(t, p, b, "\n", prefix+pat, true)
				}
				posixBool(t, p, b, "\n", prefix+`[^0-9]`, !stop)
			}
			for _, c := range []struct {
				s, p string
				m    bool
			}{
				{"a\rb", `(?n)a.b`, true}, {"a\u2028b", `(?n)a.b`, true}, {"a\r\nb", `(?n)^a$`, false},
				{"ab", "(?x) a # comment\n b", true}, {"a b", `(?x)a\ b`, true}, {"a#b", `(?x)a\#b`, true}, {" ", `(?x)[ ]`, true},
				{"a b", `(?xt)a b`, true}, {"ab", `(?tx)a b`, true}, {"a.b", `(?q)a.b`, true}, {"axb", `(?q)a.b`, false}, {"ab", `a(?#comment)b`, true},
			} {
				posixBool(t, p, b, c.s, c.p, c.m)
			}
		})
	}
}
