package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func intarrayAbsent(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, s := range []fixture.Step{
		fixture.Select("SELECT extname FROM pg_extension WHERE extname='intarray'", []string{"extname"}, []string{"NAME"}, [][]any{}),
		fixture.Select("SELECT typname FROM pg_type WHERE typnamespace='ia_provider'::regnamespace AND typname IN ('query_int','_query_int','intbig_gkey','_intbig_gkey')", []string{"typname"}, []string{"NAME"}, [][]any{}),
		fixture.Select("SELECT proname FROM pg_proc WHERE pronamespace='ia_provider'::regnamespace AND proname IN ('bqarr_in','bqarr_out','_intbig_in','_intbig_out')", []string{"proname"}, []string{"NAME"}, [][]any{}),
	} {
		intarrayBoth(t, p, b, c, s)
	}
}

func TestReferenceIntarrayProviderLifecycle(t *testing.T) {
	t.Run("transaction_private_and_drop_rollback", func(t *testing.T) {
		intarrayScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("CREATE SCHEMA ia_provider", 0))
			run(fixture.Exec("BEGIN", 0))
			defer c.ExecContext(p.Ctx, "ROLLBACK")
			run(fixture.Exec("CREATE EXTENSION intarray SCHEMA ia_provider", 0))
			intarrayIdentity(t, p, b, c, intarrayFixtureOwner(b))
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT extname FROM pg_extension WHERE extname='intarray'", []string{"extname"}, []string{"NAME"}, [][]any{}))
			run(fixture.Exec("ROLLBACK", 0))
			intarrayAbsent(t, p, b, c)
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("CREATE EXTENSION intarray SCHEMA ia_provider", 0))
			run(fixture.Exec("COMMIT", 0))
			before := intarrayIdentity(t, p, b, c, intarrayFixtureOwner(b))
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("DROP EXTENSION intarray", 0))
			intarrayAbsent(t, p, b, c)
			run(fixture.Exec("ROLLBACK", 0))
			if after := intarrayIdentity(t, p, b, c, intarrayFixtureOwner(b)); after != before {
				t.Fatalf("DROP rollback changed identities %v -> %v", before, after)
			}
		})
	})
	t.Run("duplicate_restrict_reinstall", func(t *testing.T) {
		intarrayScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			intarraySeedArrays(t, p, b, c)
			intarrayInstall(t, p, b, c)
			before := intarrayIdentity(t, p, b, c, intarrayFixtureOwner(b))
			for _, prepared := range []bool{false, true} {
				intarrayError(t, p, b, c, "CREATE EXTENSION intarray", prepared, "execute", fixture.ControlsReferenceDiagnostic("42710", `extension "intarray" already exists`))
			}
			var notices []fixture.Diagnostic
			_, noticeConn, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
			fixture.RunStep(t, p.Ctx, noticeConn, recorder, fixture.Exec("CREATE EXTENSION IF NOT EXISTS intarray", 0))
			wantNotice := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42710", Message: `extension "intarray" already exists, skipping`}
			if len(notices) != 1 || notices[0] != wantNotice {
				t.Fatalf("notices=%#v want=%#v", notices, wantNotice)
			}
			if after := intarrayIdentity(t, p, b, c, intarrayFixtureOwner(b)); after != before {
				t.Fatalf("duplicate changed identities %v -> %v", before, after)
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE ia_dependency(k ia_provider.intbig_gkey)", 0))
			for _, prepared := range []bool{false, true} {
				want := fixture.ControlsReferenceDiagnostic("2BP01", "cannot drop extension intarray because other objects depend on it")
				want.Detail = "column k of table ia_dependency depends on type ia_provider.intbig_gkey"
				want.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
				intarrayError(t, p, b, c, "DROP EXTENSION intarray RESTRICT", prepared, "execute", want)
			}
			if after := intarrayIdentity(t, p, b, c, intarrayFixtureOwner(b)); after != before {
				t.Fatalf("failed DROP changed identities %v -> %v", before, after)
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DROP TABLE ia_dependency", 0))
			q := "SELECT NULL::ia_provider.intbig_gkey AS k"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			var retained []intarrayRetained
			if !b.PG {
				check := intarrayArrayCheck(b)
				rows, err := c.QueryContext(p.Ctx, check.Query)
				if err != nil {
					t.Fatal(err)
				}
				defer rows.Close()
				columns, err := rows.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
				check.CheckMetadata(t, rows)
				retained = []intarrayRetained{{rows, columns, check}}
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DROP EXTENSION intarray", 0))
			intarrayAbsent(t, p, b, c)
			intarrayArrayReads(t, p, b, c)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE EXTENSION intarray SCHEMA ia_provider", 0))
			after := intarrayIdentity(t, p, b, c, intarrayFixtureOwner(b))
			if after[0] == before[0] || after[2] == before[2] {
				t.Fatalf("reinstall reused dropped identity %v -> %v", before, after)
			}
			intarrayConsume(t, retained)
			want := fixture.ControlsReferenceDiagnostic("0A000", "cached plan must not change result type")
			failure := fixture.ErrorDiagnostic(q, want)
			failure.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, held, b.Recorder, failure)
		})
	})
	for _, prepared := range []bool{false, true} {
		name := "direct"
		if prepared {
			name = "prepared"
		}
		t.Run(name+"_late_collision", func(t *testing.T) {
			intarrayScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE SCHEMA ia_collision", 0))
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TYPE ia_collision.intbig_gkey AS (x integer)", 0))
				base := intarrayOID(t, p, c, "ia_collision.intbig_gkey")
				array := intarrayOID(t, p, c, "ia_collision._intbig_gkey")
				want := fixture.ControlsReferenceDiagnostic("42710", `type "intbig_gkey" already exists`)
				want.Context = "SQL statement \"CREATE TYPE intbig_gkey (\n        INTERNALLENGTH = -1,\n        INPUT = _intbig_in,\n        OUTPUT = _intbig_out\n)\"\nextension script file \"intarray--1.2.sql\", near line 432"
				intarrayError(t, p, b, c, "CREATE EXTENSION intarray SCHEMA ia_collision", prepared, "execute", want)
				if base != intarrayOID(t, p, c, "ia_collision.intbig_gkey") || array != intarrayOID(t, p, c, "ia_collision._intbig_gkey") {
					t.Fatal("failed installation replaced collider or companion")
				}
				for _, s := range []fixture.Step{
					fixture.Select("SELECT extname FROM pg_extension WHERE extname='intarray'", []string{"extname"}, []string{"NAME"}, [][]any{}),
					fixture.Select("SELECT typname FROM pg_type WHERE typnamespace='ia_collision'::regnamespace AND typname IN ('query_int','_query_int')", []string{"typname"}, []string{"NAME"}, [][]any{}),
					fixture.Select("SELECT proname FROM pg_proc WHERE pronamespace='ia_collision'::regnamespace AND proname IN ('bqarr_in','bqarr_out','_intbig_in','_intbig_out')", []string{"proname"}, []string{"NAME"}, [][]any{}),
				} {
					intarrayBoth(t, p, b, c, s)
				}
			})
		})
	}
	t.Run("native_ABI_helper_collision", func(t *testing.T) {
		intarrayScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			// Real server-only LANGUAGE internal loading. Descriptor collision
			// units cover native authority; do not add a fake native ABI loader.
			if !b.PG {
				return
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE SCHEMA ia_provider", 0))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE FUNCTION ia_provider._intbig_in(cstring) RETURNS text LANGUAGE internal IMMUTABLE STRICT AS 'textin'", 0))
			for _, prepared := range []bool{false, true} {
				want := fixture.ControlsReferenceDiagnostic("42723", `function "_intbig_in" already exists with same argument types`)
				want.Context = "SQL statement \"CREATE FUNCTION _intbig_in(cstring)\nRETURNS intbig_gkey\nAS '$libdir/_int'\nLANGUAGE C STRICT IMMUTABLE PARALLEL SAFE\"\nextension script file \"intarray--1.2.sql\", near line 422"
				intarrayError(t, p, b, c, "CREATE EXTENSION intarray SCHEMA ia_provider", prepared, "execute", want)
			}
			intarrayBoth(t, p, b, c, fixture.Select("SELECT typname FROM pg_type WHERE typnamespace='ia_provider'::regnamespace AND typname IN ('query_int','intbig_gkey')", []string{"typname"}, []string{"NAME"}, [][]any{}))
			intarrayBoth(t, p, b, c, fixture.Select("SELECT proname FROM pg_proc WHERE pronamespace='ia_provider'::regnamespace ORDER BY proname", []string{"proname"}, []string{"NAME"}, [][]any{{"_intbig_in"}}))
		})
	})
}
