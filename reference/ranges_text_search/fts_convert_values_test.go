package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

var ftsConvertNames = []string{"to_tsvector", "to_tsquery", "plainto_tsquery", "phraseto_tsquery", "websearch_to_tsquery"}

func ftsConvertType(name string) string {
	if name == "to_tsvector" {
		return "TSVECTOR"
	}
	return "TSQUERY"
}
func ftsConvertValue(query, name string, value any, args ...any) fixture.Step {
	return fixture.Select(query, []string{"normalized"}, []string{ftsConvertType(name)}, [][]any{{value}}, args...)
}
func ftsConvertCases(t *testing.T, cases [][4]string) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, tc := range cases {
		query := fmt.Sprintf("SELECT %s('%s'::regconfig,$1::text) AS normalized", tc[0], tc[1])
		step := ftsConvertValue(query, tc[0], tc[3], tc[2])
		p.Step(t, step)
		p.Step(t, fixture.Prepared(step))
	}
}

func TestReferenceFTSConvertDocuments(t *testing.T) {
	ftsConvertCases(t, [][4]string{
		{"to_tsvector", "simple", "The Fat Rats", "'fat':2 'rats':3 'the':1"},
		{"to_tsvector", "english", "The Fat Rats", "'fat':2 'rat':3"},
		{"to_tsvector", "simple", "Rats rats", "'rats':1,2"},
		{"to_tsvector", "simple", "Foo-Bar", "'bar':3 'foo':2 'foo-bar':1"},
		{"to_tsvector", "english", "in the list of stop words", "'list':3 'stop':5 'word':6"},
		{"to_tsvector", "english", "a fat  cat sat on a mat - it ate a fat rats", "'ate':9 'cat':3 'fat':2,11 'mat':7 'rat':12 'sat':4"},
	})
}
func TestReferenceFTSConvertQueries(t *testing.T) {
	ftsConvertCases(t, [][4]string{
		{"to_tsquery", "english", "The & Fat & Rats", "'fat' & 'rat'"},
		{"to_tsquery", "english", "Fat | Rats:AB", "'fat' | 'rat':AB"},
		{"to_tsquery", "english", "supern:*A & star:A*B", "'supern':*A & 'star':*AB"},
		{"to_tsquery", "simple", "a | b & !c <0> d", "'a' | 'b' & !'c' <0> 'd'"},
		{"to_tsquery", "simple", "a <16384> b", "'a' <16384> 'b'"},
		{"to_tsquery", "simple", "!(a | b) & c", "!( 'a' | 'b' ) & 'c'"},
		{"to_tsquery", "simple", "'Foo-Bar':*AB", "'foo-bar':*AB <-> 'foo':*AB <-> 'bar':*AB"},
		{"to_tsquery", "simple", "'a\\ b'", "'a' <-> 'b'"},
		{"to_tsquery", "english", "'" + strings.Repeat("cat ", 599) + "cat'", strings.Repeat("'cat' <-> ", 599) + "'cat'"},
		{"plainto_tsquery", "english", "The Fat & Rats:C", "'fat' & 'rat' & 'c'"},
		{"phraseto_tsquery", "english", "The Fat & Rats:C", "'fat' <-> 'rat' <-> 'c'"},
		{"phraseto_tsquery", "english", "The Cat and Rats", "'cat' <2> 'rat'"},
		{"phraseto_tsquery", "english", "PostgreSQL can be extended by the user in many ways", "'postgresql' <3> 'extend' <3> 'user' <2> 'mani' <-> 'way'"},
	})
	p := fixture.NewScenario(t)
	// Raw typed input must not acquire configuration normalization.
	s := fixture.Select("SELECT 'The & Rats:*AB'::tsquery AS raw", []string{"raw"}, []string{"TSQUERY"}, [][]any{{"'The' & 'Rats':*AB"}})
	p.Step(t, s)
	p.Step(t, fixture.Prepared(s))
}
func TestReferenceFTSConvertStopwords(t *testing.T) {
	ftsConvertCases(t, [][4]string{
		{"to_tsquery", "english", "(1 <-> a) <-> 2", "'1' <2> '2'"},
		{"to_tsquery", "english", "1 <-> (a <-> 2)", "'1' <2> '2'"},
		{"to_tsquery", "english", "(1 <-> a) <3> 2", "'1' <4> '2'"},
		{"to_tsquery", "english", "foo <-> a <-> the <-> bar", "'foo' <3> 'bar'"},
		{"to_tsquery", "english", "foo <-> 'the bar'", "'foo' <-> 'bar'"},
		{"to_tsquery", "english", "'foo the' <-> bar", "'foo' <-> 'bar'"},
		{"to_tsquery", "english", "foo <-> 'bar the baz'", "'foo' <-> ( 'bar' <2> 'baz' )"},
		{"to_tsquery", "english", "foo <-> 'the a' <-> bar", "'foo' <2> 'bar'"},
		{"to_tsquery", "english", "!(a & !b) & c", "!!'b' & 'c'"},
		{"phraseto_tsquery", "english", "the cat and rats the", "'cat' <2> 'rat'"},
		{"plainto_tsquery", "english", "the cat and rats the", "'cat' & 'rat'"},
	})
}
func TestReferenceFTSConvertWeb(t *testing.T) {
	ftsConvertCases(t, [][4]string{
		{"websearch_to_tsquery", "english", `"sad cat" or "fat rat"`, "'sad' <-> 'cat' | 'fat' <-> 'rat'"},
		{"websearch_to_tsquery", "english", `signal -"segmentation fault"`, "'signal' & !( 'segment' <-> 'fault' )"},
		{"websearch_to_tsquery", "simple", "abc | def", "'abc' & 'def'"},
		{"websearch_to_tsquery", "simple", "fat*rat", "'fat' <-> 'rat'"},
		{"websearch_to_tsquery", "simple", "fat-rat", "'fat-rat' <-> 'fat' <-> 'rat'"},
		{"websearch_to_tsquery", "simple", "cat OR", "'cat' & 'or'"},
		{"websearch_to_tsquery", "simple", "OR rat", "'or' & 'rat'"},
		{"websearch_to_tsquery", "simple", `"fat cat"or"fat rat"`, "'fat' <-> 'cat' | 'fat' <-> 'rat'"},
		{"websearch_to_tsquery", "english", `"pg_class pg`, "'pg' <-> 'class' <-> 'pg'"},
		{"websearch_to_tsquery", "english", "this is ----fine", "!!!!'fine'"},
		{"websearch_to_tsquery", "english", `(()) )))) this ||| is && -fine, "dear friend" OR good`, "!'fine' & 'dear' <-> 'friend' | 'good'"},
		{"websearch_to_tsquery", "english", "'", ""},
		{"websearch_to_tsquery", "english", "\\", ""},
		{"websearch_to_tsquery", "english", `"" cat ""`, "'cat'"},
		{"websearch_to_tsquery", "english", "cat &", "'cat'"},
		{"websearch_to_tsquery", "simple", `cat OR "" dog`, "'cat' | 'dog'"},
		{"websearch_to_tsquery", "simple", "cat OR,dog", "'cat' | 'dog'"},
		{"websearch_to_tsquery", "simple", "cat OR|dog", "'cat' | 'dog'"},
		{"websearch_to_tsquery", "simple", "cat OR,", "'cat' & 'or'"},
		{"websearch_to_tsquery", "simple", "cat OR-", "'cat' & 'or'"},
		{"websearch_to_tsquery", "simple", "cat OR)", "'cat' & 'or'"},
		{"websearch_to_tsquery", "simple", "cat OR-dog", "'cat' & 'or-dog' <-> 'or' <-> 'dog'"},
		{"websearch_to_tsquery", "simple", `cat OR""`, "'cat'"},
		{"websearch_to_tsquery", "simple", `cat OR ""`, "'cat'"},
		{"websearch_to_tsquery", "simple", `"" OR dog`, "'dog'"},
		{"websearch_to_tsquery", "simple", `cat OR "the" dog`, "'cat' | 'the' & 'dog'"},
		{"websearch_to_tsquery", "simple", `""`, ""},
	})
}
func TestReferenceFTSConvertNullEmpty(t *testing.T) {
	ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		defer ftsConvertSetting(t, p, c)()
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, name := range ftsConvertNames {
			for _, explicit := range []bool{false, true} {
				prefix := ""
				if explicit {
					prefix = "'english'::regconfig,"
				}
				for _, tc := range []struct {
					input string
					want  any
				}{{"NULL::text", nil}, {"''::text", ""}, {"'the'::text", ""}} {
					s := ftsConvertValue("SELECT "+name+"("+prefix+tc.input+") AS normalized", name, tc.want)
					run(s)
					run(fixture.Prepared(s))
				}
			}
			s := ftsConvertValue("SELECT "+name+"(NULL::regconfig,'words'::text) AS normalized", name, nil)
			run(s)
			run(fixture.Prepared(s))
			want := "'the'"
			if name == "to_tsvector" {
				want = "'the':1"
			}
			s = ftsConvertValue("SELECT "+name+"('simple'::regconfig,'the') AS normalized", name, want)
			run(s)
			run(fixture.Prepared(s))
			// Strictness bypasses even a missing numeric identity at execution.
			s = ftsConvertValue("SELECT "+name+"(0::regconfig,NULL::text) AS normalized", name, nil)
			run(s)
			run(fixture.Prepared(s))
		}
	})
}
