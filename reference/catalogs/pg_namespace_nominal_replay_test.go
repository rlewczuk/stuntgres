package reference_test

import (
	"bytes"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func pgNamespaceNominalReplay(t *testing.T) {
	pgNamespaceScenario(t, nil, []string{"ns18_nominal_owner"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		c := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
		pgNamespaceExec(t, p, b, c, "CREATE ROLE ns18_nominal_owner")
		t.Cleanup(func() {
			pgNamespaceExec(t, p, b, c, "RESET ROLE", "SET ROLE ns18_nominal_owner", "DROP TABLE IF EXISTS public.ns18_nominal_rows", "DROP DOMAIN IF EXISTS public.ns18_nominal_domain", "DROP TYPE IF EXISTS public.ns18_nominal_record")
			if b.PG {
				pgNamespaceExec(t, p, b, c, "DROP TYPE IF EXISTS public.ns18_nominal_enum")
			} else {
				pgNamespaceExec(t, p, b, c, "DROP OWNED BY ns18_nominal_owner CASCADE")
			}
			pgNamespaceExec(t, p, b, c, "RESET ROLE")
		})
		fixture.WithPublicSchemaOwner(t, p, b, "ns18_nominal_owner", func() {
			pgNamespaceExec(t, p, b, c, "SET ROLE ns18_nominal_owner", "CREATE DOMAIN public.ns18_nominal_domain AS INT", "CREATE TYPE public.ns18_nominal_enum AS ENUM('ok')", "CREATE TYPE public.ns18_nominal_record AS (i INT)", "CREATE TABLE public.ns18_nominal_rows(v public.ns18_nominal_domain,e public.ns18_nominal_enum,c public.ns18_nominal_record)")
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO public.ns18_nominal_rows VALUES(7,'ok','(8)')", 1))
			pgNamespaceExec(t, p, b, c, "RESET ROLE")
		})
		check := func() {
			pgNamespaceExec(t, p, b, c, "SET ROLE ns18_nominal_owner")
			s := fixture.Select("SELECT v::int4,e::text,c::text FROM public.ns18_nominal_rows", []string{"v", "e", "c"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(7), "ok", "(8)"}})
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
			for _, q := range []string{"CREATE DOMAIN public.ns18_nominal_denied AS INT", "CREATE TYPE public.ns18_nominal_denied AS ENUM('ok')", "CREATE TYPE public.ns18_nominal_denied AS (i INT)"} {
				denied := fixture.ErrorDiagnostic(q, fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for schema public"})
				fixture.RunStep(t, p.Ctx, c, b.Recorder, denied)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(denied))
			}
			pgNamespaceExec(t, p, b, c, "RESET ROLE")
		}
		check()
		if !b.PG {
			dump := fixture.ViewDump(t, p)
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			fixture.ViewRestore(t, p, dump)
			check()
			if again := fixture.ViewDump(t, p); !bytes.Equal(dump, again) {
				t.Fatal("named-owner nominal replay changed complete dump", strings.TrimSpace(string(dump)), strings.TrimSpace(string(again)))
			}
		}
		// Prove actual restored owner authority, not only readable values.
		pgNamespaceExec(t, p, b, c, "SET ROLE ns18_nominal_owner", "DROP TABLE public.ns18_nominal_rows", "DROP DOMAIN public.ns18_nominal_domain", "DROP TYPE public.ns18_nominal_record")
		if b.PG {
			pgNamespaceExec(t, p, b, c, "DROP TYPE public.ns18_nominal_enum")
		} else {
			// DROP TYPE supports standalone composites, not enum deletion.
			// Keep that exact boundary and remove the owned enum via existing
			// ownership cleanup rather than expanding nominal DDL for a fixture.
			denied := fixture.ErrorDiagnostic("DROP TYPE public.ns18_nominal_enum", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "statement is unsupported", Position: "1"})
			fixture.RunStep(t, p.Ctx, c, b.Recorder, denied)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(denied))
			value := fixture.Select("SELECT 'ok'::public.ns18_nominal_enum::text AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"ok"}})
			fixture.RunStep(t, p.Ctx, c, b.Recorder, value)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(value))
			pgNamespaceExec(t, p, b, c, "DROP OWNED BY ns18_nominal_owner CASCADE")
		}
		pgNamespaceExec(t, p, b, c, "RESET ROLE")
	})
}
