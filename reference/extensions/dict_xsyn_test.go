package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDictXsynSmoke(t *testing.T) {
	xsynCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		xsynExec(t, p, b, c, "CREATE EXTENSION dict_xsyn", 0)
		h := xsynHeld(t, p, c)
		defer h.Statement.Close()
		check := func(original, synonym any) {
			for _, e := range []fixture.Executor{c, h} {
				xsynLexemes(t, p, b, e, original, "xsyn", "supernova")
				xsynLexemes(t, p, b, e, synonym, "xsyn", "sn")
				xsynLexemes(t, p, b, e, nil, "xsyn", "grb")
			}
		}
		check(nil, nil)
		xsynExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY xsyn (rules=xsyn_sample)", 0)
		check(xsynAll, nil)
		xsynExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY xsyn (matchsynonyms=true)", 0)
		check(xsynAll, xsynAll)
		xsynExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY xsyn (keeporig=false,keepsynonyms=false)", 0)
		check("{}", "{}")
	})
}

func TestReferenceDictXsynValues(t *testing.T) {
	xsynCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		xsynExec(t, p, b, c, "CREATE EXTENSION dict_xsyn", 0)
		h := xsynHeld(t, p, c)
		defer h.Statement.Close()
		for _, e := range []fixture.Executor{c, h} {
			xsynLexemes(t, p, b, e, nil, "xsyn", "supernova")
		}
		xsynExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY xsyn (rules=xsyn_sample,matchsynonyms=true)", 0)
		for _, tc := range []struct{ dictionary, input, want any }{
			{"xsyn", "supernova", xsynAll}, {"xsyn", "sn", xsynAll}, {"xsyn", "sne", xsynAll}, {"xsyn", "1987a", xsynAll},
			{"xsyn", "SUPERNOVA", xsynAll}, {"xsyn", "SN", xsynAll}, {"xsyn", "SNE", xsynAll}, {"xsyn", "1987A", xsynAll},
			{"xsyn", " supernova", nil}, {"xsyn", "supernova ", nil}, {"xsyn", "supernovax", nil}, {"xsyn", "grb", nil}, {"xsyn", "", nil},
			{nil, "supernova", nil}, {"xsyn", nil, nil}, {nil, nil, nil},
			{[]byte("xsyn"), []byte("SUPERNOVA"), xsynAll}, {"xsyn", []byte{}, nil},
		} {
			for _, e := range []fixture.Executor{c, h} {
				xsynLexemes(t, p, b, e, tc.want, tc.dictionary, tc.input)
			}
		}
		// Keep a real rowset across ALTER; the new held execution must acquire
		// new settings while the old row and its metadata retain their value.
		old, err := c.QueryContext(p.Ctx, xsynLexizeSQL, "xsyn", "supernova")
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		ct, err := old.ColumnTypes()
		if err != nil || len(ct) != 1 || ct[0].DatabaseTypeName() != "_TEXT" {
			t.Fatal("old metadata", ct, err)
		}
		// pq streams Rows on c; mutate through an independent borrower rather
		// than issuing another command over its outstanding protocol stream.
		xsynExec(t, p, b, b.DB, "ALTER TEXT SEARCH DICTIONARY xsyn (keeporig=false,keepsynonyms=false)", 0)
		if !old.Next() {
			t.Fatal("old row missing", old.Err())
		}
		var raw any
		if err := old.Scan(&raw); err != nil || fixture.Normalize(raw) != xsynAll {
			t.Fatal("old row", raw, err)
		}
		if old.Next() || old.Err() != nil {
			t.Fatal("old completion", old.Err())
		}
		if err := old.Close(); err != nil {
			t.Fatal(err)
		}
		for _, e := range []fixture.Executor{c, h} {
			xsynLexemes(t, p, b, e, "{}", "xsyn", "supernova")
		}
		xsynExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY xsyn (rules)", 0)
		xsynLexemes(t, p, b, h, nil, "xsyn", "supernova")
	})
}
