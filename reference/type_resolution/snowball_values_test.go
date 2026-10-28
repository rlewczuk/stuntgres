package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// Expectations are derived from the English algorithm shipped in REL_18_6,
// not from current Snowball rules or the native implementation.
func TestReferenceSnowballMorphology(t *testing.T) {
	groups := [][][2]string{
		{{"rats", "rat"}, {"RATS", "rat"}, {"running", "run"}, {"skies", "sky"}, {"ties", "tie"}, {"cries", "cri"}, {"dying", "die"}, {"news", "news"}, {"inning", "inning"}, {"innings", "inning"}, {"added", "add"}, {"hopping", "hop"}, {"filing", "file"}, {"relational", "relat"}, {"conditional", "condit"}, {"Paris", "pari"}, {"evening", "even"}, {"geologist", "geologist"}},
		{{"skis", "ski"}, {"lying", "lie"}, {"tying", "tie"}, {"idly", "idl"}, {"gently", "gentl"}, {"ugly", "ugli"}, {"early", "earli"}, {"only", "onli"}, {"singly", "singl"}, {"sky", "sky"}, {"howe", "howe"}, {"atlas", "atlas"}, {"cosmos", "cosmos"}, {"bias", "bias"}, {"andes", "andes"}, {"outings", "outing"}, {"cannings", "canning"}, {"herrings", "herring"}, {"earrings", "earring"}, {"proceed", "proceed"}, {"exceed", "exceed"}, {"succeed", "succeed"}},
		{{"caresses", "caress"}, {"ponies", "poni"}, {"gas", "gas"}, {"gaps", "gap"}, {"agreed", "agre"}, {"plastered", "plaster"}, {"motoring", "motor"}, {"sing", "sing"}, {"conflated", "conflat"}, {"troubled", "troubl"}, {"sized", "size"}, {"happy", "happi"}, {"boy", "boy"}, {"triplicate", "triplic"}, {"formative", "format"}, {"formalize", "formal"}, {"electriciti", "electr"}, {"electrical", "electr"}, {"hopeful", "hope"}, {"goodness", "good"}, {"revival", "reviv"}, {"probate", "probat"}, {"rate", "rate"}, {"cease", "ceas"}, {"controll", "control"}, {"roll", "roll"}},
		{{"rational", "ration"}, {"valenci", "valenc"}, {"hesitanci", "hesit"}, {"digitizer", "digit"}, {"conformabli", "conform"}, {"radicalli", "radic"}, {"differentli", "differ"}, {"vileli", "vile"}, {"analogousli", "analog"}, {"vietnamization", "vietnam"}, {"predication", "predic"}, {"operator", "oper"}, {"feudalism", "feudal"}, {"decisiveness", "decis"}, {"hopefulness", "hope"}, {"callousness", "callous"}, {"formaliti", "formal"}, {"sensitiviti", "sensit"}, {"sensibiliti", "sensibl"}, {"logi", "logi"}, {"analogi", "analog"}, {"vabli", "vabli"}},
		{{"allowance", "allow"}, {"inference", "infer"}, {"airliner", "airlin"}, {"gyroscopic", "gyroscop"}, {"adjustable", "adjust"}, {"defensible", "defens"}, {"irritant", "irrit"}, {"replacement", "replac"}, {"adjustment", "adjust"}, {"dependent", "depend"}, {"adoption", "adopt"}, {"homologou", "homologou"}, {"communism", "communism"}, {"activate", "activ"}, {"angulariti", "angular"}, {"homologous", "homolog"}, {"effective", "effect"}, {"bowdlerize", "bowdler"}, {"youth", "youth"}, {"enjoy", "enjoy"}, {"cry", "cri"}, {"by", "by"}, {"y", "y"}, {"a", "a"}, {"abb", "abb"}, {"ebbed", "ebb"}, {"owned", "own"}, {"generously", "generous"}, {"arsenal", "arsenal"}},
	}
	for i, group := range groups {
		t.Run(string(rune('A'+i)), func(t *testing.T) {
			dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY snowball_rules (TEMPLATE=pg_catalog.snowball,Language=english)", 0)
				q := "SELECT ts_lexize('snowball_rules'::regdictionary,$1::text) AS lexemes"
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				for _, tc := range group {
					dictionaryResult(t, p, b, c, q, "{"+tc[1]+"}", tc[0])
					dictionaryResult(t, p, b, held, q, "{"+tc[1]+"}", tc[0])
				}
			})
		})
	}
}

func TestReferenceSnowballStopwords(t *testing.T) {
	// The released list has 127 entries. Keep this independent of embedded data.
	words := strings.Fields("i me my myself we our ours ourselves you your yours yourself yourselves he him his himself she her hers herself it its itself they them their theirs themselves what which who whom this that these those am is are was were be been being have has had having do does did doing a an the and but if or because as until while of at by for with about against between into through during before after above below to from up down in out on off over under again further then once here there when where why how all any both each few more most other some such no nor not only own same so than too very s t can will just don should now")
	if len(words) != 127 {
		t.Fatal("independent stop list length", len(words))
	}
	for start := 0; start < len(words); start += 24 {
		end := min(start+24, len(words))
		t.Run(words[start], func(t *testing.T) {
			dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				q := "SELECT ts_lexize('english_stem',$1::text) AS lexemes"
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				for _, word := range words[start:end] {
					dictionaryResult(t, p, b, c, q, "{}", word)
					dictionaryResult(t, p, b, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, q, "{}", strings.ToUpper(word))
				}
			})
		})
	}
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY snowball_no_stop (TEMPLATE=snowball,Language=english)", 0)
		for _, tc := range [][2]string{{"ONLY", "onli"}, {"being", "be"}, {"does", "doe"}, {"The", "the"}, {"DON", "don"}, {"S", "s"}, {"T", "t"}} {
			for _, prepared := range []bool{false, true} {
				s := fixture.Select("SELECT ts_lexize('snowball_no_stop',$1::text) AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{"{" + tc[1] + "}"}}, tc[0])
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			}
		}
		dictionaryResult(t, p, b, c, "SELECT ts_lexize('english_stem','') AS lexemes", "{}")
		dictionaryResult(t, p, b, c, "SELECT ts_lexize('english_stem',NULL) AS lexemes", nil)
		dictionaryResult(t, p, b, c, "SELECT ts_lexize(NULL,'RATS') AS lexemes", nil)
	})
}

func TestReferenceSnowballBoundaries(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		q := "SELECT ts_lexize('english_stem',$1::text) AS lexemes"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, tc := range [][2]string{{"café", "café"}, {"cafés", "café"}, {"CAFÉS", "cafÉ"}, {"hôtels", "hôtel"}, {"ès", "ès"}, {"children's", "children"}, {"children’s", "children’"}, {"'skies", "ski"}, {"cats!", "cats!"}, {strings.Repeat("a", 997) + "ies", strings.Repeat("a", 997) + "i"}, {strings.Repeat("A", 998) + "ies", strings.Repeat("a", 998) + "ies"}, {strings.Repeat("é", 499) + "ies", strings.Repeat("é", 499) + "ies"}} {
			dictionaryResult(t, p, b, c, q, "{"+tc[1]+"}", tc[0])
			dictionaryResult(t, p, b, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, q, "{"+tc[1]+"}", tc[0])
		}
		regtsExec(t, p, b, c, "CREATE EXTENSION unaccent", 0)
		defer regtsExec(t, p, b, c, "DROP EXTENSION unaccent CASCADE", 0)
		dictionaryResult(t, p, b, c, "SELECT ts_lexize('english_stem',unaccent('hôtels')) AS lexemes", "{hotel}")
	})
}

func TestReferenceSnowballMetadata(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		q := "SELECT ts_lexize($1,$2) AS lexemes"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, tc := range []struct {
			args []any
			want any
		}{
			{[]any{"english_stem", "RATS"}, "{rat}"}, {[]any{[]byte("english_stem"), []byte("ONLY")}, "{}"}, {[]any{"english_stem", ""}, "{}"}, {[]any{"english_stem", []byte{}}, "{}"}, {[]any{"english_stem", nil}, nil}, {[]any{nil, "rats"}, nil},
		} {
			dictionaryResult(t, p, b, c, q, tc.want, tc.args...)
			dictionaryResult(t, p, b, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, q, tc.want, tc.args...)
		}
		var want any = "{}"
		if b.PG {
			want = nil
		}
		dictionaryResult(t, p, b, c, q, want, "english_stem", []byte(nil))
		dictionaryResult(t, p, b, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, q, want, "english_stem", []byte(nil))
		for _, prepared := range []bool{false, true} {
			s := fixture.Select("SELECT ts_lexize('english_stem','rats') AS lexemes WHERE false", []string{"lexemes"}, []string{"_TEXT"}, nil)
			s.Prepared = prepared
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
	})
}
