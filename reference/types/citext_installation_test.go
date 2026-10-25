package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceCitextInstallation(t *testing.T) {
	t.Run("identity", func(t *testing.T) {
		citextScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			for _, prepared := range []bool{false, true} {
				q := "SELECT 'MiX'::citext AS v"
				want := citextDiagnostic("42704", `type "citext" does not exist`)
				want.Position = citextPosition(q, "citext")
				citextError(t, p, b, c, q, prepared, true, true, want)
			}
			run(fixture.Select("SELECT name,default_version,installed_version,comment FROM pg_available_extensions WHERE name='citext'", []string{"name", "default_version", "installed_version", "comment"}, []string{"NAME", "TEXT", "TEXT", "TEXT"}, [][]any{{"citext", "1.8", nil, "data type for case-insensitive character strings"}}))
			citextInstall(t, p, b, c)
			run(fixture.Select("SELECT extname,extversion,extrelocatable FROM pg_extension WHERE extname='citext'", []string{"extname", "extversion", "extrelocatable"}, []string{"NAME", "TEXT", "BOOL"}, [][]any{{"citext", "1.8", true}}))
			run(fixture.Select("SELECT superuser,trusted,relocatable FROM pg_available_extension_versions WHERE name='citext' AND version='1.8'", []string{"superuser", "trusted", "relocatable"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true}}))
			run(fixture.Select("SELECT installed_version FROM pg_available_extensions WHERE name='citext'", []string{"installed_version"}, []string{"TEXT"}, [][]any{{"1.8"}}))
			var before, after int64
			if err := c.QueryRowContext(p.Ctx, "SELECT 'citext'::regtype::oid").Scan(&before); err != nil {
				t.Fatal(err)
			}
			for _, prepared := range []bool{false, true} {
				citextError(t, p, b, c, "CREATE EXTENSION citext", prepared, false, false, citextDiagnostic("42710", `extension "citext" already exists`))
			}
			var notices []fixture.Diagnostic
			_, noticeConn, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
			fixture.RunStep(t, p.Ctx, noticeConn, recorder, fixture.Exec("CREATE EXTENSION IF NOT EXISTS citext", 0))
			wantNotice := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42710", Message: `extension "citext" already exists, skipping`}
			if len(notices) != 1 || notices[0] != wantNotice {
				t.Fatalf("skip notices %#v want %#v", notices, wantNotice)
			}
			if err := c.QueryRowContext(p.Ctx, "SELECT 'public.citext'::regtype::oid").Scan(&after); err != nil {
				t.Fatal(err)
			}
			if before <= 0 || before != after {
				t.Fatalf("identity %d -> %d", before, after)
			}
		})
	})
	t.Run("transaction_visibility", func(t *testing.T) {
		citextScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("BEGIN", 0))
			defer c.ExecContext(p.Ctx, "ROLLBACK")
			citextInstall(t, p, b, c)
			run(fixture.CitextSelect(b, "SELECT 'Private'::citext AS v", []string{"v"}, [][]any{{"Private"}}))
			peer, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer peer.Close()
			fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Select("SELECT extname FROM pg_extension WHERE extname='citext'", []string{"extname"}, []string{"NAME"}, [][]any{}))
			run(fixture.Exec("ROLLBACK", 0))
			run(fixture.Select("SELECT typname FROM pg_type WHERE typname='citext'", []string{"typname"}, []string{"NAME"}, [][]any{}))
			run(fixture.Exec("BEGIN", 0))
			citextInstall(t, p, b, c)
			run(fixture.Exec("COMMIT", 0))
			fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.CitextSelect(b, "SELECT 'Committed'::citext AS v", []string{"v"}, [][]any{{"Committed"}}))
		})
	})
	t.Run("qualified", func(t *testing.T) {
		fixture.SchemaReferenceCleanup(t, "citext_install_namespace")
		citextScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			for _, q := range []string{"CREATE SCHEMA citext_install_namespace", "CREATE EXTENSION citext SCHEMA citext_install_namespace VERSION '1.8'", "CREATE TABLE citext_qualified(id integer,v citext_install_namespace.citext)", "SET search_path=citext_install_namespace,public"} {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO public.citext_qualified VALUES (1,'Qualified'),(2,NULL)", 2))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.CitextSelect(b, "SELECT id,v FROM public.citext_qualified ORDER BY id", []string{"id", "v"}, [][]any{{int64(1), "Qualified"}, {int64(2), nil}}))
		})
	})
	t.Run("type_conflict", func(t *testing.T) {
		citextScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("CREATE TYPE citext AS ENUM ('occupied')", 0))
			run(fixture.Exec("CREATE TABLE citext_sentinel(id integer)", 0))
			run(fixture.Exec("INSERT INTO citext_sentinel VALUES (7)", 1))
			want := citextDiagnostic("42710", `type "citext" already exists`)
			want.Context = "SQL statement \"CREATE TYPE citext\"\nextension script file \"citext--1.4.sql\", near line 17"
			citextError(t, p, b, c, "CREATE EXTENSION citext", false, false, false, want)
			run(fixture.Select("SELECT extname FROM pg_extension WHERE extname='citext'", []string{"extname"}, []string{"NAME"}, [][]any{}))
			run(fixture.Select("SELECT typname,typtype FROM pg_type WHERE typnamespace='public'::regnamespace AND typname IN ('citext','_citext') ORDER BY typname", []string{"typname", "typtype"}, []string{"NAME", "CHAR"}, [][]any{{"_citext", "b"}, {"citext", "e"}}))
			run(fixture.Select("SELECT proname FROM pg_proc WHERE pronamespace='public'::regnamespace AND proname IN ('citextin','citextout') ORDER BY proname", []string{"proname"}, []string{"NAME"}, [][]any{}))
			run(fixture.Select("SELECT id FROM citext_sentinel", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
		})
	})
	t.Run("trusted_authority", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"citext_installer"})
		citextScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("CREATE USER citext_installer NOSUPERUSER", 0))
			run(fixture.Exec("GRANT USAGE ON SCHEMA public TO citext_installer", 0))
			var database string
			if err := c.QueryRowContext(p.Ctx, "SELECT current_database()").Scan(&database); err != nil {
				t.Fatal(err)
			}
			target := citextIdentifier(database)
			run(fixture.Exec("REVOKE CREATE ON DATABASE "+target+" FROM PUBLIC", 0))
			defer c.ExecContext(p.Ctx, "GRANT CREATE ON DATABASE "+target+" TO PUBLIC")
			run(fixture.Exec("SET SESSION AUTHORIZATION citext_installer", 0))
			defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
			want := citextDiagnostic("42501", `permission denied to create extension "citext"`)
			want.Hint = "Must have CREATE privilege on current database to create this extension."
			citextError(t, p, b, c, "CREATE EXTENSION citext SCHEMA public", false, false, false, want)
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			run(fixture.Exec("GRANT CREATE ON DATABASE "+target+" TO citext_installer", 0))
			run(fixture.Exec("SET SESSION AUTHORIZATION citext_installer", 0))
			run(fixture.Exec("CREATE EXTENSION citext SCHEMA public", 0))
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			run(fixture.Select("SELECT r.rolname FROM pg_extension e JOIN pg_roles r ON r.oid=e.extowner WHERE e.extname='citext'", []string{"rolname"}, []string{"NAME"}, [][]any{{"citext_installer"}}))
			run(fixture.Select("SELECT t.typowner::bigint,p.proowner::bigint FROM pg_type t JOIN pg_proc p ON p.oid=t.typinput WHERE t.oid='citext'::regtype::oid", []string{"typowner", "proowner"}, []string{"INT8", "INT8"}, [][]any{{int64(10), int64(10)}}))
			run(fixture.Exec("REVOKE CREATE ON DATABASE "+target+" FROM citext_installer", 0))
		})
	})
	t.Run("input_signature_conflict", func(t *testing.T) {
		citextScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			// LANGUAGE internal is a PostgreSQL ABI setup, not a native SQL
			// loader. Native genuine Stage signature conflicts belong to units.
			if !b.PG {
				return
			}
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("CREATE FUNCTION citextin(cstring) RETURNS text LANGUAGE internal IMMUTABLE STRICT AS 'textin'", 0))
			want := citextDiagnostic("42723", `function "citextin" already exists with same argument types`)
			want.Context = "SQL statement \"CREATE FUNCTION citextin(cstring)\nRETURNS citext\nAS 'textin'\nLANGUAGE internal IMMUTABLE STRICT PARALLEL SAFE\"\nextension script file \"citext--1.4.sql\", near line 22"
			citextError(t, p, b, c, "CREATE EXTENSION citext", false, false, false, want)
			run(fixture.Select("SELECT extname FROM pg_extension WHERE extname='citext'", []string{"extname"}, []string{"NAME"}, [][]any{}))
			run(fixture.Select("SELECT typname FROM pg_type WHERE typnamespace='public'::regnamespace AND typname IN ('citext','_citext')", []string{"typname"}, []string{"NAME"}, [][]any{}))
			run(fixture.Select("SELECT proname FROM pg_proc WHERE pronamespace='public'::regnamespace AND proname IN ('citextin','citextout') ORDER BY proname", []string{"proname"}, []string{"NAME"}, [][]any{{"citextin"}}))
		})
	})
}
