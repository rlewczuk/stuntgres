package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceLtreeLqueryMatching(t *testing.T) {
	ltreeLqueryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		ltreeInstall(t, p, b, c)
		for _, tc := range []struct{ path, query string }{{"NULL::ltree","'a'::lquery"},{"'a'::ltree","NULL::lquery"},{"NULL::ltree","NULL::lquery"}} {
			for _, q := range []string{
				"SELECT "+tc.path+" ~ "+tc.query+" AS forward,"+tc.query+" ~ "+tc.path+" AS reverse",
				"SELECT ltq_regex("+tc.path+","+tc.query+") AS forward,ltq_rregex("+tc.query+","+tc.path+") AS reverse",
			} { s:=ltreeLqueryPair(b,q,nil);ltreeLqueryRun(t,p,b,c,s);ltreeLqueryRun(t,p,b,c,fixture.Prepared(s)) }
		}
		for _, tc := range []struct{ path, query string; want bool }{
			{"Top.A.Child", "Top.*{1,2}.Child", true}, {"Top.A.B.Child", "Top.*{1,2}.Child", true},
			{"Top.Child", "Top.*{1,2}.Child", false}, {"Top.A.B.C.Child", "Top.*{1,2}.Child", false},
			{"Other.A.Child", "Top.*{1,2}.Child", false}, {"Top.A.Child.Tail", "Top.*{1,2}.Child", false}, {"top.A.Child", "Top.*{1,2}.Child", false},
			{"", "*", true}, {"", "a{0}", true}, {"", "a", false}, {"a.b", "a", false},
			{"a.a", "a{0,2}.a", true}, {"a.a.b", "a{1,2}.b", true}, {"a.b", "a{2}.b", false},
			{"a.b", "*.*.b.*{0}", true}, {"a", "a{0}.a", true},
			{"a.a", "a{2}", true}, {"a.a.a", "a{2,}", true}, {"a", "a{,2}", true}, {"", "a{,}", true},
			{"a.b", "*{2}", true}, {"a.b.c", "*{2,}", true}, {"a", "*{,2}", true}, {"", "*{,}", true},
			{"b", "a|b", true}, {"c", "a|b", false}, {"a.c", "a.!b|d", true}, {"a.b", "a.!b|d", false},
			{"c.c", "!a|b{2}", true}, {"c.b", "!a|b{2}", false},
			{"Foo", "foo", false}, {"Foo", "foo@", true}, {"food", "foo*", true}, {"fo", "foo*", false}, {"food", "foo", false},
			{"bar_foo", "foo_bar%", true}, {"foo", "foo_foo%", true}, {"a", "_%", true},
			{"_bar__foo_", "__foo__bar_%", true}, {"foo-bar", "foo_bar%", false},
			{"foo1_bar2_baz", "foo_bar%*", true}, {"foo1_br2_baz", "foo_bar%*", false}, {"FOO1_BAR2_baz", "foo_bar%@*", true},
			{"Top.sporting.swim.Russia", "Top.*{0,2}.sport*@.!football|tennis{1,}.Russ*|Spain", true},
			{"Top.sporting.football.Russia", "Top.*{0,2}.sport*@.!football|tennis{1,}.Russ*|Spain", false},
			{"Top.sporting.swim.France", "Top.*{0,2}.sport*@.!football|tennis{1,}.Russ*|Spain", false},
		} {
			path, query := queryIntLiteral(tc.path)+"::ltree", queryIntLiteral(tc.query)+"::lquery"
			q := "SELECT "+path+" ~ "+query+" AS forward,"+query+" ~ "+path+" AS reverse"
			s := ltreeLqueryPair(b, q, tc.want)
			ltreeLqueryRun(t, p, b, c, s)
			ltreeLqueryRun(t, p, b, c, fixture.Prepared(s))
		}
	})
}
