package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRegexpSetProjection(t *testing.T) {
	regexpScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, c := range []struct {
			q            string
			names, types []string
			rows         [][]any
			bag          bool
		}{
			{`SELECT id,regexp_matches(s,'(a)','g') AS m FROM (VALUES(1,'aba'),(2,'x'),(3,NULL::text)) v(id,s) ORDER BY id`, []string{"id", "m"}, []string{"INT4", "_TEXT"}, [][]any{{int64(1), "{a}"}, {int64(1), "{a}"}}, false},
			{`SELECT regexp_matches('aa','a','g') AS m,regexp_split_to_table('x','z') AS s`, []string{"m", "s"}, []string{"_TEXT", "TEXT"}, [][]any{{"{a}", "x"}, {"{a}", nil}}, false},
			{`SELECT regexp_matches('x','a') AS m,regexp_split_to_table('x y',' ') AS s`, []string{"m", "s"}, []string{"_TEXT", "TEXT"}, [][]any{{nil, "x"}, {nil, "y"}}, false},
			{`SELECT regexp_matches('x','a') AS m,regexp_matches(NULL::text,'a') AS n`, []string{"m", "n"}, []string{"_TEXT", "_TEXT"}, nil, false},
			{`SELECT upper(regexp_split_to_table('a b',' ')) AS s`, []string{"s"}, []string{"TEXT"}, [][]any{{"A"}, {"B"}}, false},
			{`SELECT regexp_split_to_table(regexp_split_to_table('a b,c d',','),' ') AS s`, []string{"s"}, []string{"TEXT"}, [][]any{{"a"}, {"b"}, {"c"}, {"d"}}, true},
			{`SELECT regexp_matches(regexp_split_to_table('aa bb',' '),'(.)','g') AS m`, []string{"m"}, []string{"_TEXT"}, [][]any{{"{a}"}, {"{a}"}, {"{b}"}, {"{b}"}}, true},
			{`SELECT regexp_count(regexp_split_to_table('aa b',' '),'a') AS n`, []string{"n"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(0)}}, false},
			{`SELECT regexp_matches('aba','a','g') AS m,regexp_matches('xx','x','g') AS n,regexp_split_to_table('1 2 3',' ') AS s`, []string{"m", "n", "s"}, []string{"_TEXT", "_TEXT", "TEXT"}, [][]any{{"{a}", "{x}", "1"}, {"{a}", "{x}", "2"}, {nil, nil, "3"}}, false},
			{`SELECT regexp_split_to_table(s,p) AS s FROM (VALUES('a b',' '),('a','[')) v(s,p) WHERE p=' '`, []string{"s"}, []string{"TEXT"}, [][]any{{"a"}, {"b"}}, false},
			{`SELECT regexp_split_to_table('1 2 3',' ') AS s,random() AS r`, []string{"s", "r"}, []string{"TEXT", "FLOAT8"}, nil, false},
		} {
			if len(c.types) > 1 && c.types[1] == "FLOAT8" {
				// Actual volatile siblings must be reevaluated per output, not broadcast.
				for _, prepared := range []bool{false, true} {
					rows, closeRows, err := fixture.TransactionQuery(p.Ctx, b.DB, c.q, prepared)
					if err != nil {
						t.Fatal(err)
					}
					func() {
						defer closeRows()
						names, err := rows.Columns()
						if err != nil {
							t.Fatal(err)
						}
						if len(names) != 2 || names[0] != "s" || names[1] != "r" {
							t.Fatal(names)
						}
						types, err := rows.ColumnTypes()
						if err != nil {
							t.Fatal(err)
						}
						if types[0].DatabaseTypeName() != "TEXT" || types[1].DatabaseTypeName() != "FLOAT8" {
							t.Fatal(types)
						}
						seen := map[float64]bool{}
						pieces := map[string]bool{}
						for rows.Next() {
							var s string
							var r float64
							if err := rows.Scan(&s, &r); err != nil {
								t.Fatal(err)
							}
							if r < 0 || r >= 1 || seen[r] {
								t.Fatalf("volatile scalar broadcast: %v", r)
							}
							if (s != "1" && s != "2" && s != "3") || pieces[s] {
								t.Fatalf("unexpected split output %q", s)
							}
							seen[r] = true
							pieces[s] = true
						}
						if err := rows.Err(); err != nil {
							t.Fatal(err)
						}
						if len(pieces) != 3 {
							t.Fatal(pieces)
						}
					}()
				}
				continue
			}
			s := regexpRows(b, c.q, c.names, c.types, c.rows)
			s.Bag = s.Bag || c.bag
			regexpRoutes(t, p, b, s)
		}
	})
}

func TestReferenceRegexpSetFrom(t *testing.T) {
	regexpScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, c := range []struct {
			q            string
			names, types []string
			rows         [][]any
		}{
			{`SELECT regexp_matches FROM regexp_matches('aa','a','g')`, []string{"regexp_matches"}, []string{"_TEXT"}, [][]any{{"{a}"}, {"{a}"}}},
			{`SELECT r.* FROM pg_catalog.regexp_split_to_table('a b',' ') AS r(piece)`, []string{"piece"}, []string{"TEXT"}, [][]any{{"a"}, {"b"}}},
			{`SELECT m,o FROM regexp_matches('aa','a','g') WITH ORDINALITY r(m,o)`, []string{"m", "o"}, []string{"_TEXT", "INT8"}, [][]any{{"{a}", int64(1)}, {"{a}", int64(2)}}},
			{`SELECT r.* FROM regexp_split_to_table('é🙂x','') WITH ORDINALITY r(s,o)`, []string{"s", "o"}, []string{"TEXT", "INT8"}, [][]any{{"é", int64(1)}, {"🙂", int64(2)}, {"x", int64(3)}}},
			{`SELECT r.* FROM ROWS FROM(regexp_matches('aa','a','g'),regexp_split_to_table('x','z')) r(m,s)`, []string{"m", "s"}, []string{"_TEXT", "TEXT"}, [][]any{{"{a}", "x"}, {"{a}", nil}}},
			{`SELECT r.* FROM ROWS FROM(regexp_matches('x','a'),regexp_split_to_table('x y',' ')) WITH ORDINALITY r(m,s,o)`, []string{"m", "s", "o"}, []string{"_TEXT", "TEXT", "INT8"}, [][]any{{nil, "x", int64(1)}, {nil, "y", int64(2)}}},
			{`SELECT r.* FROM ROWS FROM(regexp_matches('x','a'),regexp_matches(NULL::text,'a')) r(m,n)`, []string{"m", "n"}, []string{"_TEXT", "_TEXT"}, nil},
			{`SELECT m,s FROM regexp_matches('aa','a','g') r(m),regexp_split_to_table('x y',' ') t(s) ORDER BY s`, []string{"m", "s"}, []string{"_TEXT", "TEXT"}, [][]any{{"{a}", "x"}, {"{a}", "x"}, {"{a}", "y"}, {"{a}", "y"}}},
			{`SELECT r.* FROM regexp_count(NULL::text,'a') r(n)`, []string{"n"}, []string{"INT4"}, [][]any{{nil}}},
			{`SELECT r.* FROM regexp_like('a','a') r(m)`, []string{"m"}, []string{"BOOL"}, [][]any{{true}}},
			{`SELECT r.* FROM regexp_match('x','z') r(m)`, []string{"m"}, []string{"_TEXT"}, [][]any{{nil}}},
			{`SELECT m FROM regexp_matches(pattern=>'a',string=>'aa',flags=>'g') r(m)`, []string{"m"}, []string{"_TEXT"}, [][]any{{"{a}"}, {"{a}"}}},
			{`SELECT s FROM regexp_split_to_table(pattern=>' ',string=>'a b') r(s)`, []string{"s"}, []string{"TEXT"}, [][]any{{"a"}, {"b"}}},
		} {
			regexpRoutes(t, p, b, regexpRows(b, c.q, c.names, c.types, c.rows))
		}
		for _, c := range []struct{ q, code, msg, token string }{
			{`SELECT * FROM regexp_matches('a','a') r(a,b)`, "42P10", `table "r" has 1 columns available but 2 columns specified`, "r(a,b)"},
			{`SELECT * FROM regexp_split_to_table(regexp_split_to_table('a b,c d',','),' ')`, "0A000", `set-returning functions must appear at top level of FROM`, "regexp_split_to_table('a b"},
		} {
			s := regexpBindError(c.q, c.code, c.msg, "", c.token)
			if c.code == "42P10" {
				s.Diagnostic.Position = ""
			}
			regexpRoutes(t, p, b, s)
		}
	})
}

func TestReferenceRegexpSetCorrelation(t *testing.T) {
	regexpScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		base := `(VALUES(1,'aba'),(2,'x'),(3,NULL::text)) v(id,s)`
		for _, join := range []string{`,regexp_matches(v.s,'(a)','g') r(m)`, ` CROSS JOIN regexp_matches(v.s,'(a)','g') r(m)`, ` INNER JOIN LATERAL regexp_matches(v.s,'(a)','g') r(m) ON true`} {
			regexpRoutes(t, p, b, regexpRows(b, `SELECT id,r.m FROM `+base+join+` ORDER BY id`, []string{"id", "m"}, []string{"INT4", "_TEXT"}, [][]any{{int64(1), "{a}"}, {int64(1), "{a}"}}))
		}
		for _, c := range []struct {
			on   string
			rows [][]any
		}{
			{"true", [][]any{{int64(1), "{a}"}, {int64(1), "{a}"}, {int64(2), nil}, {int64(3), nil}}},
			{"false", [][]any{{int64(1), nil}, {int64(2), nil}, {int64(3), nil}}},
			{"v.id=1", [][]any{{int64(1), "{a}"}, {int64(1), "{a}"}, {int64(2), nil}, {int64(3), nil}}},
		} {
			s := regexpRows(b, `SELECT id,r.m FROM `+base+` LEFT JOIN LATERAL regexp_matches(v.s,'(a)','g') r(m) ON `+c.on+` ORDER BY id`, []string{"id", "m"}, []string{"INT4", "_TEXT"}, c.rows)
			s.CheckMetadata = regexpMetadataNullable(b.PG, []string{"m"})
			regexpRoutes(t, p, b, s)
		}
		for i, c := range []struct {
			q            string
			names, types []string
			rows         [][]any
		}{
			{`SELECT id,(SELECT regexp_matches(s,'a')) AS m FROM (VALUES(1,'a'),(2,'x')) v(id,s) ORDER BY id`, []string{"id", "m"}, []string{"INT4", "_TEXT"}, [][]any{{int64(1), "{a}"}, {int64(2), nil}}},
			{`SELECT id,(SELECT count(*) FROM regexp_matches(v.s,'a','g') r(m)) AS n FROM (VALUES(1,'aa'),(2,'x')) v(id,s) ORDER BY id`, []string{"id", "n"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(2)}, {int64(2), int64(0)}}},
			{`SELECT v.id,r.s FROM (VALUES(1,'a b'),(2,'c')) v(id,s),regexp_split_to_table(v.s,' ') r(s) ORDER BY v.id,r.s`, []string{"id", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}, {int64(1), "b"}, {int64(2), "c"}}},
			{`SELECT v.id,r.s FROM (VALUES(1),(2)) v(id) RIGHT JOIN regexp_split_to_table('a b',' ') r(s) ON v.id=1 ORDER BY r.s`, []string{"id", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}, {int64(1), "b"}}},
			{`SELECT v.id,r.s FROM (VALUES(1)) v(id) FULL JOIN regexp_split_to_table('a b',' ') r(s) ON false ORDER BY v.id,r.s`, []string{"id", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), nil}, {nil, "a"}, {nil, "b"}}},
		} {
			s := regexpRows(b, c.q, c.names, c.types, c.rows)
			if i == 3 {
				s.CheckMetadata = regexpMetadataNullable(b.PG, []string{"id"})
			} else if i == 4 {
				s.CheckMetadata = regexpMetadataNullable(b.PG, []string{"id", "s"})
			}
			regexpRoutes(t, p, b, s)
		}
		regexpRoutes(t, p, b, regexpRuntimeError(`SELECT (SELECT regexp_matches('aa','a','g'))`, "21000", "more than one row returned by a subquery used as an expression", ""))
		for _, join := range []string{"RIGHT", "FULL"} {
			q := `SELECT * FROM (VALUES('a')) v(s) ` + join + ` JOIN LATERAL regexp_matches(v.s,'a') r(m) ON true`
			s := regexpBindError(q, "42P10", `invalid reference to FROM-clause entry for table "v"`, "", "v.s")
			s.Diagnostic.Detail = "The combining JOIN type must be INNER or LEFT for a LATERAL reference."
			regexpRoutes(t, p, b, s)
		}
		q := `SELECT * FROM regexp_matches(v.s,'a') r(m),(VALUES('a')) v(s)`
		regexpRoutes(t, p, b, regexpBindError(q, "42P01", `missing FROM-clause entry for table "v"`, "", "v.s"))
	})
}
