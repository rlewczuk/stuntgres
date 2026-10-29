package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func thesaurusDiagnostic(code, message string) fixture.Diagnostic {
	return fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message}
}

func TestReferenceThesaurusDiagnostics(t *testing.T) {
	thesaurusScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		thesaurusSetup(t, p, b, c, "thesaurus_sample")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TEXT SEARCH DICTIONARY public.reject_sub (TEMPLATE=pg_catalog.simple,STOPWORDS=english,Accept=false)", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE EXTENSION unaccent", 0))
		cases := []struct{ options, code, message, hint, context string }{
			{"DictFile=stuntgres_thes_sample_stop, Dictionary='public.thes_sub'", "F0000", `thesaurus sample word "the" is a stop word (rule 1)`, `Use "?" to represent a stop word within a sample phrase.`, ""},
			{"DictFile=stuntgres_thes_substitute_stop, Dictionary='public.thes_sub'", "F0000", `thesaurus substitute word "the" is a stop word (rule 1)`, "", ""},
			{"DictFile=stuntgres_thes_ties, Dictionary='public.reject_sub'", "F0000", `thesaurus sample word "alpha" isn't recognized by subdictionary (rule 1)`, "", ""},
			{"DictFile=stuntgres_thes_unrecognized_rhs, Dictionary='public.reject_sub'", "F0000", `thesaurus substitute word "alpha" isn't recognized by subdictionary (rule 1)`, "", ""},
			{"DictFile=stuntgres_thes_bad_end, Dictionary='public.thes_sub'", "F0000", "unexpected end of line", "", "line 1 of configuration file \"stuntgres_thes_bad_end.ths\": \"alpha\n\""},
			{"DictFile=stuntgres_thes_empty_rhs, Dictionary='public.thes_sub'", "F0000", "unexpected end of line", "", "line 1 of configuration file \"stuntgres_thes_empty_rhs.ths\": \"alpha :\n\""},
			{"DictFile=stuntgres_thes_bad_end", "F0000", "unexpected end of line", "", "line 1 of configuration file \"stuntgres_thes_bad_end.ths\": \"alpha\n\""},
			{"DictFile=stuntgres_thes_bad_end, Unknown=1", "F0000", "unexpected end of line", "", "line 1 of configuration file \"stuntgres_thes_bad_end.ths\": \"alpha\n\""},
			{"DictFile=stuntgres_thes_bad_end, Dictionary='public.missing_sub'", "F0000", "unexpected end of line", "", "line 1 of configuration file \"stuntgres_thes_bad_end.ths\": \"alpha\n\""},
			{"DictFile=stuntgres_thes_bad_end, Dictionary='public.'", "F0000", "unexpected end of line", "", "line 1 of configuration file \"stuntgres_thes_bad_end.ths\": \"alpha\n\""},
			{"DictFile=stuntgres_thes_empty_rhs, Dictionary='public.missing_sub', Unknown=1", "F0000", "unexpected end of line", "", "line 1 of configuration file \"stuntgres_thes_empty_rhs.ths\": \"alpha :\n\""},
			{"DictFile=stuntgres_thes_missing, Dictionary='public.thes_sub'", "F0000", `could not open thesaurus file "stuntgres_thes_missing.ths": No such file or directory`, "", ""},
			{"DictFile='../thesaurus_sample', Dictionary='public.thes_sub'", "22023", `invalid text search configuration file name "../thesaurus_sample"`, "", ""},
			{"Dictionary='public.thes_sub'", "22023", "missing DictFile parameter", "", ""},
			{"DictFile=thesaurus_sample", "22023", "missing Dictionary parameter", "", ""},
			{"DictFile=stuntgres_thes_missing", "F0000", `could not open thesaurus file "stuntgres_thes_missing.ths": No such file or directory`, "", ""},
			{"DictFile=thesaurus_sample, DictFile=stuntgres_thes_ties, Dictionary='public.thes_sub'", "22023", "multiple DictFile parameters", "", ""},
			{"DictFile=thesaurus_sample, Dictionary='public.thes_sub', Dictionary='simple'", "22023", "multiple Dictionary parameters", "", ""},
			{"DictFile, Dictionary='public.thes_sub'", "42601", "dictfile requires a parameter", "", ""},
			{"DictFile=thesaurus_sample, Dictionary", "42601", "dictionary requires a parameter", "", ""},
			{"DictFile=thesaurus_sample, Dictionary='public.thes_sub', Unknown=1", "22023", `unrecognized Thesaurus parameter: "unknown"`, "", ""},
			{"DictFile=stuntgres_thes_missing, Dictionary='public.missing_sub'", "F0000", `could not open thesaurus file "stuntgres_thes_missing.ths": No such file or directory`, "", ""},
			{"DictFile=thesaurus_sample, Dictionary='public.missing_sub'", "42704", `text search dictionary "public.missing_sub" does not exist`, "", ""},
			{"DictFile=thesaurus_sample, Dictionary='missing_namespace.sub'", "3F000", `schema "missing_namespace" does not exist`, "", ""},
			{"DictFile=thesaurus_sample, Dictionary='public.'", "42602", "invalid name syntax", "", ""},
			{"DictFile=thesaurus_sample, Dictionary=''", "42602", "invalid name syntax", "", ""},
			{"DictFile=thesaurus_sample, Dictionary='public.thes_dict'", "XX000", "forbidden call of thesaurus or nested call", "", ""},
			{"DictFile=thesaurus_sample, Dictionary='public.unaccent'", "F0000", `thesaurus sample word "one" isn't recognized by subdictionary (rule 1)`, "", ""},
		}
		for i, tc := range cases {
			for _, prepared := range []bool{false, true} {
				query := fmt.Sprintf("/* é */ CREATE TEXT SEARCH DICTIONARY public.thes_bad_%d_%t (TEMPLATE=pg_catalog.thesaurus,%s)", i, prepared, tc.options)
				want := thesaurusDiagnostic(tc.code, tc.message)
				want.Hint = tc.hint
				want.Context = tc.context
				step := fixture.ErrorDiagnostic(query, want)
				step.Prepared = prepared
				step.ErrorStage = "execute"
				if tc.context != "" || tc.message == `could not open thesaurus file "stuntgres_thes_missing.ths": No such file or directory` {
					pg := want
					if tc.context != "" {
						pg.Context = "line 1 of configuration file \"/usr/share/postgresql/18/tsearch_data/" + tc.context[len("line 1 of configuration file \""):]
					} else {
						pg.Message = `could not open thesaurus file "/usr/share/postgresql/18/tsearch_data/stuntgres_thes_missing.ths": No such file or directory`
					}
					step.PostgresDiagnostic = &pg
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select(fmt.Sprintf("SELECT count(*)::bigint AS n FROM pg_ts_dict WHERE dictname='thes_bad_%d_%t'", i, prepared), []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
			}
		}
		want := thesaurusDiagnostic("F0000", `thesaurus sample word "the" is a stop word (rule 1)`)
		want.Hint = `Use "?" to represent a stop word within a sample phrase.`
		for _, prepared := range []bool{false, true} {
			malformed := thesaurusDiagnostic("F0000", "unexpected end of line")
			malformed.Context = "line 1 of configuration file \"stuntgres_thes_bad_end.ths\": \"alpha\n\""
			malformedPG := malformed
			malformedPG.Context = "line 1 of configuration file \"/usr/share/postgresql/18/tsearch_data/stuntgres_thes_bad_end.ths\": \"alpha\n\""
			combined := fixture.ErrorDiagnostic("ALTER TEXT SEARCH DICTIONARY public.thes_dict (DictFile=stuntgres_thes_bad_end, Unknown=1)", malformed)
			combined.PostgresDiagnostic = &malformedPG
			combined.Prepared, combined.ErrorStage = prepared, "execute"
			fixture.RunStep(t, p.Ctx, c, b.Recorder, combined)
			step := fixture.ErrorDiagnostic("ALTER TEXT SEARCH DICTIONARY public.thes_dict (DictFile=stuntgres_thes_sample_stop)", want)
			step.Prepared = prepared
			step.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT dictinitoption FROM pg_ts_dict WHERE oid='public.thes_dict'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{"dictfile = 'thesaurus_sample', dictionary = 'public.thes_sub'"}}))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, ftsConfigurationVector("SELECT to_tsvector('public.thes_cfg','one two three') AS normalized", "'123':1"))
		}
	})
}
