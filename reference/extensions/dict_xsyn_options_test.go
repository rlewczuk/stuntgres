package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDictXsynBooleanMatrix(t *testing.T) {
	// Literal release-derived truth table: NULL is not a consumed empty group.
	for _, tc := range []struct {
		m, s, k, y        bool
		original, synonym any
	}{
		{false, false, false, false, nil, nil}, {false, false, false, true, nil, nil},
		{false, false, true, false, nil, nil}, {false, false, true, true, nil, nil},
		{false, true, false, false, nil, "{}"}, {false, true, false, true, nil, xsynSynonyms},
		{false, true, true, false, nil, "{supernova}"}, {false, true, true, true, nil, xsynAll},
		{true, false, false, false, "{}", nil}, {true, false, false, true, xsynSynonyms, nil},
		{true, false, true, false, "{supernova}", nil}, {true, false, true, true, xsynAll, nil},
		{true, true, false, false, "{}", "{}"}, {true, true, false, true, xsynSynonyms, xsynSynonyms},
		{true, true, true, false, "{supernova}", "{supernova}"}, {true, true, true, true, xsynAll, xsynAll},
	} {
		t.Run(fmt.Sprintf("%t_%t_%t_%t", tc.m, tc.s, tc.k, tc.y), func(t *testing.T) {
			xsynCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				xsynExec(t, p, b, c, "CREATE EXTENSION dict_xsyn", 0)
				xsynExec(t, p, b, c, fmt.Sprintf("ALTER TEXT SEARCH DICTIONARY xsyn (rules=xsyn_sample,matchorig=%t,matchsynonyms=%t,keeporig=%t,keepsynonyms=%t)", tc.m, tc.s, tc.k, tc.y), 0)
				h := xsynHeld(t, p, c)
				defer h.Statement.Close()
				for _, e := range []fixture.Executor{c, h} {
					xsynLexemes(t, p, b, e, tc.original, "xsyn", "supernova")
					for _, input := range []string{"sn", "sne", "1987a"} {
						xsynLexemes(t, p, b, e, tc.synonym, "xsyn", input)
					}
					for _, input := range []string{"grb", ""} {
						xsynLexemes(t, p, b, e, nil, "xsyn", input)
					}
				}
			})
		})
	}
}

func TestReferenceDictXsynOptions(t *testing.T) {
	xsynCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		xsynExec(t, p, b, c, "CREATE EXTENSION dict_xsyn", 0)
		h := xsynHeld(t, p, c)
		defer h.Statement.Close()
		for _, tc := range []struct {
			options, stored   string
			original, synonym any
		}{
			{"rules=xsyn_sample", "rules = 'xsyn_sample'", xsynAll, nil},
			{"rules=xsyn_sample,matchorig=false,matchorig=true", "rules = 'xsyn_sample', matchorig = 'false', matchorig = 'true'", xsynAll, nil},
			{"rules=xsyn_sample,matchorig=true,matchorig=false", "rules = 'xsyn_sample', matchorig = 'true', matchorig = 'false'", nil, nil},
			{"rules='xsyn_missing',rules=xsyn_sample", "rules = 'xsyn_missing', rules = 'xsyn_sample'", xsynAll, nil},
			{"rules='../bad',rules=xsyn_sample", "rules = '../bad', rules = 'xsyn_sample'", xsynAll, nil},
			{"rules=xsyn_sample,matchorig=0,matchsynonyms=1,keeporig=0,keepsynonyms=1", "rules = 'xsyn_sample', matchorig = 0, matchsynonyms = 1, keeporig = 0, keepsynonyms = 1", nil, xsynSynonyms},
			{"rules=xsyn_sample,matchorig=1,matchsynonyms=0,keeporig=1,keepsynonyms=0", "rules = 'xsyn_sample', matchorig = 1, matchsynonyms = 0, keeporig = 1, keepsynonyms = 0", "{supernova}", nil},
			{"rules=xsyn_sample,matchorig=ON,matchsynonyms=OFF,keeporig='ON',keepsynonyms='OFF'", "rules = 'xsyn_sample', matchorig = 'on', matchsynonyms = 'off', keeporig = 'ON', keepsynonyms = 'OFF'", "{supernova}", nil},
			{"rules=xsyn_sample,matchorig='oFf',matchsynonyms='oN',keeporig=OFF,keepsynonyms=ON", "rules = 'xsyn_sample', matchorig = 'oFf', matchsynonyms = 'oN', keeporig = 'off', keepsynonyms = 'on'", nil, xsynSynonyms},
			{"rules=xsyn_sample,matchorig='TrUe',matchsynonyms='FaLsE',keeporig=true,keepsynonyms=false", "rules = 'xsyn_sample', matchorig = 'TrUe', matchsynonyms = 'FaLsE', keeporig = 'true', keepsynonyms = 'false'", "{supernova}", nil},
			{"rules=xsyn_sample,matchsynonyms=false,matchsynonyms=true,keeporig=true,keeporig=false,keepsynonyms=false,keepsynonyms=true", "rules = 'xsyn_sample', matchsynonyms = 'false', matchsynonyms = 'true', keeporig = 'true', keeporig = 'false', keepsynonyms = 'false', keepsynonyms = 'true'", xsynSynonyms, xsynSynonyms},
		} {
			xsynExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY xsyn_options (TEMPLATE=xsyn_template,"+tc.options+")", 0)
			xsynOptions(t, p, b, c, "xsyn_options", tc.stored)
			for _, e := range []fixture.Executor{c, h} {
				xsynLexemes(t, p, b, e, tc.original, "xsyn_options", "supernova")
				xsynLexemes(t, p, b, e, tc.synonym, "xsyn_options", "sn")
			}
			xsynExec(t, p, b, c, "DROP TEXT SEARCH DICTIONARY xsyn_options", 0)
		}
		xsynExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY xsyn_options (TEMPLATE=xsyn_template,rules=xsyn_sample,matchorig=false,matchsynonyms=true,keeporig=false,keepsynonyms=false)", 0)
		for _, tc := range []struct {
			option                    string
			stored, original, synonym any
		}{
			{"matchorig", "rules = 'xsyn_sample', matchsynonyms = 'true', keeporig = 'false', keepsynonyms = 'false'", "{}", "{}"},
			{"matchsynonyms", "rules = 'xsyn_sample', keeporig = 'false', keepsynonyms = 'false'", "{}", nil},
			{"keeporig", "rules = 'xsyn_sample', keepsynonyms = 'false'", "{supernova}", nil},
			{"keepsynonyms", "rules = 'xsyn_sample'", xsynAll, nil},
			{"rules", nil, nil, nil},
		} {
			xsynExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY xsyn_options ("+tc.option+")", 0)
			xsynOptions(t, p, b, c, "xsyn_options", tc.stored)
			for _, e := range []fixture.Executor{c, h} {
				xsynLexemes(t, p, b, e, tc.original, "xsyn_options", "supernova")
				xsynLexemes(t, p, b, e, tc.synonym, "xsyn_options", "sn")
			}
		}
		xsynExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY xsyn_options (rules=xsyn_sample,matchorig=false,matchorig=true,keeporig=false,keeporig=true)", 0)
		xsynOptions(t, p, b, c, "xsyn_options", "rules = 'xsyn_sample', matchorig = 'true', keeporig = 'true'")
		xsynLexemes(t, p, b, h, xsynAll, "xsyn_options", "supernova")
		const createSQL = "CREATE TEXT SEARCH DICTIONARY xsyn_prepared (TEMPLATE=xsyn_template,rules=xsyn_sample,matchsynonyms=true)"
		create, err := c.PrepareContext(p.Ctx, createSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer create.Close()
		xsynSelect(t, p, b, c, "SELECT dictname FROM pg_ts_dict WHERE dictname='xsyn_prepared' AND dictnamespace='public'::regnamespace", []string{"dictname"}, []string{"NAME"}, nil)
		preparedCreate := fixture.HeldStatement{Executor: c, Statement: create, SQL: createSQL}
		xsynExec(t, p, b, preparedCreate, createSQL, 0)
		xsynOptions(t, p, b, c, "xsyn_prepared", "rules = 'xsyn_sample', matchsynonyms = 'true'")
		xsynLexemes(t, p, b, h, xsynAll, "xsyn_prepared", "sn")
		xsynExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY xsyn_alternate (TEMPLATE=xsyn_template,rules=unaccent)", 0)
		for _, e := range []fixture.Executor{c, h} {
			xsynLexemes(t, p, b, e, "{©,(c)}", "xsyn_alternate", "©")
		}
	})
}
