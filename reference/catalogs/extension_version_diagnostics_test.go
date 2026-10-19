package reference_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceExtensionVersionDiagnosticsAndConflict(t *testing.T) {
	t.Run("AbsentAndInstall", func(t *testing.T) {
		extensionVersionCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			for _, suffix := range []string{"", " TO ''", " TO '--'"} {
				extensionVersionBoth(t, p, b, c, extensionError("ALTER EXTENSION fuzzystrmatch UPDATE"+suffix, "42704", `extension "fuzzystrmatch" does not exist`))
			}
			for _, version := range []string{"1.0", "missing"} {
				extensionVersionBoth(t, p, b, c, extensionError("CREATE EXTENSION fuzzystrmatch VERSION '"+version+"'", "22023", fmt.Sprintf(`extension "fuzzystrmatch" has no installation script nor update path for version "%s"`, version)))
				daitchCount(t, p, b, c, 0)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT count(*) FROM pg_proc WHERE proname='daitch_mokotoff'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
			}
			for _, tc := range extensionVersionInvalidNames() {
				q := "CREATE EXTENSION fuzzystrmatch SCHEMA ev_missing VERSION " + fixture.ArrayReferenceQuote(tc.version)
				extensionVersionBoth(t, p, b, c, fixture.ImplicitError(q, fixture.Diagnostic{Code: "22023", Message: fmt.Sprintf(`invalid extension version name: "%s"`, tc.version), Detail: tc.detail}))
				daitchCount(t, p, b, c, 0)
			}
		})
	})
	t.Run("Installed", func(t *testing.T) {
		extensionVersionCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("CREATE EXTENSION fuzzystrmatch VERSION '1.1'", 0))
			before := extensionVersionInventory(t, p, b, c, "1.1")
			for _, tc := range extensionVersionInvalidNames() {
				q := "ALTER EXTENSION fuzzystrmatch UPDATE TO " + fixture.ArrayReferenceQuote(tc.version)
				extensionVersionBoth(t, p, b, c, fixture.ImplicitError(q, fixture.Diagnostic{Code: "22023", Message: fmt.Sprintf(`invalid extension version name: "%s"`, tc.version), Detail: tc.detail}))
				extensionVersionPreserved(t, before, extensionVersionInventory(t, p, b, c, "1.1"))
				extensionVersionBoth(t, p, b, c, extensionVersionBase())
			}
			for _, version := range []string{"missing", "1.0"} {
				extensionVersionBoth(t, p, b, c, extensionError("ALTER EXTENSION fuzzystrmatch UPDATE TO '"+version+"'", "22023", fmt.Sprintf(`extension "fuzzystrmatch" has no update path from version "1.1" to version "%s"`, version)))
				extensionVersionPreserved(t, before, extensionVersionInventory(t, p, b, c, "1.1"))
				extensionVersionMissing(t, p, b, c)
			}
			for _, tc := range []struct{ q, token, message string }{
				{"ALTER EXTENSION fuzzystrmatch UPDATE TO '1.2' TO '1.1'", "TO '1.1'", "conflicting or redundant options"},
				{"ALTER EXTENSION fuzzystrmatch UPDATE TO '1.2' junk", "junk", `syntax error at or near "junk"`},
			} {
				s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: "42601", Message: tc.message, Position: fixture.Position(strings.LastIndex(tc.q, tc.token) + 1)})
				extensionVersionBoth(t, p, b, c, s)
				extensionVersionPreserved(t, before, extensionVersionInventory(t, p, b, c, "1.1"))
			}
			run(fixture.Exec("ALTER EXTENSION fuzzystrmatch UPDATE TO '1.2'", 0))
			after := extensionVersionInventory(t, p, b, c, "1.2")
			extensionVersionBoth(t, p, b, c, extensionError("ALTER EXTENSION fuzzystrmatch UPDATE TO '1.1'", "22023", `extension "fuzzystrmatch" has no update path from version "1.2" to version "1.1"`))
			extensionVersionPreserved(t, after, extensionVersionInventory(t, p, b, c, "1.2"))
			var notices []fixture.Diagnostic
			_, noticeConn, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
			for _, q := range []string{"ALTER EXTENSION fuzzystrmatch UPDATE TO '1.2'", "ALTER EXTENSION fuzzystrmatch UPDATE"} {
				fixture.RunStep(t, p.Ctx, noticeConn, recorder, fixture.Exec(q, 0))
			}
			want := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `version "1.2" of extension "fuzzystrmatch" is already installed`}
			if !reflect.DeepEqual(notices, []fixture.Diagnostic{want, want}) {
				t.Fatal("same-version notices", notices)
			}
			extensionVersionPreserved(t, after, extensionVersionInventory(t, p, b, c, "1.2"))
			extensionVersionDM(t, p, b, c)
		})
	})
	t.Run("PathErrors", func(t *testing.T) {
		extensionVersionCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			for _, name := range []string{"ev_missing_control", "", "bad/name", "bad--name", "-bad"} {
				want := fixture.Diagnostic{Code: "0A000", Message: fmt.Sprintf(`extension "%s" is not available`, name)}
				if name == "" || name == "bad/name" || name == "bad--name" || name == "-bad" {
					want.Code = "22023"
					want.Message = fmt.Sprintf(`invalid extension name: "%s"`, name)
					switch name {
					case "":
						want.Detail = "Extension names must not be empty."
					case "bad/name":
						want.Detail = "Extension names must not contain directory separator characters."
					case "bad--name":
						want.Detail = `Extension names must not contain "--".`
					case "-bad":
						want.Detail = `Extension names must not begin or end with "-".`
					}
				} else {
					// REL_18_6's failed control-file search emits no Detail;
					// do not synthesize a host pathname from older releases.
					want.Hint = "The extension must first be installed on the system where PostgreSQL is running."
				}
				s := fixture.ImplicitError("SELECT source,target,path FROM pg_extension_update_paths($1::name)", want, name)
				s.SelectRows = true
				extensionVersionBoth(t, p, b, c, s)
			}
		})
	})
	t.Run("RealLooseMemberConflict", func(t *testing.T) {
		extensionVersionCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE EXTENSION fuzzystrmatch VERSION '1.1'", 0))
			before := extensionVersionInventory(t, p, b, c, "1.1")
			if !b.PG {
				// No public native CREATE FUNCTION or fake fuzzy provider: the
				// genuine native competing-stage counterpart is a package unit.
				extensionVersionMissing(t, p, b, c)
				return
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE FUNCTION public.daitch_mokotoff(text) RETURNS text[] AS '$libdir/fuzzystrmatch','daitch_mokotoff' LANGUAGE C IMMUTABLE STRICT PARALLEL SAFE", 0))
			var original, owner int64
			if err := c.QueryRowContext(p.Ctx, "SELECT oid,proowner FROM pg_proc WHERE proname='daitch_mokotoff'").Scan(&original, &owner); err != nil {
				t.Fatal(err)
			}
			// Released script is source-grounded; pinned discovery must resolve
			// exact Context whitespace/near-line before production authorization.
			want := fixture.Diagnostic{Code: "42723", Message: `function "daitch_mokotoff" already exists with same argument types`, Context: "SQL statement \"CREATE FUNCTION daitch_mokotoff(text) RETURNS text[]\nAS '$libdir/fuzzystrmatch', 'daitch_mokotoff'\nLANGUAGE C IMMUTABLE STRICT PARALLEL SAFE\"\nextension script file \"fuzzystrmatch--1.1--1.2.sql\", near line 6"}
			extensionVersionBoth(t, p, b, c, fixture.ImplicitError("ALTER EXTENSION fuzzystrmatch UPDATE TO '1.2'", want))
			extensionVersionPreserved(t, before, extensionVersionInventory(t, p, b, c, "1.1"))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT oid::bigint,proowner::bigint FROM pg_proc WHERE proname='daitch_mokotoff'", []string{"oid", "proowner"}, []string{"INT8", "INT8"}, [][]any{{original, owner}}))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT count(*) FROM pg_depend WHERE classid=1255 AND objid=$1 AND refclassid=3079 AND deptype='e'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}, original))
			extensionVersionBoth(t, p, b, c, extensionVersionBase())
			extensionVersionDM(t, p, b, c)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DROP FUNCTION public.daitch_mokotoff(text)", 0))
			extensionVersionMissing(t, p, b, c)
		})
	})
}

func extensionVersionInvalidNames() []struct{ version, detail string } {
	return []struct{ version, detail string }{
		{"", "Version names must not be empty."},
		{"--", `Version names must not contain "--".`},
		{"-bad", `Version names must not begin or end with "-".`},
		{"bad-", `Version names must not begin or end with "-".`},
		{"bad/name", "Version names must not contain directory separator characters."},
		{"-bad/name", `Version names must not begin or end with "-".`},
		{"bad--/name", `Version names must not contain "--".`},
	}
}
