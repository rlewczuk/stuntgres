package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceXML2XPathTableRows(t *testing.T) {
	xml2Case(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		xml2Install(t, p, b, c)
		xtSeed(t, p, b, c)
		q := xtQuery("id", "doc", "xt_src", "/doc/a|/doc/b|/doc/@tag", "true", "id integer,a text,b integer,tag text,spare text")
		want := [][]any{
			{int64(1), "10", int64(7), "one", nil},
			{int64(1), "20", nil, nil, nil},
			{int64(2), "", nil, nil, nil},
			{int64(2), "30", nil, nil, nil},
			{int64(4), nil, nil, nil, nil},
			{int64(5), nil, nil, nil, nil},
		}
		s := fixture.Select(q, []string{"id", "a", "b", "tag", "spare"}, []string{"INT4", "TEXT", "INT4", "TEXT", "TEXT"}, want)
		s.Bag = true
		xtBoth(t, p, b, c, "zip_bag", s)
		s.Query += " ORDER BY id,a"
		s.Bag = false
		xtBoth(t, p, b, c, "zip_ordered", s)

		for _, tc := range []struct {
			name, paths, criteria, definitions string
			columns, types                     []string
			rows                               [][]any
		}{
			{"string_repeated", "/doc/a|string(/doc/@tag)", "id=1", "id integer,a text,tag text", []string{"id", "a", "tag"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(1), "10", "one"}, {int64(1), "20", "one"}}},
			{"unmatched", "/missing", "id=1", "id integer,a text", []string{"id", "a"}, []string{"INT4", "TEXT"}, nil},
			{"scalar_only", "string(.)", "id=1", "id integer,a text", []string{"id", "a"}, []string{"INT4", "TEXT"}, nil},
			{"key_only_valid", "[", "id=1", "id integer", []string{"id"}, []string{"INT4"}, nil},
			{"key_only_unusable", "[", "id>=4", "id integer", []string{"id"}, []string{"INT4"}, [][]any{{int64(4)}, {int64(5)}}},
			{"ignored_invalid_tail", "/doc/a|[", "id=1", "id integer,a text", []string{"id", "a"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "10"}, {int64(1), "20"}}},
			{"spare_null", "/doc/b", "id=1", "id integer,b integer,spare text", []string{"id", "b", "spare"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(1), int64(7), nil}}},
			{"invalid_path_unusable_docs", "[", "id>=4", "id integer,a text", []string{"id", "a"}, []string{"INT4", "TEXT"}, [][]any{{int64(4), nil}, {int64(5), nil}}},
			{"invalid_path_empty_source", "[", "false", "id integer,a text", []string{"id", "a"}, []string{"INT4", "TEXT"}, nil},
		} {
			s := fixture.Select(xtQuery("id", "doc", "xt_src", tc.paths, tc.criteria, tc.definitions), tc.columns, tc.types, tc.rows)
			s.Bag = true
			xtBoth(t, p, b, c, tc.name, s)
		}

		xtExec(t, p, b, c, "CREATE TABLE xt_values(id integer,doc text)")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO xt_values VALUES
(1,'<doc><a>10</a></doc>'),(1,'<doc><a>10</a></doc>'),
(NULL,'<doc><a>10</a><a>20</a></doc>'),(NULL,NULL),
(6,'<doc><a><b>x</b>y</a><a/></doc>'),
(7,'<!DOCTYPE doc [<!ENTITY e "<a>entity</a>">]><doc>&e;</doc>')`, 6))
		s = fixture.Select(xtQuery("id", "doc", "xt_values", "/doc/a", "true", "id integer,a text"), []string{"id", "a"}, []string{"INT4", "TEXT"}, [][]any{
			{int64(1), "10"}, {int64(1), "10"}, {nil, "10"}, {nil, "20"}, {nil, nil},
			{int64(6), "xy"}, {int64(6), ""}, {int64(7), "entity"},
		})
		s.Bag = true
		xtBoth(t, p, b, c, "duplicates_null_keys_string_values_entities", s)
	})
}
