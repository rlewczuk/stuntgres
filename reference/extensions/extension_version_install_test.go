package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceExtensionVersionInstallAndUpdate(t *testing.T) {
	for _, target := range []string{"TO '1.2'", "TO \"1.2\"", ""} {
		t.Run("Update"+target, func(t *testing.T) {
			extensionVersionCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				extensionVersionAvailability(t, p, b, c, nil)
				run(fixture.Exec("CREATE SCHEMA ev_addon", 0))
				run(fixture.Exec("CREATE EXTENSION fuzzystrmatch WITH SCHEMA ev_addon VERSION '1.1'", 0))
				run(fixture.Exec("SET search_path TO ev_addon,public", 0))
				before := extensionVersionInventory(t, p, b, c, "1.1")
				extensionVersionBoth(t, p, b, c, extensionVersionBase())
				extensionVersionMissing(t, p, b, c)
				extensionVersionAvailability(t, p, b, c, "1.1")
				base := extensionVersionBase()
				heldBase := extensionVersionHeld(t, p, c, base.Query)
				q := "SELECT extversion FROM pg_extension WHERE extname='fuzzystrmatch'"
				heldCatalog := extensionVersionHeld(t, p, c, q)
				aq := "SELECT version,installed FROM pg_available_extension_versions WHERE name='fuzzystrmatch' ORDER BY version"
				heldAvailability := extensionVersionHeld(t, p, c, aq)
				update := fixture.Exec("ALTER EXTENSION fuzzystrmatch UPDATE "+target, 0)
				if target == `TO "1.2"` {
					update = fixture.Prepared(update)
				}
				run(update)
				after := extensionVersionInventory(t, p, b, c, "1.2")
				extensionVersionPreserved(t, before, after)
				fixture.RunStep(t, p.Ctx, heldBase, b.Recorder, base)
				fixture.RunStep(t, p.Ctx, heldCatalog, b.Recorder, fixture.Select(q, []string{"extversion"}, []string{"TEXT"}, [][]any{{"1.2"}}))
				fixture.RunStep(t, p.Ctx, heldAvailability, b.Recorder, fixture.Select(aq, []string{"version", "installed"}, []string{"TEXT", "BOOL"}, [][]any{{"1.1", false}, {"1.2", true}}))
				extensionVersionDM(t, p, b, c)
				extensionVersionAvailability(t, p, b, c, "1.2")
			})
		})
	}
	for _, options := range []string{"VERSION '1.1'", "VERSION \"1.1\"", "VERSION '1.2'", "VERSION \"1.2\"", ""} {
		t.Run("Create"+options, func(t *testing.T) {
			extensionVersionCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				// Prepared utility description must not install anything.
				stmt, err := c.PrepareContext(p.Ctx, "CREATE EXTENSION fuzzystrmatch "+options)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				daitchCount(t, p, b, c, 0)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE EXTENSION fuzzystrmatch "+options, 0))
				version := "1.2"
				if options == "VERSION '1.1'" || options == "VERSION \"1.1\"" {
					version = "1.1"
				}
				extensionVersionInventory(t, p, b, c, version)
				extensionVersionBoth(t, p, b, c, extensionVersionBase())
				if version == "1.1" {
					extensionVersionMissing(t, p, b, c)
				} else {
					extensionVersionDM(t, p, b, c)
				}
			})
		})
	}
	t.Run("ExistingExecuteACL", func(t *testing.T) {
		extensionVersionCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("CREATE USER ev_other NOSUPERUSER", 0))
			run(fixture.Exec("GRANT USAGE ON SCHEMA public TO ev_other", 0))
			run(fixture.Exec("CREATE EXTENSION fuzzystrmatch VERSION '1.1'", 0))
			reader, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			fixture.RunStep(t, p.Ctx, reader, b.Recorder, fixture.Exec("SET SESSION AUTHORIZATION ev_other", 0))
			defer reader.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
			q := "SELECT public.levenshtein($1::text,'sitting'::text)"
			held := extensionVersionHeld(t, p, reader, q)
			run(fixture.Exec("REVOKE EXECUTE ON FUNCTION public.levenshtein(text,text) FROM PUBLIC", 0))
			before := extensionVersionInventory(t, p, b, c, "1.1")
			for _, updated := range []bool{false, true} {
				if updated {
					run(fixture.Exec("ALTER EXTENSION fuzzystrmatch UPDATE TO '1.2'", 0))
					extensionVersionPreserved(t, before, extensionVersionInventory(t, p, b, c, "1.2"))
				}
				denied := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for function levenshtein"}, "kitten")
				denied.SelectRows = true
				extensionVersionBoth(t, p, b, reader, denied)
				fixture.RunStep(t, p.Ctx, held, b.Recorder, denied)
				extensionVersionBoth(t, p, b, reader, metaphoneSelect("SELECT public.soundex('John')", "soundex", "J500"))
			}
			run(fixture.Exec("GRANT EXECUTE ON FUNCTION public.levenshtein(text,text) TO ev_other", 0))
			check := fixture.Select(q, []string{"levenshtein"}, []string{"INT4"}, [][]any{{int64(3)}}, "kitten")
			extensionVersionBoth(t, p, b, reader, check)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, check)
		})
	})
}

func TestReferenceExtensionVersionFeatureSmoke(t *testing.T) {
	extensionVersionCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE EXTENSION fuzzystrmatch VERSION '1.1'", 0))
		before := extensionVersionInventory(t, p, b, c, "1.1")
		extensionVersionMissing(t, p, b, c)
		base := extensionVersionBase()
		held := extensionVersionHeld(t, p, c, base.Query)
		run(fixture.Exec("ALTER EXTENSION fuzzystrmatch UPDATE TO '1.2'", 0))
		extensionVersionPreserved(t, before, extensionVersionInventory(t, p, b, c, "1.2"))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, base)
		extensionVersionDM(t, p, b, c)
		extensionVersionAvailability(t, p, b, c, "1.2")
		extensionVersionBoth(t, p, b, c, extensionVersionPaths("SELECT source,target,path FROM pg_extension_update_paths('fuzzystrmatch') ORDER BY source,target", extensionVersionPathRows()))
		extensionVersionBoth(t, p, b, c, extensionError("ALTER EXTENSION fuzzystrmatch UPDATE TO '1.1'", "22023", `extension "fuzzystrmatch" has no update path from version "1.2" to version "1.1"`))
		extensionVersionDM(t, p, b, c)
		run(fixture.Exec("CREATE TABLE ev_smoke(v text)", 0))
		run(fixture.Exec("INSERT INTO ev_smoke VALUES('John'),(NULL)", 2))
		run(fixture.Exec("CREATE VIEW ev_smoke_codes AS SELECT v,daitch_mokotoff(v) AS code FROM ev_smoke", 0))
		check := fixture.ArrayReferenceSelect("SELECT v,code FROM ev_smoke_codes ORDER BY v", []string{"v", "code"}, []string{"TEXT", "_TEXT"}, []string{"TEXT", "_TEXT"}, [][]any{{"John", "{160000,460000}"}, {nil, nil}}, b.PG)
		extensionVersionBoth(t, p, b, c, check)
		if !b.PG {
			extensionVersionRoundTrip(t, p, b, c, "1.2", check)
		}
	})
}
