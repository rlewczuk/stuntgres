package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

type hstoreValueCase struct{ name, input, output string }

func hstoreValueCases() []hstoreValueCase {
	return []hstoreValueCase{
		{"empty", "", ""},
		{"ascii_space", " \t\n\r\v\f", ""},
		{"simple", "a=>1", `"a"=>"1"`},
		{"sort", "aa=>long,b=>short,a=>NULL", `"a"=>NULL, "b"=>"short", "aa"=>"long"`},
		{"null_lower", "a=>null", `"a"=>NULL`},
		{"null_mixed", "a=>NuLl", `"a"=>NULL`},
		{"quoted_null", `a=>"NULL"`, `"a"=>"NULL"`},
		{"null_key", "NULL=>x", `"NULL"=>"x"`},
		{"empty_pair", `""=>""`, `""=>""`},
		{"trailing_comma", "a=>b,", `"a"=>"b"`},
		{"trailing_space", "a=>b, \t\n", `"a"=>"b"`},
		{"equals_value", "a=>q=w", `"a"=>"q=w"`},
		{"greater_value", "a=>q>w", `"a"=>"q>w"`},
		{"ordinary_escape", `a=>b\qc`, `"a"=>"bqc"`},
		{"unquoted_comma_key", "a,b=>c", `"a,b"=>"c"`},
		{"initial_comma_value", "a=>,b", `"a"=>",b"`},
		{"unquoted_quote", `a=>b"c`, `"a"=>"b\"c"`},
		{"escaped_null", `N\ULL=>N\ULL`, `"NULL"=>NULL`},
		{"quoted_escaped_null", `a=>"N\ULL"`, `"a"=>"NULL"`},
		{"quotes_slashes", `"a\"b"=>"c\\d"`, `"a\"b"=>"c\\d"`},
		{"delimiters", `"a,b"=>"x=>y"`, `"a,b"=>"x=>y"`},
		{"whitespace", "\"a b\"=>\"x\ny\"", "\"a b\"=>\"x\ny\""},
		{"apostrophe", `a=>O'Reilly`, `"a"=>"O'Reilly"`},
		{"unicode_sort", "é=>雪,aa=>z,b=>β", `"b"=>"β", "aa"=>"z", "é"=>"雪"`},
		{"nbsp_key", "\u00a0a=>b", "\"\u00a0a\"=>\"b\""},
		{"nbsp_value", "a=>\u00a0b", "\"a\"=>\"\u00a0b\""},
		{"same_duplicate", "a=>1,a=>1", `"a"=>"1"`},
	}
}

func TestReferenceHstoreValues(t *testing.T) {
	for _, tc := range hstoreValueCases() {
		t.Run(tc.name, func(t *testing.T) {
			hstoreScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				hstoreInstall(t, p, b, c)
				hstoreRun(t, p, b, c, fixture.Exec("CREATE TABLE hs_values(id integer NOT NULL,v hstore)", 0))
				for _, prepared := range []bool{false, true} {
					s := fixture.Exec("INSERT INTO hs_values VALUES (1,"+hstoreLiteral(tc.input)+")", 1)
					s.Prepared = prepared
					hstoreRun(t, p, b, c, s)
					hstoreRun(t, p, b, c, hstoreSelect(b, "SELECT v FROM hs_values ORDER BY id", []string{"v"}, [][]any{{tc.output}}, []bool{true}))
					text := fixture.Select("SELECT ("+hstoreLiteral(tc.input)+"::hstore)::text AS v", []string{"v"}, []string{"TEXT"}, [][]any{{tc.output}})
					text.Prepared = prepared
					hstoreRun(t, p, b, c, text)
					for _, q := range []string{"SELECT " + hstoreLiteral(tc.input) + "::hstore AS v", "SELECT CAST(" + hstoreLiteral(tc.input) + " AS hstore) AS v", "SELECT CAST(CAST(" + hstoreLiteral(tc.input) + " AS text) AS hstore) AS v"} {
						s := hstoreSelect(b, q, []string{"v"}, [][]any{{tc.output}}, nil)
						s.Prepared = prepared
						hstoreRun(t, p, b, c, s)
					}
					hstoreRun(t, p, b, c, fixture.Exec("DELETE FROM hs_values", 1))
				}
			})
		})
	}
	t.Run("conflicting_duplicate", func(t *testing.T) {
		hstoreScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			hstoreInstall(t, p, b, c)
			for _, prepared := range []bool{false, true} {
				q := "SELECT $1::hstore"
				var value string
				if prepared {
					s, err := c.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					err = s.QueryRowContext(p.Ctx, "a=>1,a=>2").Scan(&value)
					s.Close()
					if err != nil {
						t.Fatal(err)
					}
				} else if err := c.QueryRowContext(p.Ctx, q, "a=>1,a=>2").Scan(&value); err != nil {
					t.Fatal(err)
				}
				if value != `"a"=>"1"` && value != `"a"=>"2"` {
					t.Fatalf("duplicate must retain exactly one original pair: %q", value)
				}
			}
		})
	})
}

func TestReferenceHstoreBindings(t *testing.T) {
	for _, route := range []string{"direct", "fresh", "held"} {
		t.Run(route, func(t *testing.T) {
			hstoreScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				hstoreInstall(t, p, b, c)
				hstoreRun(t, p, b, c, fixture.Exec("CREATE TABLE hs_bound(id integer NOT NULL,v hstore)", 0))
				q := "INSERT INTO hs_bound VALUES ($1,$2)"
				var db fixture.Executor = c
				if route == "held" {
					s, err := c.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer s.Close()
					db = fixture.HeldStatement{Executor: c, Statement: s, SQL: q}
				}
				var want [][]any
				id := 0
				insert := func(input any, output any) {
					id++
					s := fixture.Exec(q, 1, id, input)
					s.Prepared = route == "fresh"
					hstoreRun(t, p, b, db, s)
					want = append(want, []any{int64(id), output})
				}
				for _, tc := range hstoreValueCases() {
					insert(tc.input, tc.output)
				}
				insert(nil, nil)
				insert([]byte(nil), nil)
				insert([]byte{}, "")
				buffer := []byte("a=>original")
				insert(buffer, `"a"=>"original"`)
				copy(buffer, []byte("a=>mutated!"))
				wantError := hstoreParameterDiagnostic("42601", `syntax error in hstore, near "b" at position 2`, 2)
				hstoreError(t, p, b, db, q, route == "fresh", false, false, wantError, id+1, "a=b")
				insert("z=>after", `"z"=>"after"`)
				check := hstoreSelect(b, "SELECT id,v FROM hs_bound ORDER BY id", []string{"id", "v"}, want, []bool{false, true})
				hstoreRun(t, p, b, c, check)
				hstoreRun(t, p, b, c, fixture.Prepared(check))
				reader, err := c.PrepareContext(p.Ctx, check.Query)
				if err != nil {
					t.Fatal(err)
				}
				hstoreRun(t, p, b, fixture.HeldStatement{Executor: c, Statement: reader, SQL: check.Query}, check)
				reader.Close()
				for _, tc := range []struct {
					input  any
					output any
				}{{"a=>1", `"a"=>"1"`}, {"", ""}, {nil, nil}, {[]byte(nil), nil}} {
					t.Run(fmt.Sprintf("cast_%T_%v", tc.input, tc.input), func(t *testing.T) {
						s := hstoreSelect(b, "SELECT $1::hstore AS v", []string{"v"}, [][]any{{tc.output}}, nil, tc.input)
						s.Prepared = route != "direct"
						hstoreRun(t, p, b, c, s)
					})
				}
			})
		})
	}
}
