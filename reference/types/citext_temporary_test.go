package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func citextIdentifier(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }

func TestReferenceCitextTemporary(t *testing.T) {
	t.Run("alias", func(t *testing.T) {
		citextScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TEMP TABLE citext_temp_alias(id integer)", 0))
			citextError(t, p, b, c, "CREATE EXTENSION citext SCHEMA pg_temp", false, false, false, citextDiagnostic("3F000", `schema "pg_temp" does not exist`))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT extname FROM pg_extension WHERE extname='citext'", []string{"extname"}, []string{"NAME"}, [][]any{}))
		})
	})
	t.Run("own_physical", func(t *testing.T) {
		citextScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TEMP TABLE citext_temp_own_seed(id integer)", 0))
			var name string
			if err := c.QueryRowContext(p.Ctx, "SELECT schemaname FROM pg_tables WHERE tablename='citext_temp_own_seed'").Scan(&name); err != nil {
				t.Fatal(err)
			}
			want := citextDiagnostic("42883", "function citextin(cstring) does not exist")
			want.Context = "SQL statement \"CREATE TYPE citext (\n    INPUT          = citextin,\n    OUTPUT         = citextout,\n    RECEIVE        = citextrecv,\n    SEND           = citextsend,\n    INTERNALLENGTH = VARIABLE,\n    STORAGE        = extended,\n    -- make it a non-preferred member of string type category\n    CATEGORY       = 'S',\n    PREFERRED      = false,\n    COLLATABLE     = true\n)\"\nextension script file \"citext--1.4.sql\", near line 46"
			citextError(t, p, b, c, "CREATE EXTENSION citext SCHEMA "+citextIdentifier(name), false, false, false, want)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT extname FROM pg_extension WHERE extname='citext'", []string{"extname"}, []string{"NAME"}, [][]any{}))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT typname FROM pg_type WHERE typnamespace=$1::regnamespace AND typname IN ('citext','_citext')", []string{"typname"}, []string{"NAME"}, [][]any{}, name))
		})
	})
	t.Run("foreign_owner_lifetime", func(t *testing.T) {
		citextScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, peer *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, peer, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE citext_temp_permanent(id integer)", 0))
			run(fixture.Exec("INSERT INTO citext_temp_permanent VALUES (73)", 1))
			var notices []fixture.Diagnostic
			ownerDB, owner, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
			defer ownerDB.Close()
			defer owner.Close()
			fixture.RunStep(t, p.Ctx, owner, recorder, fixture.Exec("CREATE TEMP TABLE citext_temp_foreign_seed(id integer)", 0))
			fixture.RunStep(t, p.Ctx, owner, recorder, fixture.Select("SELECT pg_advisory_lock(30791801)", []string{"pg_advisory_lock"}, []string{"VOID"}, [][]any{{""}}))
			var name string
			if err := owner.QueryRowContext(p.Ctx, "SELECT schemaname FROM pg_tables WHERE tablename='citext_temp_foreign_seed'").Scan(&name); err != nil {
				t.Fatal(err)
			}
			install := "CREATE EXTENSION citext SCHEMA " + citextIdentifier(name)
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec(install, 0))
			run(fixture.Exec("ROLLBACK", 0))
			run(fixture.Select("SELECT extname FROM pg_extension WHERE extname='citext'", []string{"extname"}, []string{"NAME"}, [][]any{}))
			var installerNotices []fixture.Diagnostic
			installerDB, installer, ir := fixture.NoticeReferencePool(t, p, b.PG, &installerNotices)
			fixture.RunStep(t, p.Ctx, installer, ir, fixture.Exec(install, 0))
			if err := installer.Close(); err != nil {
				t.Fatal(err)
			}
			if err := installerDB.Close(); err != nil {
				t.Fatal(err)
			}
			run(fixture.Select("SELECT extname FROM pg_extension WHERE extname='citext'", []string{"extname"}, []string{"NAME"}, [][]any{{"citext"}}))
			s := fixture.CitextSelect(b, "SELECT 'TeMp'::"+citextIdentifier(name)+".citext AS v", []string{"v"}, [][]any{{"TeMp"}})
			run(s)
			if !b.PG {
				var output bytes.Buffer
				err := stuntgres.Dump(p.Ctx, p.Native, &output)
				if err == nil || err.Error() != "stuntgres: temporary extension installation is not portable" || output.Len() != 0 {
					t.Fatalf("TEMP Dump %v output %q", err, output.String())
				}
			}
			if err := owner.Close(); err != nil {
				t.Fatal(err)
			}
			if err := ownerDB.Close(); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(p.Ctx, 5*time.Second)
			defer cancel()
			fixture.RunStep(t, ctx, peer, b.Recorder, fixture.Select("SELECT pg_advisory_lock(30791801)", []string{"pg_advisory_lock"}, []string{"VOID"}, [][]any{{""}}))
			run(fixture.Select("SELECT pg_advisory_unlock(30791801)", []string{"pg_advisory_unlock"}, []string{"BOOL"}, [][]any{{true}}))
			run(fixture.Select("SELECT extname FROM pg_extension WHERE extname='citext'", []string{"extname"}, []string{"NAME"}, [][]any{}))
			run(fixture.Select("SELECT t.typname FROM pg_type t JOIN pg_namespace n ON n.oid=t.typnamespace WHERE n.nspname=$1 AND t.typname IN ('citext','_citext')", []string{"typname"}, []string{"NAME"}, [][]any{}, name))
			run(fixture.Select("SELECT id FROM citext_temp_permanent", []string{"id"}, []string{"INT4"}, [][]any{{int64(73)}}))
		})
	})
}
