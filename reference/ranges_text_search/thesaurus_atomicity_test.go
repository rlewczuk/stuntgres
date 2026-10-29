package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceThesaurusAtomicity(t *testing.T) {
	thesaurusScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		thesaurusSetup(t, p, b, c, "thesaurus_sample")
		run := func(step fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, step) }
		var before bytes.Buffer
		if !b.PG {
			if err := stuntgres.Dump(p.Ctx, p.Native, &before); err != nil {
				t.Fatal(err)
			}
		}
		preserved := func() {
			t.Helper()
			run(fixture.Select("SELECT dictinitoption FROM pg_ts_dict WHERE oid='public.thes_dict'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{"dictfile = 'thesaurus_sample', dictionary = 'public.thes_sub'"}}))
			run(ftsConfigurationVector("SELECT to_tsvector('public.thes_cfg','one two three') AS normalized", "'123':1"))
			run(fixture.Select("SELECT count(*)::bigint AS edges FROM pg_depend WHERE classid=3600 AND objid='public.thes_dict'::regdictionary AND refclassid=3600", []string{"edges"}, []string{"INT8"}, [][]any{{int64(0)}}))
			if !b.PG {
				var after bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil || !bytes.Equal(before.Bytes(), after.Bytes()) {
					t.Fatal("failed operation changed complete dump", err)
				}
			}
		}
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("SAVEPOINT thes_save", 0))
		invalid := thesaurusDiagnostic("F0000", `thesaurus sample word "the" is a stop word (rule 1)`)
		invalid.Hint = `Use "?" to represent a stop word within a sample phrase.`
		run(fixture.ErrorDiagnostic("ALTER TEXT SEARCH DICTIONARY public.thes_dict (DictFile=stuntgres_thes_sample_stop)", invalid))
		run(fixture.Exec("ROLLBACK TO SAVEPOINT thes_save", 0))
		preserved()
		run(fixture.Exec("ALTER TEXT SEARCH DICTIONARY public.thes_dict (DictFile=stuntgres_thes_ties)", 0))
		run(fixture.Exec("ALTER TEXT SEARCH CONFIGURATION public.thes_cfg ALTER MAPPING FOR asciiword WITH public.thes_sub", 0))
		run(fixture.Exec("ROLLBACK TO SAVEPOINT thes_save", 0))
		run(fixture.Exec("COMMIT", 0))
		preserved()
		for _, prepared := range []bool{false, true} {
			step := fixture.ErrorDiagnostic("ALTER TEXT SEARCH DICTIONARY public.thes_dict (DictFile=stuntgres_thes_ties, Unknown=1)", thesaurusDiagnostic("22023", `unrecognized Thesaurus parameter: "unknown"`))
			step.Prepared = prepared
			step.ErrorStage = "execute"
			run(step)
			preserved()
		}
		if !b.PG {
			canceled, cancel := context.WithCancel(p.Ctx)
			cancel()
			_, err := c.ExecContext(canceled, "ALTER TEXT SEARCH DICTIONARY public.thes_dict (DictFile=stuntgres_thes_ties)")
			if !errors.Is(err, context.Canceled) {
				t.Fatal("pre-canceled dictionary publication", err)
			}
			preserved()
		}
		const query = "SELECT to_tsvector('public.thes_cfg',$1::text) AS normalized"
		held, err := c.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		// Native admission suspends the first token deterministically; only
		// PostgreSQL needs a sustained workload for server-side observation.
		input := "one two three"
		if b.PG {
			input = strings.Repeat("one two three ", 2_000_000)
		}
		fixture.BinaryConversionActiveCancel(t, p, b, c, query, stuntgres.WorkTSVectorConvert, func(ctx context.Context) fixture.RelationOutcome {
			rows, err := held.QueryContext(ctx, input)
			return fixture.RelationOutcome{Rows: rows, Err: err}
		})
		preserved()
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: held, SQL: query}, b.Recorder, ftsConfigurationVector(query, "'123':1", "one two three"))
	})
}
