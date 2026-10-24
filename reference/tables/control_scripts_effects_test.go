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

func TestReferenceControlScriptsForeignEffectBoundary(t *testing.T) {
	for _, tc := range []struct {
		name, body, undo string
		changed          [][]any
	}{
		{"scan", "SELECT count(*)::integer FROM fdw_data", "", [][]any{{int64(1), "one"}, {int64(2), nil}, {int64(3), "three"}}},
		{"insert", "INSERT INTO fdw_data VALUES(4,'four',true); SELECT 3", "DELETE FROM fdw_source.data WHERE id=4", [][]any{{int64(1), "one"}, {int64(2), nil}, {int64(3), "three"}, {int64(4), "four"}}},
		{"update", "UPDATE fdw_data SET label='changed' WHERE id=1; SELECT 3", "UPDATE fdw_source.data SET label='one' WHERE id=1", [][]any{{int64(1), "changed"}, {int64(2), nil}, {int64(3), "three"}}},
		{"delete", "DELETE FROM fdw_data WHERE id=3; SELECT 3", "INSERT INTO fdw_source.data VALUES(3,'three',NULL)", [][]any{{int64(1), "one"}, {int64(2), nil}}},
		{"import", "IMPORT FOREIGN SCHEMA fdw_source LIMIT TO (data) FROM SERVER fdw_live INTO public; SELECT 3", "", [][]any{{int64(1), "one"}, {int64(2), nil}, {int64(3), "three"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fdwWriteScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
				fdwLive(t, p, b, r, run)
				definition := "CREATE FUNCTION cs_foreign() RETURNS integer LANGUAGE SQL AS $$" + tc.body + "$$"
				run(fixture.Exec(definition, 0))
				remote := func(want [][]any) {
					t.Helper()
					s := fixture.Select("SELECT id,label FROM fdw_source.data ORDER BY id", []string{"id", "label"}, []string{"INT4", "TEXT"}, want)
					if b.PG {
						run(s)
					} else {
						fixture.CatalogControlTuples(t, r.source, s.Query, want)
					}
				}
				baseline := [][]any{{int64(1), "one"}, {int64(2), nil}, {int64(3), "three"}}
				for _, prepared := range []bool{false, true} {
					s := fixture.Select("SELECT cs_foreign() AS value", []string{"value"}, []string{"INT4"}, [][]any{{int64(3)}})
					s.Prepared = prepared
					run(s)
					remote(tc.changed)
					if tc.name == "import" {
						imported := fixture.Select("SELECT id,label FROM public.data ORDER BY id", []string{"id", "label"}, []string{"INT4", "TEXT"}, baseline)
						run(imported)
						run(fixture.Prepared(imported))
						run(fixture.Exec("DROP FOREIGN TABLE public.data", 0))
					}
					if tc.undo != "" {
						fdwSourceExec(t, p, b, r, run, tc.undo, 1)
					}
				}
				remote(baseline)
				if b.PG {
					return
				}
				// The genuine declaration serializer carries authentic provider and
				// validator identities, not a handwritten substitute definition.
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
					t.Fatal(err)
				}
				var providerEvents []string
				validatedDefinition := false
				r.setHook(func(ctx context.Context, method string) error {
					if method == "Validate" {
						validatedDefinition = true
					} else {
						providerEvents = append(providerEvents, method)
					}
					return ctx.Err()
				})
				defer r.setHook(nil)
				assertNoProvider := func(before fdwReaderState) {
					t.Helper()
					after := r.snapshot()
					// calls also includes trusted declaration validation. Actual
					// acquisition/planning events and resource lifetimes may not move.
					before.calls, after.calls = 0, 0
					if after != before || len(providerEvents) != 0 {
						t.Fatal("replay acquired remote capability", before, after, providerEvents)
					}
				}
				before := r.snapshot()
				if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
				assertNoProvider(before)
				if !validatedDefinition {
					t.Fatal("definition replay bypassed trusted validator")
				}
				run(fixture.Select("SELECT ftoptions::text AS options FROM pg_foreign_table WHERE ftrelid='fdw_data'::regclass::oid", []string{"options"}, []string{"TEXT"}, [][]any{{"{schema_name=fdw_source,table_name=data}"}}))
				run(fixture.Exec("CREATE TABLE cs_sentinel(v integer)", 0))
				run(fixture.Exec("INSERT INTO cs_sentinel VALUES(77)", 1))
				before = r.snapshot()
				providerEvents = nil
				writeCalls := 0
				r.writer.writeHook = func(ctx context.Context, method string) error { writeCalls++; return ctx.Err() }
				defer func() { r.writer.writeHook = nil }()
				script := dump.String() + "CREATE TABLE must_not_publish(v integer); CREATE TABLE cs_effect(v integer DEFAULT cs_foreign()); INSERT INTO cs_effect DEFAULT VALUES;"
				err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script))
				d := fixture.ControlsReferenceDiagnostic("0A000", "restore cannot stage non-retractable effects")
				if tc.name == "insert" {
					d.Message = "restore cannot write foreign tables"
				}
				d.Context = "SQL function \"cs_foreign\" statement 1"
				fixture.AssertControlDiagnostic(t, err, d)
				assertNoProvider(before)
				if writeCalls != 0 {
					t.Fatal("rejected replay acquired foreign modification capability", writeCalls)
				}
				remote(baseline)
				run(fixture.Select("SELECT v FROM cs_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(77)}}))
				run(fixture.Select("SELECT count(*) FROM pg_class WHERE relname='must_not_publish'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
				if tc.name == "import" {
					run(fixture.Select("SELECT count(*) FROM pg_class WHERE relname='data'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
				}
				// Trusted validators remain actual declaration callbacks. Their
				// ordinary error must abort publication without being SQL-wrapped.
				validationFailure := errors.New("control validator rejects table")
				validated := false
				r.mu.Lock()
				r.validateHook = func(ctx context.Context, kind stuntgres.ForeignObjectKind, options []stuntgres.ForeignOption) error {
					if kind == stuntgres.ForeignTable {
						validated = true
						return validationFailure
					}
					return ctx.Err()
				}
				r.mu.Unlock()
				err = stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes()))
				r.mu.Lock()
				r.validateHook = nil
				r.mu.Unlock()
				if !validated || !errors.Is(err, validationFailure) {
					t.Fatal("trusted validator did not retain original failure", validated, err)
				}
				var state interface{ SQLState() string }
				if errors.As(err, &state) {
					t.Fatal("validator error acquired SQLSTATE", err)
				}
				run(fixture.Select("SELECT v FROM cs_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(77)}}))
				remote(baseline)
			})
		})
	}
}
