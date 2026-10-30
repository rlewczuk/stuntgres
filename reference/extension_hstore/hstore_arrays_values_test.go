package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func hstoreArraysSelect(b fixture.Backend, q string, rows [][]any, args ...any) fixture.Step {
	return fixture.ArrayReferenceSelect(q, []string{"a", "m", "u", "w"}, []string{"_TEXT", "_TEXT", "_TEXT", "_TEXT"}, []string{"_TEXT", "_TEXT", "_TEXT", "_TEXT"}, rows, b.PG, args...)
}
func hstoreArraysQuery(input string) string {
	return "SELECT hstore_to_array(" + input + ") AS a,hstore_to_matrix(" + input + ") AS m,%% " + input + " AS u,%# " + input + " AS w"
}
func TestReferenceHstoreArraysValues(t *testing.T) {
	hstoreScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		hstoreInstall(t, p, b, c)
		cases := []struct {
			input        string
			flat, matrix any
		}{
			{"a=>1", "{a,1}", "{{a,1}}"}, {"a=>NULL", "{a,NULL}", "{{a,NULL}}"}, {`a=>"NULL"`, `{a,"NULL"}`, `{{a,"NULL"}}`},
			{`""=>""`, `{"",""}`, `{{"",""}}`}, {"", "{}", "{}"}, {"aa=>1,cq=>l,b=>g,fg=>NULL", "{b,g,aa,1,cq,l,fg,NULL}", "{{b,g},{aa,1},{cq,l},{fg,NULL}}"},
			{"é=>雪,aa=>z,b=>β", "{b,β,aa,z,é,雪}", "{{b,β},{aa,z},{é,雪}}"}, {"a=>1,a=>1", "{a,1}", "{{a,1}}"},
			{`"a,b"=>"{x}"`, `{"a,b","{x}"}`, `{{"a,b","{x}"}}`}, {`"a b"=>"c d"`, `{"a b","c d"}`, `{{"a b","c d"}}`}, {"a=>'", "{a,'}", "{{a,'}}"},
			{`"a\"b"=>"c\\d"`, `{"a\"b","c\\d"}`, `{{"a\"b","c\\d"}}`},
		}
		q := hstoreArraysQuery("$1::hstore")
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, tc := range cases {
			want := [][]any{{tc.flat, tc.matrix, tc.flat, tc.matrix}}
			for _, prepared := range []bool{false, true} {
				s := hstoreArraysSelect(b, hstoreArraysQuery(hstoreLiteral(tc.input)+"::hstore"), want)
				s.Prepared = prepared
				hstoreRun(t, p, b, c, s)
				s = hstoreArraysSelect(b, q, want, tc.input)
				s.Prepared = prepared
				hstoreRun(t, p, b, c, s)
			}
			hstoreRun(t, p, b, held, hstoreArraysSelect(b, q, want, []byte(tc.input)))
		}
		hstoreRun(t, p, b, held, hstoreArraysSelect(b, q, [][]any{{nil, nil, nil, nil}}, nil))
		hstoreRun(t, p, b, c, hstoreArraysSelect(b, hstoreArraysQuery("NULL::hstore"), [][]any{{nil, nil, nil, nil}}))
		hstoreRun(t, p, b, c, hstoreArraysSelect(b, hstoreArraysQuery("'a=>1'"), [][]any{{"{a,1}", "{{a,1}}", "{a,1}", "{{a,1}}"}}))
		hstoreArraysRelational(t, p, b, c)
		hstoreRun(t, p, b, c, fixture.Select("SELECT array_lower(hstore_to_array('a=>1'),1) AS a,array_lower(hstore_to_matrix('a=>1'),1) AS b,array_lower(hstore_to_matrix('a=>1'),2) AS c,array_length(hstore_to_array('aa=>1,cq=>l,b=>g,fg=>NULL'),1) AS d,array_length(hstore_to_matrix('aa=>1,cq=>l,b=>g,fg=>NULL'),1) AS e", []string{"a", "b", "c", "d", "e"}, []string{"INT4", "INT4", "INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(1), int64(1), int64(8), int64(4)}}))
		hstoreRun(t, p, b, c, hstoreArraysSelect(b, "SELECT public.hstore_to_array('a=>1') AS a,public.hstore_to_matrix('a=>1') AS m,OPERATOR(public.%%) 'a=>1'::hstore AS u,OPERATOR(public.%#) 'a=>1'::hstore AS w", [][]any{{"{a,1}", "{{a,1}}", "{a,1}", "{{a,1}}"}}))
	})
}
func hstoreArraysRelational(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	hstoreRun(t, p, b, c, fixture.Exec("CREATE TABLE hs_arrays(id integer,v hstore)", 0))
	hstoreRun(t, p, b, c, fixture.Exec(`INSERT INTO hs_arrays VALUES (1,'a=>1'),(2,''),(3,NULL),(4,'a=>NULL'),(5,'a=>"NULL"')`, 5))
	for _, predicate := range []string{"hstore_to_array(v)=ARRAY['a','1']", "%% v=ARRAY['a','1']", "hstore_to_matrix(v)=ARRAY[['a','1']]", "%# v=ARRAY[['a','1']]"} {
		s := fixture.Select("SELECT id FROM hs_arrays WHERE "+predicate+" ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}})
		hstoreRun(t, p, b, c, s)
		hstoreRun(t, p, b, c, fixture.Prepared(s))
	}
	for _, f := range []string{"hstore_to_array(v)", "hstore_to_matrix(v)", "%% v", "%# v"} {
		hstoreRun(t, p, b, c, fixture.Select("SELECT id FROM hs_arrays WHERE ("+f+") IS NULL ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(3)}}))
	}
	hstoreRun(t, p, b, c, fixture.Select("SELECT id,(hstore_to_array(v))[2] AS x,(hstore_to_matrix(v))[1][2] AS y FROM hs_arrays ORDER BY id", []string{"id", "x", "y"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(1), "1", "1"}, {int64(2), nil, nil}, {int64(3), nil, nil}, {int64(4), nil, nil}, {int64(5), "NULL", "NULL"}}))
	hstoreRun(t, p, b, c, fixture.Select("SELECT id,array_ndims(hstore_to_array(v)) AS r,array_length(hstore_to_array(v),1) AS n,array_ndims(hstore_to_matrix(v)) AS s,array_length(hstore_to_matrix(v),1) AS k,array_length(hstore_to_matrix(v),2) AS l,cardinality(hstore_to_matrix(v)) AS z FROM hs_arrays ORDER BY id", []string{"id", "r", "n", "s", "k", "l", "z"}, []string{"INT4", "INT4", "INT4", "INT4", "INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(1), int64(2), int64(2), int64(1), int64(2), int64(2)}, {int64(2), nil, nil, nil, nil, nil, int64(0)}, {int64(3), nil, nil, nil, nil, nil, nil}, {int64(4), int64(1), int64(2), int64(2), int64(1), int64(2), int64(2)}, {int64(5), int64(1), int64(2), int64(2), int64(1), int64(2), int64(2)}}))
}
