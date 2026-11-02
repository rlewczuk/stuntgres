package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func xml2NoticeExec(t *testing.T, p *fixture.Pair, b fixture.Backend, q string, prepared bool, want []fixture.Diagnostic) {
	t.Helper()
	var notices []fixture.Diagnostic
	_, c, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
	s := fixture.Exec(q, 0)
	s.Prepared = prepared
	fixture.RunStep(t, p.Ctx, c, recorder, s)
	if !reflect.DeepEqual(notices, want) {
		t.Fatalf("notices=%#v want=%#v", notices, want)
	}
}

func TestReferenceXML2Installation(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"xml2_probe_user"})
	fixture.SchemaReferenceCleanup(t, "xml2_probe_schema")
	xml2InstallationCollisions(t)
	xml2Case(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE ROLE xml2_probe_user", 0))
		// Recreated public schemas need explicit lookup authority for this role;
		// keep CREATE ungranted so the extension's superuser gate is isolated.
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("GRANT USAGE ON SCHEMA public TO xml2_probe_user", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE SCHEMA xml2_probe_schema", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET ROLE xml2_probe_user", 0))
		d := xml2Error("42501", "permission denied to create extension \"xml2\"")
		d.Hint = "Must be superuser to create this extension."
		xml2Failure(t, p, b, c, "CREATE EXTENSION xml2", d, false)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("RESET ROLE", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT extname FROM pg_extension WHERE extname='xml2'", []string{"extname"}, []string{"NAME"}, nil))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT p.proname FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='public' AND p.proname IN ("+xml2Names+")", []string{"proname"}, []string{"NAME"}, nil))
		xml2Install(t, p, b, c)
		xml2Inventory(t, p, b, c)
		before := xml2IDs(t, p, c)
		xml2Failure(t, p, b, c, "CREATE EXTENSION xml2", xml2Error("42710", "extension \"xml2\" already exists"), false)
		for _, prepared := range []bool{false, true} {
			xml2NoticeExec(t, p, b, "CREATE EXTENSION IF NOT EXISTS xml2", prepared, []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42710", Message: "extension \"xml2\" already exists, skipping"}})
		}
		if after := xml2IDs(t, p, c); !reflect.DeepEqual(before, after) {
			t.Fatalf("repeat install changed identities: %v -> %v", before, after)
		}
		xml2Failure(t, p, b, c, "ALTER EXTENSION xml2 SET SCHEMA xml2_probe_schema", xml2Error("0A000", "extension \"xml2\" does not support SET SCHEMA"), false)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE VIEW xml2_probe_view AS SELECT xpath_number('<r>12</r>','.') AS n", 0))
		xml2NoticeExec(t, p, b, "DROP EXTENSION xml2 CASCADE", false, []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: "drop cascades to view xml2_probe_view"}})
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE EXTENSION xml2 SCHEMA xml2_probe_schema", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET search_path TO xml2_probe_schema,public", 0))
		xml2Both(t, p, b, c, fixture.Select("SELECT xml_valid('<r/>') AS v,xpath_string('<r>12</r>','.') AS s,xpath_number('<r>12</r>','.') AS n,xpath_bool('<r/>','.') AS b",
			[]string{"v", "s", "n", "b"}, []string{"BOOL", "TEXT", "FLOAT4", "BOOL"}, [][]any{{true, "12", float64(12), true}}))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET ROLE xml2_probe_user", 0))
		d = xml2Error("42501", "permission denied for schema xml2_probe_schema")
		d.Position = "8"
		xml2Failure(t, p, b, c, "SELECT xml2_probe_schema.xpath_string('<r/>','.')", d, true)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("RESET ROLE", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("GRANT USAGE ON SCHEMA xml2_probe_schema TO xml2_probe_user", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET ROLE xml2_probe_user", 0))
		xml2Both(t, p, b, c, fixture.Select("SELECT xml2_probe_schema.xpath_string('<r>12</r>','.') AS s", []string{"s"}, []string{"TEXT"}, [][]any{{"12"}}))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("RESET ROLE", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET search_path TO public", 0))
	})
}
