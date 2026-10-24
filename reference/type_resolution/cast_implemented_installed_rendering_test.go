package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// The installed citext(boolean) callable, not a lookalike user routine, is
// stored in a real default. Presentation follows its current namespace while
// execution and replay retain the original routine identity.
func TestReferenceCastImplementedInstalledCastRendering(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "cast_routine_before")
	fixture.SchemaReferenceCleanup(t, "cast_routine_after")
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			for _, q := range []string{
				"CREATE SCHEMA cast_routine_before",
				"CREATE EXTENSION citext SCHEMA cast_routine_before",
				"CREATE TABLE public.cast_routine_saved(v cast_routine_before.citext DEFAULT cast_routine_before.citext(true))",
				"SET search_path=pg_catalog",
			} {
				run(fixture.Exec(q, 0))
			}
			definition := "SELECT pg_get_expr(adbin,adrelid) AS expression FROM pg_attrdef WHERE adrelid='public.cast_routine_saved'::regclass"
			run(fixture.Select(definition, []string{"expression"}, []string{"TEXT"}, [][]any{{"cast_routine_before.citext(true)"}}))
			insert := fixture.Exec("INSERT INTO public.cast_routine_saved DEFAULT VALUES", 1)
			run(insert)
			run(fixture.Exec("ALTER SCHEMA cast_routine_before RENAME TO cast_routine_after", 0))
			// Reusing the old spelling makes accidental lexical rebinding observable:
			// replaying the stale default would now call this different routine.
			run(fixture.Exec("CREATE SCHEMA cast_routine_before", 0))
			run(fixture.Exec("CREATE FUNCTION cast_routine_before.citext(boolean) RETURNS text LANGUAGE SQL AS $$SELECT 'shadow'::text$$", 0))
			current := fixture.Select(definition, []string{"expression"}, []string{"TEXT"}, [][]any{{"cast_routine_after.citext(true)"}})
			run(current)
			run(fixture.Select("SELECT cast_routine_before.citext(true) AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"shadow"}}))
			run(insert)
			values := fixture.Select("SELECT v::text AS v FROM public.cast_routine_saved", []string{"v"}, []string{"TEXT"}, [][]any{{"true"}, {"true"}})
			run(values)
			if !b.PG {
				// Native-only public controls are an explicit lifecycle phase, not
				// replacement SQL or an oracle-normalized default definition.
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
				run(fixture.Exec("SET search_path=pg_catalog", 0))
				run(current)
				run(values)
				connector, err := stuntgres.NewDriver().OpenConnector(t.Name() + "/fresh-installed-cast")
				if err != nil {
					t.Fatal(err)
				}
				control := sql.OpenDB(connector)
				defer control.Close()
				if err := stuntgres.Restore(p.Ctx, control, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
				fresh, rec := fixture.RecordedNativePool(t, connector)
				fixture.RunStep(t, p.Ctx, fresh, rec, fixture.Exec("SET search_path=pg_catalog", 0))
				fixture.RunStep(t, p.Ctx, fresh, rec, current)
				fixture.RunStep(t, p.Ctx, fresh, rec, insert)
				values.Rows = append(values.Rows, []any{"true"})
				fixture.RunStep(t, p.Ctx, fresh, rec, values)
			}
		})
	}
}
