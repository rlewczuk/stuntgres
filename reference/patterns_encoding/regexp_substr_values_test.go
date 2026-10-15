package reference_test

import (
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func regexpSubstrValues(t *testing.T, cases []struct {
	args string
	want any
}) {
	t.Helper()
	regexpScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, c := range cases {
			regexpRoutes(t, p, b, regexpOne(b, "regexp_substr("+c.args+")", "TEXT", c.want))
		}
	})
}

func TestReferenceRegexpSubstrSelection(t *testing.T) {
	regexpSubstrValues(t, []struct {
		args string
		want any
	}{
		{`'foobar','o(.)b'`, "oob"}, {`'a1b22c333','[0-9]+',1,2`, "22"},
		{`'ababa','aba',1,2`, nil}, {`'a','a',1,2147483647`, nil}, {`'abc','z'`, nil},
		{`'é🙂x','',1,4`, ""}, {`'é🙂x','',1,5`, nil}, {`'aaa','a*',1,2`, ""}, {`'aaa','a*',1,3`, nil},
	})
}
func TestReferenceRegexpSubstrStart(t *testing.T) {
	regexpSubstrValues(t, []struct {
		args string
		want any
	}{
		{`'a1b22c333','[0-9]+',4`, "22"}, {`'é🙂x','.',2`, "🙂"}, {`'abc','^',2`, nil},
		{`'abc','\A',2`, nil}, {`'abc','(?<=a)b',2`, "b"}, {`'abc','\mb',2`, nil},
		{`'é🙂x','',4`, ""}, {`'é🙂x','',5`, nil}, {`'abc','b',2147483647`, nil},
	})
}
func TestReferenceRegexpSubstrFlags(t *testing.T) {
	regexpSubstrValues(t, []struct {
		args string
		want any
	}{
		{`'ABCDEF','c(.)(..)',1,1,'i'`, "CDEF"}, {`'ABCDEF','c(.)(..)',1,1,'i',2`, "EF"},
		{`'A','a',1,1,'ic'`, nil}, {`'A','a',1,1,'ci'`, "A"}, {`'A','(?c)a',1,1,'i'`, nil},
		{`'a.b','a.b',1,1,'q'`, "a.b"}, {`'axb','a.b',1,1,'q'`, nil},
		{`'a b','a b',1,1,'x'`, nil}, {`'a b','a b',1,1,'xt'`, "a b"}, {`'a b','a b',1,1,'xqt'`, "a b"},
		{`E'a\nb','^b',1,1,'n'`, "b"}, {`E'a\nb','^b',1,1,'m'`, "b"}, {`E'a\nb','^b',1,1,'w'`, "b"},
		{`E'a\nb','^b',1,1,'p'`, nil}, {`E'a\nb','^b',1,1,'s'`, nil},
		{`E'a\nb','a.b',1,1,'p'`, nil}, {`E'a\nb','a.b',1,1,'n'`, nil},
		{`E'a\nb','a.b',1,1,'w'`, "a\nb"}, {`E'a\nb','a.b',1,1,'s'`, "a\nb"},
		{`'aaab','a+b',1,1,'e'`, nil}, {`'aaab','(?e)a+b'`, "aaab"},
		{`'ab','\(a\)b',1,1,'b',1`, "a"}, {`'a+b','a+b',1,1,'qe'`, "a+b"},
	})
}
func TestReferenceRegexpSubstrCaptures(t *testing.T) {
	cases := []struct {
		args string
		want any
	}{
		{`'foobar','o(.)b',1,1,'',1`, "o"}, {`'b ab','(a)?b',1,1,'',1`, nil},
		{`'b ab','(a)?b',1,2,'',1`, "a"}, {`'b','(a*)b',1,1,'',1`, ""},
		{`'abc','b',1,1,'',1`, "b"}, {`'abc','b',1,1,'',2`, nil}, {`'aba','(a(b)?)+',1,1,'',2`, nil},
	}
	for i, w := range []any{"12345678", "123", "45678", "56", "78", nil} {
		cases = append(cases, struct {
			args string
			want any
		}{fmt.Sprintf(`'1234567890','(123)(4(56)(78))',1,1,'',%d`, i), w})
	}
	regexpSubstrValues(t, cases)
}
func TestReferenceRegexpSubstrNullDemand(t *testing.T) {
	regexpScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, c := range regexpSignatures() {
			if c.name != "regexp_substr" {
				continue
			}
			for i := range c.values {
				a := append([]string(nil), c.values...)
				a[i] = "NULL::" + c.sqltypes[i]
				regexpRoutes(t, p, b, regexpOne(b, "regexp_substr("+strings.Join(a, ",")+")", "TEXT", nil))
			}
		}
		regexpRoutes(t, p, b, regexpOne(b, `regexp_substr(NULL::text,'[',0,0,'z',-1)`, "TEXT", nil))
		regexpRoutes(t, p, b, regexpRuntimeError(`SELECT regexp_substr(NULL::text,'a',1/(length($1::text)-1))`, "22012", "division by zero", "", "a"))
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Exec(`CREATE TABLE substr_demand(s TEXT,p TEXT)`, 0))
		for _, populated := range []bool{false, true} {
			if populated {
				run(fixture.Exec(`INSERT INTO substr_demand VALUES('a','['),(NULL,'[')`, 2))
			}
			q := `SELECT regexp_substr(s,p) AS result FROM substr_demand`
			s := regexpRows(b, q, []string{"result"}, []string{"TEXT"}, nil)
			if populated {
				s = similarReferenceRegexError(q, "brackets [] not balanced", "")
			}
			regexpRoutes(t, p, b, s)
			regexpRoutes(t, p, b, regexpRows(b, q+" WHERE false", []string{"result"}, []string{"TEXT"}, nil))
		}
		regexpRoutes(t, p, b, regexpOne(b, `CASE WHEN false THEN regexp_substr('a','[') ELSE 'ok' END`, "TEXT", "ok"))
		regexpRoutes(t, p, b, similarReferenceRegexError(`SELECT regexp_substr('a','[')`, "brackets [] not balanced", ""))
		for _, q := range []string{`SELECT regexp_substr('a','z')`, `SELECT regexp_substr(NULL::text,'a')`, `SELECT regexp_substr('a','a') WHERE false`} {
			rows := [][]any{{nil}}
			if strings.HasSuffix(q, "false") {
				rows = nil
			}
			regexpRoutes(t, p, b, regexpRows(b, q, []string{"regexp_substr"}, []string{"TEXT"}, rows))
		}
	})
}
func TestReferenceRegexpSubstrFeatureSmoke(t *testing.T) {
	regexpSubstrValues(t, []struct {
		args string
		want any
	}{
		{`'foobar','o(.)b'`, "oob"}, {`'foobar','o(.)b',1,1,'',1`, "o"}, {`'é🙂x','.',2`, "🙂"},
		{`'a1b22','[0-9]+',1,2`, "22"}, {`'a','z'`, nil}, {`'a','a',1,1,NULL::text`, nil},
	})
}
