package reference_test

import (
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceHstoreJSONValues(t *testing.T) {
	groups := []struct {
		name  string
		cases []struct{ input, strict, loose string }
	}{
		{"representatives", []struct{ input, strict, loose string }{
			{"", `{}`, `{}`}, {"a=>NULL", `{"a": null}`, `{"a": null}`},
			{"a=>null", `{"a": null}`, `{"a": null}`}, {"a=>NuLl", `{"a": null}`, `{"a": null}`},
			{`a=>"NULL"`, `{"a": "NULL"}`, `{"a": "NULL"}`},
			{"a=>012345", `{"a": "012345"}`, `{"a": "012345"}`},
			{"a=>12345", `{"a": "12345"}`, `{"a": 12345}`},
			{"a=>t", `{"a": "t"}`, `{"a": true}`}, {"a=>f", `{"a": "f"}`, `{"a": false}`},
			{`a=>"1"`, `{"a": "1"}`, `{"a": 1}`},
			{`""=>""`, `{"": ""}`, `{"": ""}`},
			{`"é"=>"雪"`, `{"é": "雪"}`, `{"é": "雪"}`},
			{`a=>"<>&/'"`, `{"a": "<>&/'"}`, `{"a": "<>&/'"}`},
			{`"a\"b"=>"c\\d"`, `{"a\"b": "c\\d"}`, `{"a\"b": "c\\d"}`},
			{"a=>\"line\n\t\x01\"", `{"a": "line\n\t\u0001"}`, `{"a": "line\n\t\u0001"}`},
		}},
	}
	for _, number := range []string{"0", "-0", "0.0", "-0.0", "1.00", "1e0", "1E+02", "1e-02", "2.345e+4", "1e999999", "92233720368547758081234567890"} {
		groups[0].cases = append(groups[0].cases, struct{ input, strict, loose string }{"a=>" + number, `{"a": "` + number + `"}`, `{"a": ` + number + `}`})
	}
	stringsGroup := struct {
		name  string
		cases []struct{ input, strict, loose string }
	}{name: "strings"}
	for _, value := range []string{"00", "-01", "+1", ".5", "-.5", "1.", "1e", "1e+", "1e-", "1x", "0x10", "NaN", "Infinity", "-Infinity", "", " 1", "1 ", "١", "true", "false", "T", "F", "TRUE", "yes", "on", "null", "[]", "{}"} {
		want := `{"a": "` + value + `"}`
		stringsGroup.cases = append(stringsGroup.cases, struct{ input, strict, loose string }{`a=>"` + value + `"`, want, want})
	}
	groups = append(groups, stringsGroup)
	for _, group := range groups {
		t.Run(group.name, func(t *testing.T) {
			hstoreScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				hstoreInstall(t, p, b, c)
				for _, tc := range group.cases {
					q := hstoreJSONQuery(hstoreLiteral(tc.input))
					s := hstoreJSONPair(b, q, [][]any{{tc.strict, tc.loose}})
					hstoreRun(t, p, b, c, s)
					hstoreRun(t, p, b, c, fixture.Prepared(s))
				}
				hstoreRun(t, p, b, c, hstoreJSONPair(b, hstoreJSONQuery("NULL::hstore"), [][]any{{nil, nil}}))
			})
		})
	}
	t.Run("members_and_duplicates", func(t *testing.T) {
		hstoreScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			hstoreInstall(t, p, b, c)
			for _, prepared := range []bool{false, true} {
				q := hstoreJSONQuery(`'z=>1.00,a=>NULL,b=>t'`)
				var rows *sql.Rows
				var err error
				if prepared {
					stmt, e := c.PrepareContext(p.Ctx, q)
					if e != nil {
						t.Fatal(e)
					}
					defer stmt.Close()
					rows, err = stmt.QueryContext(p.Ctx)
				} else {
					rows, err = c.QueryContext(p.Ctx, q)
				}
				if err != nil {
					t.Fatal(err)
				}
				if !rows.Next() {
					t.Fatal("missing object row", rows.Err())
				}
				var strict, loose string
				if err := rows.Scan(&strict, &loose); err != nil {
					t.Fatal(err)
				}
				for i, text := range []string{strict, loose} {
					decoder := json.NewDecoder(strings.NewReader(text))
					decoder.UseNumber()
					var object map[string]any
					if err := decoder.Decode(&object); err != nil {
						t.Fatal(err)
					}
					if len(object) != 3 || object["a"] != nil {
						t.Fatalf("object %#v", object)
					}
					if i == 0 && (object["z"] != "1.00" || object["b"] != "t") {
						t.Fatalf("strict %#v", object)
					}
					if i == 1 && (object["z"] != json.Number("1.00") || object["b"] != true) {
						t.Fatalf("loose %#v", object)
					}
				}
				if rows.Next() || rows.Err() != nil {
					t.Fatal("unexpected extra row", rows.Err())
				}
				rows.Close()
			}
			hstoreRun(t, p, b, c, hstoreJSONPair(b, hstoreJSONQuery(`'a=>1,a=>1'`), [][]any{{`{"a": "1"}`, `{"a": 1}`}}))
			var strict, loose string
			if err := c.QueryRowContext(p.Ctx, hstoreJSONQuery(`'a=>1,a=>2'`)).Scan(&strict, &loose); err != nil {
				t.Fatal(err)
			}
			if !((strict == `{"a": "1"}` && loose == `{"a": 1}`) || (strict == `{"a": "2"}` && loose == `{"a": 2}`)) {
				t.Fatalf("duplicate survivor %q/%q", strict, loose)
			}
		})
	})
}

func TestReferenceHstoreJSONBindings(t *testing.T) {
	for _, input := range []string{"$1", "$1::hstore"} {
		t.Run(input, func(t *testing.T) {
			hstoreScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				hstoreInstall(t, p, b, c)
				for _, name := range []string{"hstore_to_json", "hstore_to_json_loose"} {
					q := "SELECT public." + name + "(" + input + ") AS j"
					stmt, err := c.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
					want := `{"a": "1.00"}`
					if name == "hstore_to_json_loose" {
						want = `{"a": 1.00}`
					}
					for _, arg := range []any{"a=>1.00", []byte("a=>1.00"), nil} {
						var value any = want
						if arg == nil {
							value = nil
						}
						s := hstoreJSONSelect(b, q, []string{"j"}, [][]any{{value}}, false, arg)
						hstoreRun(t, p, b, c, s)
						hstoreRun(t, p, b, c, fixture.Prepared(s))
						hstoreRun(t, p, b, held, s)
					}
					hstoreError(t, p, b, held, q, false, true, false, hstoreParameterDiagnostic("42601", `syntax error in hstore, near "b" at position 2`, 1), "a=b")
					hstoreRun(t, p, b, held, hstoreJSONSelect(b, q, []string{"j"}, [][]any{{want}}, false, "a=>1.00"))
				}
			})
		})
	}
}
