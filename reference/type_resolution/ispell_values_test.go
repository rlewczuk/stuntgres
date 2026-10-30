package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// Ordered expectations are independent of native: PostgreSQL REL_18_6
// src/test/regress/expected/tsdicts.out, plus reader/strictness witnesses.
var ispellMorphology = []struct {
	token string
	want  any
}{
	{"book", "{book}"}, {"skies", "{sky}"}, {"SKIES", "{sky}"},
	{"foot", "{foot}"}, {"foots", "{foot}"},
	{"booking", "{booking,book}"}, {"bookings", "{booking,book}"},
	{"rebooking", "{booking,book}"}, {"rebookings", "{booking,book}"},
	{"rebook", nil}, {"unbook", "{book}"}, {"unbooking", "{book}"}, {"unbookings", "{book}"},
	{"zzzzunknown", nil}, {"", nil}, {" book", nil}, {"book foot", nil}, {"book!", nil},
}
var ispellCompound = []struct {
	token string
	want  any
}{
	{"footklubber", "{foot,klubber}"},
	{"footballklubber", "{footballklubber,foot,ball,klubber,football,klubber}"},
	{"ballyklubber", "{ball,klubber}"}, {"footballyklubber", "{foot,ball,klubber}"}, {"bally", nil},
}

func TestReferenceIspellMorphology(t *testing.T) {
	ispellScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		ispellCreate(t, p, b, c, "ispell_morph", "legacy", "")
		q := "SELECT ts_lexize($1::regdictionary,$2::text) AS lexemes"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, tc := range ispellMorphology {
			t.Run(tc.token, func(t *testing.T) {
				ispellToken(t, p, b, c, "ispell_morph", tc.token, tc.want)
				ispellToken(t, p, b, held, "ispell_morph", tc.token, tc.want)
			})
		}
	})
}

func TestReferenceIspellFormats(t *testing.T) {
	for _, format := range []string{"legacy", "default", "long", "num", "reader"} {
		t.Run(format, func(t *testing.T) {
			ispellScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				ispellCreate(t, p, b, c, "ispell_format", format, "")
				if format == "reader" {
					for _, tc := range []struct {
						token string
						want  any
					}{
						{"4", "{4}"}, {"MIXED", "{mixed}"}, {"mixeds", "{mixed}"},
						{"wrong", "{wrong}"}, {"wrongs", nil}, {"roots", "{root}"}, {"plain", "{plain}"}, {"plain ignored", nil},
					} {
						ispellToken(t, p, b, c, "ispell_format", tc.token, tc.want)
					}
					return
				}
				for _, tc := range ispellMorphology {
					ispellToken(t, p, b, c, "ispell_format", tc.token, tc.want)
				}
				for _, tc := range ispellCompound {
					ispellToken(t, p, b, c, "ispell_format", tc.token, tc.want)
				}
				if format == "long" || format == "num" {
					ispellToken(t, p, b, c, "ispell_format", "booked", "{book}")
				}
				if format == "long" {
					ispellToken(t, p, b, c, "ispell_format", "ballsklubber", "{ball,klubber}")
					ispellToken(t, p, b, c, "ispell_format", "ex-machina", "{ex-,machina}")
					ispellToken(t, p, b, c, "ispell_format", "machinaex-", nil)
				}
				if format == "num" {
					ispellToken(t, p, b, c, "ispell_format", "sk", "{sky}")
				}
			})
		})
	}
}

func TestReferenceIspellCompounds(t *testing.T) {
	ispellScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		ispellCreate(t, p, b, c, "ispell_compounds", "legacy", "")
		for _, tc := range ispellCompound {
			ispellToken(t, p, b, c, "ispell_compounds", tc.token, tc.want)
		}
		ispellCreate(t, p, b, c, "ispell_positions", "positions", "")
		for _, tc := range []struct {
			token string
			want  any
		}{
			{"sunset", "{sun,set}"}, {"sunmidset", "{sun,mid,set}"},
			{"setsun", nil}, {"midsunset", nil}, {"sunsetmid", nil},
			{"only", nil}, {"onlyx", nil}, {"onlyxlink", "{only,link}"}, {"onlyqlink", nil},
			{"plainlink", "{plain,link}"},
		} {
			t.Run(tc.token, func(t *testing.T) { ispellToken(t, p, b, c, "ispell_positions", tc.token, tc.want) })
		}
	})
}

func TestReferenceIspellValues(t *testing.T) {
	ispellScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		ispellCreate(t, p, b, c, "ispell_values", "legacy", ",StopWords=tr_ispell_book")
		for _, tc := range []struct {
			token string
			want  any
		}{
			{"book", "{}"}, {"unbook", "{}"}, {"booking", "{booking}"}, {"zzzzunknown", nil}, {"", nil},
		} {
			ispellToken(t, p, b, c, "ispell_values", tc.token, tc.want)
		}
		for _, args := range [][]any{{nil, "book"}, {"ispell_values", nil}, {"ispell_values", []byte(nil)}, {"ispell_values", []byte{}}} {
			dictionaryResult(t, p, b, c, "SELECT ts_lexize($1::regdictionary,$2::text) AS lexemes", nil, args...)
		}
		// A stopword is a normalized component, not the original compound token.
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY ispell_compound_stop (TEMPLATE=ispell,DictFile=tr_ispell_positions,AffFile=tr_ispell_positions,StopWords=tr_ispell_sun)", 0)
		ispellToken(t, p, b, c, "ispell_compound_stop", "sunmidset", "{mid,set}")
	})
}

func TestReferenceIspellBinding(t *testing.T) {
	ispellScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		ispellCreate(t, p, b, c, "ispell_binding", "legacy", "")
		q := "SELECT ts_lexize($1,$2) AS lexemes"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, tc := range []struct {
			args []any
			want any
		}{
			{[]any{"ispell_binding", "rebookings"}, "{booking,book}"},
			{[]any{[]byte("ispell_binding"), []byte("skies")}, "{sky}"},
			{[]any{"ispell_binding", nil}, nil}, {[]any{nil, "book"}, nil},
			{[]any{"ispell_binding", ""}, nil}, {[]any{"ispell_binding", "unbook"}, "{book}"},
		} {
			dictionaryResult(t, p, b, c, q, tc.want, tc.args...)
			dictionaryResult(t, p, b, held, q, tc.want, tc.args...)
		}
		name, token := []byte("ispell_binding"), []byte("rebookings")
		rows, err := stmt.QueryContext(p.Ctx, name, token)
		if err != nil {
			t.Fatal(err)
		}
		name[0], token[0] = 'x', 'x'
		defer rows.Close()
		types, err := rows.ColumnTypes()
		if err != nil || len(types) != 1 || types[0].DatabaseTypeName() != "_TEXT" {
			t.Fatal(types, err)
		}
		if !rows.Next() {
			t.Fatal("owned array missing", rows.Err())
		}
		var value any
		if err := rows.Scan(&value); err != nil || fixture.Normalize(value) != "{booking,book}" {
			t.Fatal(value, err)
		}
		if rows.Next() || rows.Err() != nil {
			t.Fatal(rows.Err())
		}
		rows.Close()
		for _, tc := range []struct{ q, signature string }{
			{"SELECT ts_lexize(true,'book')", "boolean, unknown"},
			{"SELECT ts_lexize('ispell_binding',1)", "unknown, integer"},
			{"SELECT ts_lexize('ispell_binding')", "unknown"},
		} {
			d := dictionaryDiagnostic("42883", "function ts_lexize("+tc.signature+") does not exist")
			d.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
			d.Position = "8"
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic(tc.q, d)
				s.Prepared = prepared
				s.ErrorStage = "execute"
				if prepared {
					s.ErrorStage = "prepare"
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			}
		}
		regtsExec(t, p, b, c, "PREPARE ispell_sql(text) AS SELECT ts_lexize('ispell_binding',$1) AS lexemes", 0)
		dictionaryResult(t, p, b, c, "EXECUTE ispell_sql('rebookings')", "{booking,book}")
		regtsExec(t, p, b, c, "DEALLOCATE ispell_sql", 0)
	})
}
