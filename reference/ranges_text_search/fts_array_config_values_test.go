package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceFTSArrayConfigValues(t *testing.T) {
	ftsArrayConfigScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct {
			input string
			want  any
		}{
			{"ARRAY['b','a','a']", "'a' 'b'"},
			{"ARRAY[['b','a'],['a','c']]", "'a' 'b' 'c'"},
			{"'[0:1][4:5]={{b,a},{a,c}}'::text[]", "'a' 'b' 'c'"},
			{"'{}'::text[]", ""}, {"ARRAY[]::text[]", ""}, {"NULL::text[]", nil},
			{"ARRAY['The','rats','cat:1A','space word']", "'The' 'cat:1A' 'rats' 'space word'"},
			{`ARRAY['é','a','Z','a'] COLLATE "C"`, "'Z' 'a' 'é'"},
			{`ARRAY['é','a','Z','a'] COLLATE "POSIX"`, "'Z' 'a' 'é'"},
		} {
			t.Run(tc.input, func(t *testing.T) {
				ftsArrayConfigBoth(t, p, b, c, ftsArrayConfigVector(tc.input, tc.want))
			})
		}
		// No textual tsvector parser may interpret punctuation, positions, weights
		// or escapes. Compare recovered original lexemes rather than parity.
		ftsArrayConfigBoth(t, p, b, c, ftsArrayConfigValue(
			`SELECT tsvector_to_array(array_to_tsvector(ARRAY['a''b',E'x\\y','cat:1A','space word'])) AS result`,
			"_TEXT", `{a'b,cat:1A,"space word","x\\y"}`))
		ftsArrayConfigBoth(t, p, b, c, fixture.Select(
			"SELECT array_to_tsvector(ARRAY['b','a','a'])::text AS constructed,strip(array_to_tsvector(ARRAY['b','a','a']))::text AS stripped,tsvector_to_array(array_to_tsvector(ARRAY['b','a','a'])) AS a",
			[]string{"constructed", "stripped", "a"}, []string{"TEXT", "TEXT", "_TEXT"}, [][]any{{"'a' 'b'", "'a' 'b'", "{a,b}"}}))
		ftsArrayConfigRun(t, p, b, c, fixture.Exec("CREATE TABLE array_cfg_source(a text[])", 0))
		ftsArrayConfigRun(t, p, b, c, fixture.Exec("INSERT INTO array_cfg_source VALUES('[0:1][4:5]={{b,a},{a,c}}')", 1))
		ftsArrayConfigBoth(t, p, b, c, fixture.Select("SELECT array_to_tsvector(a) AS v,a FROM array_cfg_source",
			[]string{"v", "a"}, []string{"TSVECTOR", "_TEXT"}, [][]any{{"'a' 'b' 'c'", "[0:1][4:5]={{b,a},{a,c}}"}}))
	})
}

func TestReferenceFTSArrayConfigLimits(t *testing.T) {
	t.Run("word-bytes", func(t *testing.T) {
		ftsArrayConfigScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			for _, word := range []string{strings.Repeat("x", 2046), strings.Repeat("é", 1023)} {
				input := ftsArrayConfigArray(new(word))
				ftsArrayConfigBoth(t, p, b, c, ftsArrayConfigValue("SELECT tsvector_to_array(array_to_tsvector($1::text[])) AS result", "_TEXT", "{"+word+"}", input))
			}
			for _, word := range []string{strings.Repeat("x", 2047), strings.Repeat("é", 1024)} {
				d := fixture.ControlsReferenceDiagnostic("54000", fmt.Sprintf("word is too long (%d bytes, max 2046 bytes)", len(word)))
				ftsArrayConfigErrorRoutes(t, p, b, c, fixture.ErrorDiagnostic("SELECT array_to_tsvector($1::text[])", d, ftsArrayConfigArray(new(word))), false)
			}
		})
	})
	for _, duplicate := range []bool{false, true} {
		t.Run(fmt.Sprintf("deduplicated-total/duplicate=%t", duplicate), func(t *testing.T) {
			ftsArrayConfigScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				words := make([]*string, 0, 514)
				for i := range 512 {
					words = append(words, new(fmt.Sprintf("%04d", i)+strings.Repeat("x", 2042)))
				}
				last := "0512" + strings.Repeat("y", 1019)
				words = append(words, new(last))
				if duplicate {
					words = append(words, words[0])
				}
				// 512*2046+1023 = 1048575 surviving bytes. All entries and their
				// order are checked against independent input strings after extraction.
				q := "SELECT tsvector_to_array(array_to_tsvector($1::text[])) AS result"
				want := make([]string, 513)
				for i := range want {
					want[i] = *words[i]
				}
				ftsArrayConfigBoth(t, p, b, c, ftsArrayConfigValue(q, "_TEXT", "{"+strings.Join(want, ",")+"}", ftsArrayConfigArray(words...)))
				words[512] = new(last + "y")
				d := fixture.ControlsReferenceDiagnostic("54000", "string is too long for tsvector (1048576 bytes, max 1048575 bytes)")
				ftsArrayConfigErrorRoutes(t, p, b, c, fixture.ErrorDiagnostic(q, d, ftsArrayConfigArray(words...)), false)
				// Element validation precedes combined-size checking, even at end.
				words = append(words, new(""))
				ftsArrayConfigErrorRoutes(t, p, b, c, fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("2200F", "lexeme array may not contain empty strings"), ftsArrayConfigArray(words...)), false)
				words[len(words)-1] = nil
				ftsArrayConfigErrorRoutes(t, p, b, c, fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("22004", "lexeme array may not contain nulls"), ftsArrayConfigArray(words...)), false)
			})
		})
	}
	t.Run("storage-order-validation", func(t *testing.T) {
		ftsArrayConfigScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			empty, long := new(""), new(strings.Repeat("x", 2047))
			for _, tc := range []struct {
				words         []*string
				code, message string
			}{
				{[]*string{empty, nil, long}, "2200F", "lexeme array may not contain empty strings"},
				{[]*string{empty, long, nil}, "2200F", "lexeme array may not contain empty strings"},
				{[]*string{nil, empty, long}, "22004", "lexeme array may not contain nulls"},
				{[]*string{nil, long, empty}, "22004", "lexeme array may not contain nulls"},
				{[]*string{long, empty, nil}, "54000", "word is too long (2047 bytes, max 2046 bytes)"},
				{[]*string{long, nil, empty}, "54000", "word is too long (2047 bytes, max 2046 bytes)"},
			} {
				ftsArrayConfigErrorRoutes(t, p, b, c, fixture.ErrorDiagnostic("SELECT array_to_tsvector($1::text[])", fixture.ControlsReferenceDiagnostic(tc.code, tc.message), ftsArrayConfigArray(tc.words...)), false)
			}
		})
	})
}

func TestReferenceFTSArrayConfigPrepared(t *testing.T) {
	ftsArrayConfigScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, parameter := range []string{"$1::text[]", "$1"} {
			q := "SELECT array_to_tsvector(" + parameter + ") AS result"
			h := textSearchSettingHold(t, p, c, q)
			defer h.Statement.Close()
			for _, tc := range []struct {
				input, want   any
				code, message string
			}{
				{"{b,a,a}", "'a' 'b'", "", ""},
				{"{a,NULL}", nil, "22004", "lexeme array may not contain nulls"},
				{`{a,""}`, nil, "2200F", "lexeme array may not contain empty strings"},
				{nil, nil, "", ""}, {"{}", "", "", ""}, {"{z,b,b}", "'b' 'z'", "", ""},
			} {
				if tc.code == "" {
					s := ftsArrayConfigValue(q, "TSVECTOR", tc.want, tc.input)
					ftsArrayConfigBoth(t, p, b, c, s)
					ftsArrayConfigRun(t, p, b, h, s)
				} else {
					s := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic(tc.code, tc.message), tc.input)
					ftsArrayConfigErrorRoutes(t, p, b, c, s, false)
					for _, query := range []bool{false, true} {
						s.SelectRows, s.ErrorStage = query, "execute"
						ftsArrayConfigRun(t, p, b, h, s)
					}
				}
			}
		}
		literal := ftsArrayConfigVector("ARRAY['b','a','a']", "'a' 'b'")
		h := textSearchSettingHold(t, p, c, literal.Query)
		defer h.Statement.Close()
		for range 2 {
			ftsArrayConfigBoth(t, p, b, c, literal)
			ftsArrayConfigRun(t, p, b, h, literal)
		}
		ftsArrayConfigRun(t, p, b, c, fixture.Exec("PREPARE array_cfg_vector(text[]) AS SELECT array_to_tsvector($1) AS result", 0))
		defer c.ExecContext(p.Ctx, "DEALLOCATE array_cfg_vector")
		ftsArrayConfigRun(t, p, b, c, ftsArrayConfigValue("EXECUTE array_cfg_vector('{b,a,a}')", "TSVECTOR", "'a' 'b'"))
		ftsArrayConfigRun(t, p, b, c, fixture.Exec("PREPARE array_cfg_current AS SELECT get_current_ts_config() AS result", 0))
		defer c.ExecContext(p.Ctx, "DEALLOCATE array_cfg_current")
		for _, name := range []string{"simple", "english", "simple"} {
			ftsArrayConfigRun(t, p, b, c, fixture.Exec("SET default_text_search_config='"+name+"'", 0))
			ftsArrayConfigRun(t, p, b, c, ftsArrayConfigValue("EXECUTE array_cfg_current", "REGCONFIG", name))
		}
	})
}
