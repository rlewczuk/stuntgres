package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func jsonTypeKeys(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, family := range []string{"json", "jsonb"} {
		jsonTypeBoth(t, p, b, c, jsonTypeKeySelect(family, jsonTypeOrderedObject, jsonTypeKeyRows(family)))
		for _, input := range []any{`{}`, nil} {
			jsonTypeBoth(t, p, b, c, jsonTypeKeySelect(family, input, [][]any{}))
		}
		dup := [][]any{{"a", int64(1)}, {"a", int64(2)}}
		if family == "jsonb" {
			dup = [][]any{{"a", int64(1)}}
		}
		jsonTypeBoth(t, p, b, c, jsonTypeKeySelect(family, `{"a":1,"\u0061":2}`, dup))
		long := strings.Repeat("k", 80)
		input := `{"":0,"` + long + `":1,"outer":{"hidden":2},"arr":[{"also-hidden":3}]}`
		want := [][]any{{"", int64(1)}, {long, int64(2)}, {"outer", int64(3)}, {"arr", int64(4)}}
		if family == "jsonb" {
			want = [][]any{{"", int64(1)}, {"arr", int64(2)}, {"outer", int64(3)}, {long, int64(4)}}
		}
		jsonTypeBoth(t, p, b, c, jsonTypeKeySelect(family, input, want))
		// ORDER BY decoded text witnesses exact values without asserting container order.
		s := fixture.Select("SELECT k FROM "+family+"_object_keys($1) AS x(k)", []string{"k"}, []string{"TEXT"}, [][]any{{"\""}, {"\\"}, {"/"}, {"\n"}, {"\t"}, {"😀"}}, `{"\"":0,"\\":1,"\/":2,"\n":3,"\t":4,"\uD83D\uDE00":5}`)
		s.Bag = true
		jsonTypeBoth(t, p, b, c, s)
		// é has two UTF8 bytes while aaa has three; canonical order is not rune length.
		want = [][]any{{"aaa", int64(1)}, {"é", int64(2)}, {"z", int64(3)}}
		if family == "jsonb" {
			want = [][]any{{"z", int64(1)}, {"é", int64(2)}, {"aaa", int64(3)}}
		}
		jsonTypeBoth(t, p, b, c, jsonTypeKeySelect(family, `{"aaa":0,"é":1,"z":2}`, want))
	}
	jsonTypeBoth(t, p, b, c, jsonTypeKeySelect("json", `{"x":1e131072}`, [][]any{{"x", int64(1)}}))
	jsonTypeRun(t, p, b, c, fixture.Exec("CREATE TABLE jt_source(j json)", 0))
	source := " { \"b\" : 1, \"a\" : 2, \"b\" : 3 } "
	jsonTypeRun(t, p, b, c, fixture.Exec("INSERT INTO jt_source VALUES($1)", 1, source))
	jsonTypeBoth(t, p, b, c, fixture.Select("SELECT j::text AS source, json_typeof(j) AS kind FROM jt_source", []string{"source", "kind"}, []string{"TEXT", "TEXT"}, [][]any{{source, "object"}}))
	jsonTypeBoth(t, p, b, c, fixture.Select("SELECT j::text AS source,k,n FROM jt_source, json_object_keys(j) WITH ORDINALITY AS x(k,n) ORDER BY n", []string{"source", "k", "n"}, []string{"TEXT", "TEXT", "INT8"}, [][]any{{source, "b", int64(1)}, {source, "a", int64(2)}, {source, "b", int64(3)}}))
}

func TestReferenceJSONTypeKeys(t *testing.T) { jsonTypeScenario(t, jsonTypeKeys) }

func TestReferenceJSONBKeysDecodedOrdering(t *testing.T) {
	jsonTypeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		input := `{"\uD83D\uDE00":null,"\u00e9":0,"aaa":1,"\u0061":2,"a":3,"":4,"\n":5,"\"":6,"\\":7,"outer":{"hidden":8}}`
		// Canonical order uses decoded UTF8 byte length, then binary bytes.
		// Escaped and literal a collapse; nested hidden is not a top-level key.
		want := [][]any{
			{"", int64(1)},
			{"\n", int64(2)},
			{"\"", int64(3)},
			{"\\", int64(4)},
			{"a", int64(5)},
			{"é", int64(6)},
			{"aaa", int64(7)},
			{"😀", int64(8)},
			{"outer", int64(9)},
		}
		step := jsonTypeKeySelect("jsonb", input, want)
		jsonTypeBoth(t, p, b, c, fixture.JSONTypeWithMetadata(step, b.PG))
	})
}
