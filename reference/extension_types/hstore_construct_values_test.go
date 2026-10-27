package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceHstoreConstructValues(t *testing.T) {
	hstoreScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		hstoreInstall(t, p, b, c)
		for _, tc := range []struct {
			expression string
			value      any
		}{
			{`hstore('a','b')`, `"a"=>"b"`},
			{`hstore('a',NULL::text)`, `"a"=>NULL`},
			{`hstore('a','NULL')`, `"a"=>"NULL"`},
			{`hstore(NULL::text,'b')`, nil}, {`hstore(NULL::text,NULL::text)`, nil},
			{`hstore('','')`, `""=>""`},
			{`hstore($$a"b\c$$,$$x"y\z$$)`, `"a\"b\\c"=>"x\"y\\z"`},
			{`hstore('a,b=> c','雪 β')`, `"a,b=> c"=>"雪 β"`},
			{`hstore(ARRAY['a','1','b',NULL])`, `"a"=>"1", "b"=>NULL`},
			{`hstore(ARRAY[['a','1'],['b','2']])`, `"a"=>"1", "b"=>"2"`},
			{`hstore('[0:3]={a,1,b,2}'::text[])`, `"a"=>"1", "b"=>"2"`},
			{`hstore('[0:1][-1:0]={{a,1},{b,2}}'::text[])`, `"a"=>"1", "b"=>"2"`},
			{`hstore(ARRAY['a','b'],NULL::text[])`, `"a"=>NULL, "b"=>NULL`},
			{`hstore('[0:1]={a,b}'::text[],'[0:1]={1,2}'::text[])`, `"a"=>"1", "b"=>"2"`},
			{`hstore(ARRAY[]::text[])`, ""}, {`hstore(ARRAY[]::text[],ARRAY[]::text[])`, ""},
			{`hstore(NULL::text[])`, nil}, {`hstore(NULL::text[],ARRAY[['1']])`, nil},
			{`hstore(ARRAY['a','1','a','1'])`, `"a"=>"1"`},
		} {
			for _, prepared := range []bool{false, true} {
				s := hstoreSelect(b, "SELECT "+tc.expression+" AS v", []string{"v"}, [][]any{{tc.value}}, nil)
				s.Prepared = prepared
				hstoreRun(t, p, b, c, s)
			}
		}
		// Conflicting duplicates have no specified winner, but must be unique and original.
		hstoreRun(t, p, b, c, fixture.Select(`SELECT cardinality(hstore_to_array(hstore(ARRAY['a','1','a','2']))) AS n,fetchval(hstore(ARRAY['a','1','a','2']),'a') IN ('1','2') AS original`, []string{"n", "original"}, []string{"INT4", "BOOL"}, [][]any{{int64(2), true}}))
		for _, q := range []string{"SELECT hstore($1::text,$2::text) AS v", "SELECT hstore($1::text[]) AS v", "SELECT hstore($1::text[],$2::text[]) AS v"} {
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			var args [][]any
			switch q {
			case "SELECT hstore($1::text,$2::text) AS v":
				args = [][]any{{"a", "1"}, {"a", nil}, {nil, "1"}}
			case "SELECT hstore($1::text[]) AS v":
				args = [][]any{{"{a,1}"}, {"{a,NULL}"}, {nil}}
			default:
				args = [][]any{{"{a}", "{1}"}, {"{a}", nil}, {nil, "{1}"}}
			}
			for i, value := range []any{`"a"=>"1"`, `"a"=>NULL`, nil} {
				s := hstoreSelect(b, q, []string{"v"}, [][]any{{value}}, nil, args[i]...)
				hstoreRun(t, p, b, c, s)
				hstoreRun(t, p, b, c, fixture.Prepared(s))
				hstoreRun(t, p, b, held, s)
			}
			if err := stmt.Close(); err != nil {
				t.Fatal(err)
			}
		}
		hstoreRun(t, p, b, c, fixture.Exec("CREATE TABLE hs_construct_values(id integer,k text,v text,h hstore)", 0))
		hstoreRun(t, p, b, c, fixture.Exec("INSERT INTO hs_construct_values VALUES (1,'a',NULL,hstore('a',NULL::text)),(2,'a','1',hstore(ARRAY['a','1']))", 2))
		hstoreRun(t, p, b, c, fixture.Exec("UPDATE hs_construct_values SET h=hstore(k,v) WHERE id=2", 1))
		for _, predicate := range []string{"exist(hstore(k,v),'a') AND NOT defined(hstore(k,v),'a')", "exist(h,'a') AND h->'a' IS NULL"} {
			s := fixture.Select("SELECT id FROM hs_construct_values WHERE "+predicate+" ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}})
			hstoreRun(t, p, b, c, s)
			hstoreRun(t, p, b, c, fixture.Prepared(s))
		}
	})
}

func TestReferenceHstoreConstructLookup(t *testing.T) {
	hstoreScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		hstoreInstall(t, p, b, c)
		q := `SELECT fetchval($1::hstore,$2::text) AS f,$1::hstore -> $2::text AS o,exist($1::hstore,$2::text) AS e,defined($1::hstore,$2::text) AS d`
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, tc := range []struct {
			h, key          any
			value           any
			exists, defined any
		}{
			{`a=>1,b=>NULL,c=>"NULL",d=>""`, "a", "1", true, true},
			{`a=>1,b=>NULL,c=>"NULL",d=>""`, "b", nil, true, false},
			{`a=>1,b=>NULL,c=>"NULL",d=>""`, "c", "NULL", true, true},
			{`a=>1,b=>NULL,c=>"NULL",d=>""`, "d", "", true, true},
			{`a=>1`, "missing", nil, false, false}, {`a=>1`, nil, nil, nil, nil}, {nil, "a", nil, nil, nil},
			{`"a\"b\\c"=>"x\"y\\z"`, `a"b\c`, `x"y\z`, true, true},
		} {
			s := fixture.Select(q, []string{"f", "o", "e", "d"}, []string{"TEXT", "TEXT", "BOOL", "BOOL"}, [][]any{{tc.value, tc.value, tc.exists, tc.defined}}, tc.h, tc.key)
			hstoreRun(t, p, b, c, s)
			hstoreRun(t, p, b, c, fixture.Prepared(s))
			hstoreRun(t, p, b, held, s)
		}
		arrayQuery := "SELECT slice_array($1::hstore,$2::text[]) AS a,$1::hstore OPERATOR(public.->) $2::text[] AS o"
		arrayStmt, err := c.PrepareContext(p.Ctx, arrayQuery)
		if err != nil {
			t.Fatal(err)
		}
		defer arrayStmt.Close()
		arrayHeld := fixture.HeldStatement{Executor: c, Statement: arrayStmt, SQL: arrayQuery}
		for _, tc := range []struct {
			h, keys any
			result  any
		}{
			{`a=>1,b=>NULL,c=>"NULL"`, `{c,a,missing,b,NULL,a}`, `{"NULL",1,NULL,NULL,NULL,1}`},
			{`aa=>1,c=>3,b=>2,d=>4`, `{{b,d},{aa,c}}`, `{{2,4},{1,3}}`},
			{`a=>1`, `[0:1]={a,missing}`, `[0:1]={1,NULL}`},
			{`a=>1`, `{{{a,NULL}}}`, `{{{1,NULL}}}`},
			{`a=>1`, `{}`, `{}`}, {`a=>1`, `{{NULL,NULL}}`, `{{NULL,NULL}}`},
			{`a=>1`, nil, nil}, {nil, `{a}`, nil},
		} {
			s := fixture.ArrayReferenceSelect(arrayQuery, []string{"a", "o"}, []string{"_TEXT", "_TEXT"}, []string{"_TEXT", "_TEXT"}, [][]any{{tc.result, tc.result}}, b.PG, tc.h, tc.keys)
			hstoreRun(t, p, b, c, s)
			hstoreRun(t, p, b, c, fixture.Prepared(s))
			hstoreRun(t, p, b, arrayHeld, s)
		}
		hstoreRun(t, p, b, c, fixture.Select(`SELECT array_ndims('a=>1'::hstore -> '[0:1]={a,missing}'::text[]) AS r,array_lower('a=>1'::hstore -> '[0:1]={a,missing}'::text[],1) AS l,array_length('a=>1'::hstore -> '[0:1]={a,missing}'::text[],1) AS n,('a=>1'::hstore -> '[0:1]={a,missing}'::text[])[0] AS v`, []string{"r", "l", "n", "v"}, []string{"INT4", "INT4", "INT4", "TEXT"}, [][]any{{int64(1), int64(0), int64(2), "1"}}))
		hstoreRun(t, p, b, c, fixture.Select(`SELECT hstore($$a"b\c$$,$$x"y\z$$) OPERATOR(public.->) $$a"b\c$$ AS v`, []string{"v"}, []string{"TEXT"}, [][]any{{`x"y\z`}}))
	})
}
