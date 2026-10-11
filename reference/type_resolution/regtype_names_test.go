package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRegtypeNames(t *testing.T) {
	fixture.RegtypeScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, conn *sql.Conn, recorder *fixture.Recorder, notices *[]fixture.Diagnostic) {
		fixture.RegtypeSetup(t, p, conn, recorder)
		fixture.RegtypeBoth(t, p, conn, recorder, fixture.RegtypeSelect("SELECT 'same'::regtype AS a,'regtype_b.same'::regtype AS b,'regtype_a.mood'::regtype AS c", "same", "regtype_b.same", "mood"))
		fixture.RegtypeBoth(t, p, conn, recorder, fixture.Select("SELECT ('same'::regtype::oid='regtype_a.same'::regtype::oid) AS a,('regtype_a.same'::regtype::oid='regtype_b.same'::regtype::oid) AS b", []string{"a", "b"}, []string{"BOOL", "BOOL"}, [][]any{{true, false}}))
		fixture.RegtypeBoth(t, p, conn, recorder, fixture.RegtypeSelect(`SELECT '"Odd.Regtype"."a""b.dot"'::regtype AS a,'regtype_a."Select"'::regtype AS b,'regtype_a."23"'::regtype AS c,'regtype_a."-"'::regtype AS d`, `"Odd.Regtype"."a""b.dot"`, `"Select"`, `"23"`, `"-"`))
		fixture.RegtypeBoth(t, p, conn, recorder, fixture.RegtypeSelect("SELECT ' regtype_a . SAME '::regtype AS a,'regtype_a.same[]'::regtype AS b,'regtype_a._same'::regtype AS c,'regtype_a.mood[]'::regtype AS d", "same", "same[]", "same[]", "mood[]"))
		fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("SET search_path TO regtype_b,regtype_a,public", 0))
		fixture.RegtypeBoth(t, p, conn, recorder, fixture.RegtypeSelect("SELECT 'same'::regtype AS a,'regtype_a.same'::regtype AS b", "same", "regtype_a.same"))
		fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("SET search_path TO pg_catalog", 0))
		fixture.RegtypeBoth(t, p, conn, recorder, fixture.RegtypeSelect("SELECT 'regtype_a.same'::regtype AS a,'regtype_b.same'::regtype AS b", "regtype_a.same", "regtype_b.same"))
		fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec(`SET search_path TO "Odd.Regtype",pg_catalog`, 0))
		fixture.RegtypeBoth(t, p, conn, recorder, fixture.RegtypeSelect(`SELECT '"Odd.Regtype"."a""b.dot"'::regtype AS a`, `"a""b.dot"`))
		for _, name := range []string{"int4", "text", "integer", "numeric", "interval", "json", "bit"} {
			fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("CREATE DOMAIN regtype_a."+fixture.RegtypeIdentifier(name)+" AS pg_catalog.text", 0))
		}
		fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("SET search_path TO regtype_a,pg_catalog,public", 0))
		fixture.RegtypeBoth(t, p, conn, recorder, fixture.RegtypeSelect("SELECT 'int4'::regtype AS a,'integer'::regtype AS b,'pg_catalog.int4'::regtype AS c,'text'::regtype AS d,'pg_catalog.text'::regtype AS e", "int4", "integer", "integer", "text", "pg_catalog.text"))
		fixture.RegtypeBoth(t, p, conn, recorder, fixture.RegtypeSelect(`SELECT '"integer"'::regtype AS a,'regtype_a."integer"'::regtype AS b,'pg_catalog.numeric'::regtype AS c,'pg_catalog.interval'::regtype AS d,'pg_catalog.json'::regtype AS e,'pg_catalog.bit'::regtype AS f`, `"integer"`, `"integer"`, "numeric", "interval", "json", "bit"))
		// Identifier clipping is input semantics, not stored value truncation.
		long := "regtype_identifier_abcdefghijklmnopqrstuvwxyz_abcdefghijklmnopqrstuvwxyz"
		clipped := long[:63]
		fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("CREATE DOMAIN regtype_a."+fixture.RegtypeIdentifier(clipped)+" AS pg_catalog.int4", 0))
		s := fixture.RegtypeSelect("SELECT $1::regtype AS a", clipped)
		s.Args = []any{"regtype_a." + long}
		fixture.RegtypeBoth(t, p, conn, recorder, s)
		unicodeLong := strings.Repeat("é", 32)
		unicodeClipped := strings.Repeat("é", 31)
		fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("CREATE DOMAIN regtype_a."+fixture.RegtypeIdentifier(unicodeClipped)+" AS pg_catalog.int4", 0))
		unicodeStep := fixture.RegtypeSelect("SELECT $1::regtype AS a", fixture.RegtypeIdentifier(unicodeClipped))
		unicodeStep.Args = []any{"regtype_a." + fixture.RegtypeIdentifier(unicodeLong)}
		fixture.RegtypeBoth(t, p, conn, recorder, unicodeStep)
		clipping := func(input, source, clipped string) fixture.Diagnostic {
			return fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42622", Message: `identifier "` + source + `" will be truncated to "` + clipped + `"`, Context: `invalid type name "` + input + "\"\nunnamed portal parameter $1 = '...'"}
		}
		asciiNotice := clipping("regtype_a."+long, long, clipped)
		unicodeNotice := clipping("regtype_a."+fixture.RegtypeIdentifier(unicodeLong), unicodeLong, unicodeClipped)
		fixture.RegtypeAssertNotices(t, *notices, []fixture.Diagnostic{asciiNotice, asciiNotice, unicodeNotice, unicodeNotice})
	})
}

func TestReferenceRegtypeUserAndTemporaryPath(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"regtype_reader"})
	fixture.RegtypeScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, conn *sql.Conn, recorder *fixture.Recorder, notices *[]fixture.Diagnostic) {
		for _, q := range []string{"CREATE ROLE regtype_reader", "CREATE SCHEMA regtype_reader AUTHORIZATION regtype_reader", "SET ROLE regtype_reader", "CREATE DOMAIN regtype_reader.mine AS integer", `SET search_path TO "$user",public`} {
			fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec(q, 0))
		}
		fixture.RegtypeBoth(t, p, conn, recorder, fixture.RegtypeSelect("SELECT 'mine'::regtype AS a,'regtype_reader.mine[]'::regtype AS b", "mine", "mine[]"))
		fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("RESET ROLE", 0))
		// Establish own temporary namespace using the existing supported sequence API,
		// not an unauthorized expansion of temporary table support.
		fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("CREATE TEMP SEQUENCE regtype_temp_sequence", 0))
		fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("SET search_path TO pg_temp", 0))
		var namespace string
		if err := conn.QueryRowContext(p.Ctx, "SELECT current_schema").Scan(&namespace); err != nil || !strings.HasPrefix(namespace, "pg_temp_") {
			t.Fatal("initialized temporary namespace", namespace, err)
		}
		if !backend.PG {
			// TEMP type DDL is an existing adjacent capability boundary; the
			// selected resolver does not enable that creator.
			q := "CREATE DOMAIN " + fixture.RegtypeIdentifier(namespace) + ".regtype_temp_domain AS integer"
			fixture.RunStep(t, p.Ctx, conn, recorder, fixture.ImplicitError(q,
				fixture.ControlsReferenceDiagnostic("0A000", "temporary schema creation is unsupported")))
			return
		}
		fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("CREATE DOMAIN "+fixture.RegtypeIdentifier(namespace)+".regtype_temp_domain AS integer", 0))
		fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("SET search_path TO public", 0))
		fixture.RegtypeBoth(t, p, conn, recorder, fixture.RegtypeSelect("SELECT 'regtype_temp_domain'::regtype AS a,'pg_temp.regtype_temp_domain[]'::regtype AS b", "regtype_temp_domain", "regtype_temp_domain[]"))
		fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("DROP DOMAIN pg_temp.regtype_temp_domain", 0))
	})
}
