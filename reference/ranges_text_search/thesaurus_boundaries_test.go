package reference_test

import (
	"database/sql"
	"reflect"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceThesaurusBoundaries(t *testing.T) {
	t.Run("InitializationDemand", thesaurusInitializationDemand)
	t.Run("OversizedNotices", thesaurusOversizedNotices)
	thesaurusScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		thesaurusSetup(t, p, b, c, "thesaurus_sample")
		run := func(step fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, step) }
		vectors := func(input, want string) {
			for _, prepared := range []bool{false, true} {
				step := ftsConfigurationVector("SELECT to_tsvector('public.thes_cfg',$1::text) AS normalized", want, input)
				step.Prepared = prepared
				run(step)
			}
		}
		vectors("one 7 two", "'2':3 '7':2 'one':1")
		vectors("one extra two", "'1':1 '2':3 'extra':2")
		run(fixture.Exec("ALTER TEXT SEARCH CONFIGURATION public.thes_cfg DROP MAPPING FOR uint", 0))
		vectors("one 7 two", "'12':1")
		vectors("one\t\n two", "'12':1")
		run(fixture.Exec("ALTER TEXT SEARCH DICTIONARY public.thes_dict (DictFile=stuntgres_thes_cross_category)", 0))
		run(fixture.Exec("ALTER TEXT SEARCH CONFIGURATION public.thes_cfg ALTER MAPPING FOR word WITH public.thes_sub,public.thes_dict", 0))
		vectors("alpha café", "'cross':1")
		run(fixture.Select("SELECT tokid,token FROM ts_parse('default','one 7 two alpha café')", []string{"tokid", "token"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "one"}, {int64(12), " "}, {int64(22), "7"}, {int64(12), " "}, {int64(1), "two"}, {int64(12), " "}, {int64(1), "alpha"}, {int64(12), " "}, {int64(2), "café"}}))
		run(fixture.Exec("CREATE TEXT SEARCH CONFIGURATION public.thes_other (COPY=pg_catalog.simple)", 0))
		for range 2 {
			run(ftsConfigurationVector("SELECT to_tsvector('public.thes_other','alpha café') AS normalized", "'alpha':1 'café':2"))
			vectors("alpha café", "'cross':1")
		}
		run(fixture.Exec("CREATE EXTENSION unaccent", 0))
		run(fixture.Exec("ALTER TEXT SEARCH DICTIONARY public.thes_dict (DictFile=stuntgres_thes_literals)", 0))
		run(fixture.Exec("ALTER TEXT SEARCH CONFIGURATION public.thes_cfg ALTER MAPPING FOR word WITH public.unaccent,public.thes_dict,public.thes_sub", 0))
		vectors("bríght star", "'KeEp':2 'mixed':1")
		vectors("bríght unknown", "'bríght':1 'unknown':2")
		vectors("bríght", "'bríght':1")
		vectors("bríghtness", "'brightness':1")
		run(fixture.Exec("ALTER TEXT SEARCH CONFIGURATION public.thes_cfg ALTER MAPPING FOR uint WITH pg_catalog.simple", 0))
		vectors("bríght 7", "'7':2 'bríght':1")
		run(fixture.Exec("ALTER TEXT SEARCH DICTIONARY public.thes_dict (DictFile=stuntgres_thes_cross_category)", 0))
		// Earlier continuation filters must not transform café before the active
		// thesaurus sees it, despite unaccent occupying the first word slot.
		vectors("alpha café", "'cross':1")
	})
}

func thesaurusInitializationDemand(t *testing.T) {
	thesaurusScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		thesaurusSetup(t, p, b, c, "thesaurus_sample")
		const query = "SELECT to_tsvector('public.thes_cfg',$1::text) AS normalized"
		held, err := c.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		run := func(step fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, step) }
		allRoutes := func(step fixture.Step) {
			t.Helper()
			run(step)
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: held, SQL: query}, b.Recorder, step)
			run(fixture.Prepared(step))
		}
		run(fixture.Exec("ALTER TEXT SEARCH DICTIONARY public.thes_sub (Accept=false)", 0))
		// Independent released-chain expectations: uint reaches only its own
		// simple mapping, and an empty document consults no dictionary.
		allRoutes(ftsConfigurationVector(query, "'7':1", "7"))
		allRoutes(ftsConfigurationVector(query, "", ""))
		rejected := thesaurusDiagnostic("F0000", `thesaurus sample word "one" isn't recognized by subdictionary (rule 1)`)
		allRoutes(fixture.ErrorDiagnostic(query, rejected, "one"))
		run(fixture.Exec("ALTER TEXT SEARCH CONFIGURATION public.thes_cfg ALTER MAPPING FOR asciiword WITH pg_catalog.simple,public.thes_dict", 0))
		// A recognized nonfiltering result ends this token's dictionary chain;
		// the later invalid thesaurus is not an execution demand.
		allRoutes(ftsConfigurationVector(query, "'one':1", "one"))
		run(fixture.Exec("ALTER TEXT SEARCH CONFIGURATION public.thes_cfg ALTER MAPPING FOR asciiword WITH public.thes_dict,public.thes_sub", 0))
		allRoutes(fixture.ErrorDiagnostic(query, rejected, "one"))
		run(fixture.Exec("ALTER TEXT SEARCH DICTIONARY public.thes_sub (Accept=true)", 0))
		allRoutes(ftsConfigurationVector(query, "'1':1", "one"))
	})
}

func thesaurusOversizedNotices(t *testing.T) {
	thesaurusScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		thesaurusSetup(t, p, b, c, "thesaurus_sample")
		var notices []fixture.Diagnostic
		_, owner, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		const query = "SELECT to_tsvector('public.thes_cfg',$1::text) AS normalized"
		held, err := owner.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		long := strings.Repeat("a", 2047)
		notice := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "54000", Message: "word is too long to be indexed", Detail: "Words longer than 2047 characters are ignored."}
		for _, tc := range []struct {
			input, vector string
			count         int
		}{
			{"one " + long + " two", "'12':1", 1},
			{"booking " + long + " unknown", "'booking':1 'unknown':2", 1},
			{"booking " + long, "'booking':1", 1},
			{"one two " + long + " extra", "'12':1 'extra':2", 1},
			{"one " + long + " two " + long + " extra", "'12':1 'extra':2", 2},
		} {
			want := make([]fixture.Diagnostic, tc.count)
			for i := range want {
				want[i] = notice
			}
			for _, route := range []string{"direct", "fresh", "held", "held-repeat"} {
				step := ftsConfigurationVector(query, tc.vector, tc.input)
				var executor fixture.Executor = owner
				if route == "fresh" {
					step.Prepared = true
				} else if route == "held" || route == "held-repeat" {
					executor = fixture.HeldStatement{Executor: owner, Statement: held, SQL: query}
				}
				fixture.RunStep(t, p.Ctx, executor, recorder, step)
				if !reflect.DeepEqual(notices, want) {
					t.Fatalf("%s notices=%#v want=%#v", route, notices, want)
				}
				notices = nil
			}
		}
	})
}
