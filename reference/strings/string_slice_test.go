package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func stringSliceScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			body(t, p, b, c)
		})
	}
}

func TestReferenceStringSliceEnds(t *testing.T) {
	stringSliceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct{ count, left, right string }{
			{"2", "ab", "de"}, {"-2", "abc", "cde"}, {"0", "", ""},
			{"5", "abcde", "abcde"}, {"99", "abcde", "abcde"},
			{"-5", "", ""}, {"-99", "", ""}, {"2147483647", "abcde", "abcde"},
		} {
			for _, f := range []struct{ name, want string }{{"left", tc.left}, {"right", tc.right}} {
				t.Run(f.name+tc.count, func(t *testing.T) {
					stringPadValue(t, p, b, c, f.name+"('abcde',"+tc.count+")", "TEXT", f.want)
					stringPadValue(t, p, b, c, f.name+"('',"+tc.count+")", "TEXT", "")
				})
			}
		}
		stringPadValue(t, p, b, c, "left('Aé🙂Z',-2147483648)", "TEXT", "")
	})
}

func TestReferenceStringSlicePositions(t *testing.T) {
	stringSliceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct{ args, want string }{
			{"0,3", "ab"}, {"-2,4", "a"}, {"-2,2", ""}, {"1,0", ""}, {"99,2", ""},
			{"2,2147483647", "bcde"}, {"-2147483648,2147483647", ""}, {"2147483647,2", ""},
			{"2", "bcde"}, {"0", "abcde"}, {"-2", "abcde"}, {"-2147483648", "abcde"}, {"99", ""},
		} {
			for _, name := range []string{"substring", "substr"} {
				t.Run(name+tc.args, func(t *testing.T) { stringPadValue(t, p, b, c, name+"('abcde',"+tc.args+")", "TEXT", tc.want) })
			}
		}
	})
}

func TestReferenceStringSliceForms(t *testing.T) {
	stringSliceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct{ expr, want string }{
			{"substring('abcde' FROM 2 FOR 3)", "bcd"}, {"substring('abcde' FOR 3 FROM 2)", "bcd"},
			{"substring('abcde' FROM 2)", "bcde"}, {"substring('abcde' FOR 3)", "abc"},
			{"substring('abcde' FOR '2')", "ab"}, {"substring('abcde' FROM 'b.d')", "bcd"},
			{`"substring"('abcde',2,3)`, "bcd"}, {"pg_catalog.substring('abcde',2,3)", "bcd"},
			{`"substr"('abcde',2)`, "bcde"}, {"pg_catalog.substr('abcde',2)", "bcde"},
			{`"left"('abcde',2)`, "ab"}, {"pg_catalog.left('abcde',2)", "ab"},
			{`"right"('abcde',2)`, "de"}, {"pg_catalog.right('abcde',2)", "de"},
			{"left('abcde',VARIADIC 2)", "ab"}, {"right('abcde',VARIADIC 2)", "de"},
			{"substr('abcde',VARIADIC 2)", "bcde"},
		} {
			t.Run(tc.expr, func(t *testing.T) { stringPadValue(t, p, b, c, tc.expr, "TEXT", tc.want) })
		}
		stringPadBoth(t, p, b, c, fixture.Select("SELECT left('abc',1),right('abc',1),substring('abc',2),substr('abc',2)", []string{"left", "right", "substring", "substr"}, []string{"TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{"a", "c", "bc", "bc"}}))
		for _, nonContext := range []bool{false, true} {
			s := fixture.Select("SELECT substring('abc' FROM 2 FOR 1) AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"b"}})
			s.NonContext = nonContext
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(s))
		}
	})
}

func TestReferenceStringSliceMatchingForms(t *testing.T) {
	stringSliceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct {
			expr string
			want any
		}{
			{"substring('foobar' FROM 'o.b')", "oob"}, {"substring('foobar','o(.)b')", "o"},
			{"pg_catalog.substring('foobar','o(.)b')", "o"}, {"substring('foo','foo(bar)?')", nil},
			{"substring('foo','()foo')", ""}, {"substring('foo','z')", nil},
			{`substring('foobar' SIMILAR '%#"o_b#"%' ESCAPE '#')`, "oob"},
			{`substring('foobar' FROM '%#"o_b#"%' FOR '#')`, "oob"},
			{`substring('foobar','%#"o_b#"%','#')`, "oob"},
			{`pg_catalog.substring('foobar','%#"o_b#"%','#')`, "oob"},
			{`substring('foobar' SIMILAR '#"o_b#"%' ESCAPE '#')`, nil},
			{`substring('abcdefg' SIMILAR 'a#"%g' ESCAPE '#')`, "bcdefg"},
			{`substring('abcdefg' SIMILAR 'a%g' ESCAPE '#')`, "abcdefg"},
		} {
			t.Run(tc.expr, func(t *testing.T) { stringPadValue(t, p, b, c, tc.expr, "TEXT", tc.want) })
		}
	})
}

func TestReferenceStringSliceCoercion(t *testing.T) {
	stringSliceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE DOMAIN slice_text AS text", 0))
		for _, tc := range []struct{ expr, want string }{
			{"substring('Aé🙂Z',2,2)", "é🙂"}, {"left('Aé🙂Z',2)", "Aé"}, {"right('Aé🙂Z',2)", "🙂Z"},
			{"substring('é🙂',3)", ""}, {"substring('é🙂',3,2)", ""},
			{"substring('é🙂',2,4)", "🙂"}, {"substring('é🙂',2,2147483647)", "🙂"},
			{`left(U&'e\0301',1)`, "e"}, {`substring(U&'e\0301',2,1)`, "\u0301"},
			{"right('ab  '::char(4),2)", "ab"}, {"right('ab  '::varchar(4),2)", "  "},
			{"substr('abc'::name,2::smallint)", "bc"}, {"substring('abc'::slice_text,2,2)", "bc"},
			{"left('abc','2')", "ab"}, {"substr('abc','2','1')", "b"},
		} {
			t.Run(tc.expr, func(t *testing.T) { stringPadValue(t, p, b, c, tc.expr, "TEXT", tc.want) })
		}
		stringPadBoth(t, p, b, c, fixture.Select("SELECT left('abc',1) AS l,substr('abc',2,1) AS s WHERE FALSE", []string{"l", "s"}, []string{"TEXT", "TEXT"}, nil))
	})
}

func TestReferenceStringSliceNullDemand(t *testing.T) {
	stringSliceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, f := range []struct {
			name string
			args []string
		}{
			{"left", []string{"'abc'", "2"}}, {"right", []string{"'abc'", "2"}},
			{"substr", []string{"'abc'", "2"}}, {"substring", []string{"'abc'", "2", "1"}},
			{"substring", []string{"'abc'", "'b'"}}, {"substring", []string{"'abc'", "'%b%'", "'#'"}},
		} {
			for slot := range f.args {
				args := append([]string(nil), f.args...)
				args[slot] = "NULL"
				expr := f.name + "(" + strings.Join(args, ",") + ")"
				t.Run(expr, func(t *testing.T) { stringPadValue(t, p, b, c, expr, "TEXT", nil) })
			}
		}
		for _, expr := range []string{"substring(NULL::text,1,-1)", "substring('abc',1,NULL::int)", "substring('abc',NULL::int,2)"} {
			stringPadValue(t, p, b, c, expr, "TEXT", nil)
		}
		stringPadValue(t, p, b, c, "CASE WHEN TRUE THEN 'safe' ELSE substring('abc',1,-1) END", "TEXT", "safe")
		stringPadValue(t, p, b, c, "COALESCE('safe',substring('abc',1,-1))", "TEXT", "safe")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE slice_null(s text,n int)", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO slice_null VALUES(NULL,-1),('abc',NULL)", 2))
		stringPadBoth(t, p, b, c, fixture.Select("SELECT substring(s,1,n) AS v FROM slice_null ORDER BY n NULLS LAST", []string{"v"}, []string{"TEXT"}, [][]any{{nil}, {nil}}))
	})
}
