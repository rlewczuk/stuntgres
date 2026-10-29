package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceThesaurusControls(t *testing.T) {
	t.Run("ReplayAuthority", thesaurusReplayAuthority)
	fixture.RoleReferenceCleanup(t, []string{"thes_controls_owner", "thes_restore_denied"})
	fixture.SchemaReferenceCleanup(t, "thes_controls_schema")
	var emitted []byte
	thesaurusScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(step fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, step) }
		if !b.PG {
			run(fixture.Exec("CREATE TEXT SEARCH DICTIONARY pg_temp.thes_controls_source_temp (TEMPLATE=pg_catalog.simple)", 0))
		}
		for _, q := range []string{
			"CREATE ROLE thes_controls_owner", "CREATE ROLE thes_restore_denied",
			"CREATE SCHEMA thes_controls_schema AUTHORIZATION thes_controls_owner",
			"CREATE TEXT SEARCH DICTIONARY thes_controls_schema.zz_sub (TEMPLATE=pg_catalog.simple,STOPWORDS=english)",
			"CREATE TEXT SEARCH DICTIONARY thes_controls_schema.aa_thes (TEMPLATE=pg_catalog.thesaurus,DictFile=thesaurus_sample,Dictionary='thes_controls_schema.zz_sub')",
			"ALTER TEXT SEARCH DICTIONARY thes_controls_schema.zz_sub OWNER TO thes_controls_owner",
			"ALTER TEXT SEARCH DICTIONARY thes_controls_schema.aa_thes OWNER TO thes_controls_owner",
			"SET search_path=thes_controls_schema,public",
			"CREATE TEXT SEARCH DICTIONARY thes_controls_schema.ab_unqualified (TEMPLATE=pg_catalog.thesaurus,DictFile=thesaurus_sample,Dictionary='zz_sub')",
			"ALTER TEXT SEARCH DICTIONARY thes_controls_schema.ab_unqualified OWNER TO thes_controls_owner",
			"CREATE TEXT SEARCH CONFIGURATION public.thes_saved (COPY=pg_catalog.simple)",
			"ALTER TEXT SEARCH CONFIGURATION public.thes_saved ALTER MAPPING FOR asciiword WITH thes_controls_schema.aa_thes,thes_controls_schema.zz_sub",
			"ALTER TEXT SEARCH CONFIGURATION public.thes_saved OWNER TO thes_controls_owner",
			"CREATE TABLE public.thes_saved_refs(d regdictionary,c regconfig)",
		} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("INSERT INTO public.thes_saved_refs VALUES('thes_controls_schema.aa_thes','public.thes_saved')", 1))
		check := func(p *fixture.Pair, b fixture.Backend, e fixture.Executor) {
			t.Helper()
			for _, prepared := range []bool{false, true} {
				step := ftsConfigurationVector("SELECT to_tsvector(c,'one two three') AS normalized FROM public.thes_saved_refs", "'123':1")
				step.Prepared = prepared
				fixture.RunStep(t, p.Ctx, e, b.Recorder, step)
				scalar := fixture.Select("SELECT ts_lexize(d,'one') AS lexemes FROM public.thes_saved_refs", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{"{1}"}})
				scalar.Prepared = prepared
				fixture.RunStep(t, p.Ctx, e, b.Recorder, scalar)
				logical := fixture.Select("SELECT ts_lexize('thes_controls_schema.ab_unqualified','one') AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{"{1}"}})
				logical.Prepared = prepared
				fixture.RunStep(t, p.Ctx, e, b.Recorder, logical)
			}
			fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Select("SELECT dictowner='thes_controls_owner'::regrole::oid AS owned FROM pg_ts_dict WHERE oid='thes_controls_schema.aa_thes'::regdictionary", []string{"owned"}, []string{"BOOL"}, [][]any{{true}}))
			fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Select("SELECT count(*)::bigint AS edges FROM pg_depend WHERE classid=3600 AND objid='thes_controls_schema.aa_thes'::regdictionary AND refclassid=3600", []string{"edges"}, []string{"INT8"}, [][]any{{int64(0)}}))
		}
		check(p, b, c)
		if b.PG {
			if len(emitted) == 0 {
				t.Fatal("native emitted stream missing")
			}
			for _, q := range []string{"DROP TABLE public.thes_saved_refs", "DROP TEXT SEARCH CONFIGURATION public.thes_saved", "DROP SCHEMA thes_controls_schema CASCADE"} {
				run(fixture.Exec(q, 0))
			}
			if _, err := c.ExecContext(p.Ctx, string(emitted)); err != nil {
				t.Fatal("actual emitted SQL PostgreSQL replay", err)
			}
			check(p, b, c)
			return
		}
		dump := func(p *fixture.Pair) []byte {
			var out bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &out); err != nil {
				t.Fatal(err)
			}
			return bytes.Clone(out.Bytes())
		}
		emitted = dump(p)
		subAt := bytes.Index(emitted, []byte(`CREATE TEXT SEARCH DICTIONARY "thes_controls_schema"."zz_sub"`))
		thesAt := bytes.Index(emitted, []byte(`CREATE TEXT SEARCH DICTIONARY "thes_controls_schema"."aa_thes"`))
		if subAt < 0 || thesAt < subAt {
			t.Fatal("logical replay predecessor ordering", string(emitted))
		}
		retained, err := c.QueryContext(p.Ctx, "SELECT to_tsvector(c,'one two three') AS normalized FROM public.thes_saved_refs")
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		metadata, err := retained.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		for _, reset := range []bool{false, true} {
			if reset {
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
					t.Fatal(err)
				}
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(emitted)); err != nil {
				t.Fatal(err)
			}
			check(p, b, c)
			if !bytes.Equal(dump(p), emitted) {
				t.Fatal("nondeterministic redump")
			}
		}
		if !retained.Next() {
			t.Fatal("retained Rows", retained.Err())
		}
		var value string
		if err := retained.Scan(&value); err != nil || value != "'123':1" {
			t.Fatal(value, err)
		}
		if retained.Next() || retained.Err() != nil || metadata[0].DatabaseTypeName() != "TSVECTOR" {
			t.Fatal("retained metadata", retained.Err())
		}
		retained.Close()
		run(fixture.Exec("CREATE TABLE public.thes_sentinel(v int)", 0))
		run(fixture.Exec("INSERT INTO public.thes_sentinel VALUES(42)", 1))
		baseline := dump(p)
		preserved := func() {
			check(p, b, c)
			run(fixture.Select("SELECT v FROM public.thes_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}}))
			if !bytes.Equal(dump(p), baseline) {
				t.Fatal("failed restore changed complete destination")
			}
		}
		malformed := thesaurusDiagnostic("F0000", "unexpected end of line")
		malformed.Context = "line 1 of configuration file \"stuntgres_thes_bad_end.ths\": \"alpha\n\""
		for _, failure := range []struct {
			suffix string
			want   fixture.Diagnostic
		}{
			{"CREATE TEXT SEARCH DICTIONARY public.thes_late (TEMPLATE=pg_catalog.thesaurus,DictFile=stuntgres_thes_bad_end,Dictionary='thes_controls_schema.zz_sub');", malformed},
			{"CREATE TEXT SEARCH DICTIONARY public.thes_late (TEMPLATE=pg_catalog.thesaurus,DictFile=stuntgres_thes_missing,Dictionary='thes_controls_schema.zz_sub');", thesaurusDiagnostic("F0000", `could not open thesaurus file "stuntgres_thes_missing.ths": No such file or directory`)},
			{"SET SESSION AUTHORIZATION thes_restore_denied; CREATE TEXT SEARCH DICTIONARY public.thes_late (TEMPLATE=pg_catalog.thesaurus,DictFile=thesaurus_sample,Dictionary='thes_controls_schema.zz_sub');", thesaurusDiagnostic("42501", "permission denied for schema thes_controls_schema")},
		} {
			fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(string(emitted)+failure.suffix)), &failure.want)
			preserved()
		}
		canceled, cancel := context.WithCancel(p.Ctx)
		cancel()
		if err := stuntgres.Restore(canceled, p.Native, bytes.NewReader(emitted)); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		preserved()
		reading, stop := context.WithCancel(p.Ctx)
		err = stuntgres.Restore(reading, p.Native, &fixture.EnumCancelReader{Reader: bytes.NewReader(emitted), Cancel: stop})
		stop()
		if !errors.Is(err, context.Canceled) {
			t.Fatal("reader cancellation", err)
		}
		preserved()
		var sourceOID int64
		if err := c.QueryRowContext(p.Ctx, "SELECT 'thes_controls_schema.zz_sub'::regdictionary::oid::bigint").Scan(&sourceOID); err != nil {
			t.Fatal(err)
		}
		fresh := fixture.NewFixture(t, false, true)
		fc, err := fresh.Memory.Conn(fresh.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer fc.Close()
		fb := fixture.Backend{DB: fresh.Memory, Recorder: fresh.MemoryRecord}
		for _, q := range []string{"CREATE ROLE thes_controls_owner", "CREATE ROLE thes_restore_denied"} {
			fixture.RunStep(t, fresh.Ctx, fc, fb.Recorder, fixture.Exec(q, 0))
		}
		var collisionName string
		for i := 0; ; i++ {
			name := fmt.Sprintf("pg_temp.thes_collision_%d", i)
			fixture.RunStep(t, fresh.Ctx, fc, fb.Recorder, fixture.Exec("CREATE TEXT SEARCH DICTIONARY "+name+" (TEMPLATE=pg_catalog.simple)", 0))
			var oid int64
			if err := fc.QueryRowContext(fresh.Ctx, "SELECT $1::regdictionary::oid::bigint", name).Scan(&oid); err != nil {
				t.Fatal(err)
			}
			if oid > sourceOID {
				t.Fatalf("dictionary collision overshot %d > %d", oid, sourceOID)
			}
			if oid == sourceOID {
				collisionName = name
				break
			}
		}
		if err := stuntgres.Restore(fresh.Ctx, fresh.Native, bytes.NewReader(emitted)); err != nil {
			t.Fatal("fresh remap restore", err)
		}
		check(fresh, fb, fc)
		var destinationOID int64
		if err := fc.QueryRowContext(fresh.Ctx, "SELECT 'thes_controls_schema.zz_sub'::regdictionary::oid::bigint").Scan(&destinationOID); err != nil || destinationOID == sourceOID {
			t.Fatal("normalizer collision remap", sourceOID, destinationOID, err)
		}
		fixture.RunStep(t, fresh.Ctx, fc, fb.Recorder, fixture.Select("SELECT ts_lexize($1::regdictionary,'ONE') AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{"{one}"}}, collisionName))
		if !bytes.Equal(dump(fresh), emitted) {
			t.Fatal("fresh deterministic redump")
		}
	})
}
