package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

// Migrates the portable special-path assertions from the mixed private
// SessionDefaultProvider unit; that unit retains provider capture and parsing.
func TestReferenceNamespaceIdentitySpecialPaths(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "s1", "s2", "s1,")
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			a, peer := namespaceConn(t, p, b), namespaceConn(t, p, b)
			namespaceExec(t, p, b, a, "CREATE SCHEMA s1 CREATE TABLE t(id INT)", "CREATE SCHEMA s2 CREATE TABLE t(id INT)")
			fixture.RunStep(t, p.Ctx, a, b.Recorder, fixture.Exec("INSERT INTO s1.t VALUES(7)", 1))
			fixture.RunStep(t, p.Ctx, a, b.Recorder, fixture.Exec("INSERT INTO s2.t VALUES(9)", 1))
			namespaceExec(t, p, b, a, "SET search_path TO s1,s2")
			namespaceExec(t, p, b, peer, "SET search_path TO s2,s1")
			namespaceValue(t, p, b, a, "SELECT id FROM t", "id", "INT4", int64(7))
			namespaceValue(t, p, b, peer, "SELECT id FROM t", "id", "INT4", int64(9))
			namespaceValue(t, p, b, a, "SELECT current_schema()", "current_schema", "NAME", "s1")
			namespaceExec(t, p, b, a, "SET search_path TO missing,s2,s2,s1")
			namespaceValue(t, p, b, a, "SELECT id FROM t", "id", "INT4", int64(9))
			namespaceValue(t, p, b, peer, "SELECT current_schema", "current_schema", "NAME", "s2")
			namespaceExec(t, p, b, a, "SET search_path TO ''")
			namespaceValue(t, p, b, a, "SHOW search_path", "search_path", "TEXT", `""`)
			namespaceValue(t, p, b, a, "SELECT current_schema", "current_schema", "NAME", nil)
			missing := fixture.ImplicitError("CREATE TABLE no_destination(id INT)", fixture.Diagnostic{Code: "3F000", Message: "no schema has been selected to create in", Position: "14"})
			fixture.RunStep(t, p.Ctx, a, b.Recorder, missing)
			invalid := fixture.ControlsReferenceDiagnostic("22023", `invalid value for parameter "search_path": "s1,"`)
			invalid.Detail = "List syntax is invalid."
			fixture.RunStep(t, p.Ctx, a, b.Recorder, fixture.ErrorDiagnostic("SELECT set_config('search_path','s1,',false)", invalid))
			namespaceValue(t, p, b, a, "SHOW search_path", "search_path", "TEXT", `""`)
			namespaceExec(t, p, b, a, "SET search_path TO 's1,'")
			namespaceValue(t, p, b, a, "SHOW search_path", "search_path", "TEXT", `"s1,"`)
			namespaceValue(t, p, b, a, "SELECT current_schema", "current_schema", "NAME", nil)
			namespaceValue(t, p, b, a, "SELECT set_config('search_path','',false)", "set_config", "TEXT", "")
			namespaceValue(t, p, b, a, "SHOW search_path", "search_path", "TEXT", "")
			namespaceValue(t, p, b, a, "SELECT current_schema", "current_schema", "NAME", nil)
			namespaceExec(t, p, b, a, `CREATE SCHEMA "s1," CREATE TABLE t(id INT)`)
			fixture.RunStep(t, p.Ctx, a, b.Recorder, fixture.Exec(`INSERT INTO "s1,".t VALUES(11)`, 1))
			namespaceExec(t, p, b, a, "SET search_path TO 's1,'")
			namespaceValue(t, p, b, a, "SHOW search_path", "search_path", "TEXT", `"s1,"`)
			namespaceValue(t, p, b, a, "SELECT id FROM t", "id", "INT4", int64(11))
			namespaceValue(t, p, b, a, "SELECT id FROM s1.t", "id", "INT4", int64(7))
			namespaceExec(t, p, b, a, `SET search_path TO "lowercase","A.B",public`)
			namespaceValue(t, p, b, a, "SHOW search_path", "search_path", "TEXT", `lowercase, "A.B", public`)
			namespaceValue(t, p, b, a, "SELECT current_schema", "current_schema", "NAME", "public")
			for _, raw := range []string{`""`, `"", s2`} {
				namespaceValue(t, p, b, a, "SELECT set_config('search_path',$1,false)", "set_config", "TEXT", raw, raw)
				namespaceValue(t, p, b, a, "SHOW search_path", "search_path", "TEXT", raw)
				var want any
				if raw != `""` {
					want = "s2"
				}
				namespaceValue(t, p, b, a, "SELECT current_schema", "current_schema", "NAME", want)
				if want != nil {
					namespaceValue(t, p, b, a, "SELECT id FROM t", "id", "INT4", int64(9))
				}
			}
			namespaceExec(t, p, b, a, "SET search_path TO '',s2")
			namespaceValue(t, p, b, a, "SHOW search_path", "search_path", "TEXT", `"", s2`)
			namespaceValue(t, p, b, a, "SELECT id FROM t", "id", "INT4", int64(9))
			namespaceExec(t, p, b, a, "SET search_path TO s2")
			fixture.RunStep(t, p.Ctx, a, b.Recorder, fixture.ErrorDiagnostic("SELECT set_config('search_path','s1,',false)", invalid))
			namespaceValue(t, p, b, a, "SHOW search_path", "search_path", "TEXT", "s2")
			namespaceValue(t, p, b, a, "SELECT current_setting('search_path')", "current_setting", "TEXT", "s2")
			namespaceValue(t, p, b, a, "SELECT current_schema", "current_schema", "NAME", "s2")
			namespaceValue(t, p, b, a, "SELECT id FROM t", "id", "INT4", int64(9))
			namespaceValue(t, p, b, peer, "SHOW search_path", "search_path", "TEXT", "s2, s1")
			namespaceValue(t, p, b, peer, "SELECT id FROM t", "id", "INT4", int64(9))
			namespaceExec(t, p, b, a, "RESET search_path", "RESET ALL", "SET search_path TO s1,pg_temp,public", "CREATE TABLE earlier(id INT)")
			namespaceValue(t, p, b, a, "SELECT relnamespace FROM pg_class WHERE relname='earlier'", "relnamespace", "OID", func() int64 {
				var id int64
				if err := a.QueryRowContext(p.Ctx, "SELECT oid FROM pg_namespace WHERE nspname='s1'").Scan(&id); err != nil {
					t.Fatal(err)
				}
				return id
			}())
		})
	}
}
