package reference_test

import (
	"bytes"
	"database/sql"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceThesaurusFeatureSmoke(t *testing.T) {
	thesaurusScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		thesaurusSetup(t, p, b, c, "thesaurus_sample")
		const query = "SELECT to_tsvector('public.thes_cfg',$1::text) AS normalized"
		held, err := c.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		vector := func(input, want string) {
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: held, SQL: query}, b.Recorder, ftsConfigurationVector(query, want, input))
		}
		run := func(step fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, step) }
		vector("one two three", "'123':1")
		vector("booking the tickets", "'cards':3 'invitation':2 'order':1")
		vector("one two extra", "'12':1 'extra':2")
		run(fixture.Exec("ALTER TEXT SEARCH DICTIONARY public.thes_dict (DictFile=stuntgres_thes_ties)", 0))
		vector("Alpha Beta", "'last':1")
		run(fixture.Prepared(fixture.Exec("ALTER TEXT SEARCH DICTIONARY public.thes_dict (DictFile=stuntgres_thes_ties_reverse)", 0)))
		vector("Alpha Beta", "'first':1")
		invalid := thesaurusDiagnostic("F0000", `thesaurus sample word "the" is a stop word (rule 1)`)
		invalid.Hint = `Use "?" to represent a stop word within a sample phrase.`
		run(fixture.ErrorDiagnostic("ALTER TEXT SEARCH DICTIONARY public.thes_dict (DictFile=stuntgres_thes_sample_stop)", invalid))
		vector("Alpha Beta", "'first':1")
		run(fixture.Select("SELECT tmplname,tmplinit::oid::bigint,tmpllexize::oid::bigint FROM pg_ts_template WHERE oid=3742", []string{"tmplname", "tmplinit", "tmpllexize"}, []string{"NAME", "INT8", "INT8"}, [][]any{{"thesaurus", int64(3740), int64(3741)}}))
		if !b.PG {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			run(ftsConfigurationVector("SELECT to_tsvector('public.thes_cfg','Alpha Beta') AS normalized", "'first':1"))
			var after bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil || !bytes.Equal(dump.Bytes(), after.Bytes()) {
				t.Fatal("smoke logical redump", err)
			}
		}
	})
}
