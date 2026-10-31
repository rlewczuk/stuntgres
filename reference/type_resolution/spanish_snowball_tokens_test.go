package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func spanishTokenRouting(t *testing.T) {
	spanishScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		columns := []string{"alias", "token", "dictionaries", "dictionary", "lexemes"}
		types := []string{"TEXT", "TEXT", "_REGDICTIONARY", "REGDICTIONARY", "_TEXT"}
		for _, s := range []fixture.Step{
			fixture.Select("SELECT alias,token,dictionaries,dictionary,lexemes FROM ts_debug('spanish','CASAS-niños 123 abc123')", columns, types, [][]any{
				{"hword", "CASAS-niños", "{spanish_stem}", "spanish_stem", "{casas-niñ}"},
				{"hword_asciipart", "CASAS", "{spanish_stem}", "spanish_stem", "{cas}"},
				{"blank", "-", "{}", nil, nil},
				{"hword_part", "niños", "{spanish_stem}", "spanish_stem", "{niñ}"},
				{"blank", " ", "{}", nil, nil},
				{"uint", "123", "{simple}", "simple", "{123}"},
				{"blank", " ", "{}", nil, nil},
				{"numword", "abc123", "{simple}", "simple", "{abc123}"},
			}),
			fixture.Select("SELECT alias,token,dictionaries,dictionary,lexemes FROM ts_debug('spanish','casas-perros')", columns, types, [][]any{
				{"asciihword", "casas-perros", "{spanish_stem}", "spanish_stem", "{casas-perr}"},
				{"hword_asciipart", "casas", "{spanish_stem}", "spanish_stem", "{cas}"},
				{"blank", "-", "{}", nil, nil},
				{"hword_asciipart", "perros", "{spanish_stem}", "spanish_stem", "{perr}"},
			}),
		} {
			spanishSmokeBoth(t, p, b, c, s)
		}
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH CONFIGURATION spanish_chain (COPY=spanish)", 0)
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH CONFIGURATION spanish_chain ALTER MAPPING FOR asciiword,word WITH spanish_stem,simple", 0)
		spanishSmokeBoth(t, p, b, c, fixture.Select("SELECT alias,token,dictionaries,dictionary,lexemes FROM ts_debug('spanish_chain','casas más')", columns, types, [][]any{
			{"asciiword", "casas", "{spanish_stem,simple}", "spanish_stem", "{cas}"},
			{"blank", " ", "{}", nil, nil},
			{"word", "más", "{spanish_stem,simple}", "spanish_stem", "{}"},
		}))
		spanishSmokeBoth(t, p, b, c, fixture.Select("SELECT to_tsvector('spanish_chain','casas más perros') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'cas':1 'perr':3"}}))
	})
}
