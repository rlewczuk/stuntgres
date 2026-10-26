package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceAccessNamespacesSmoke(t *testing.T) {
	objectGrantScenario(t, []string{"an_private"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		defer accessExecutionResetConnection(t, c)
		objects := accessNamespacesSetup(t, p, b, c, run)
		accessNamespacesOverloads(t, p, b, c, run, objects)
		run(objectGrantAccessValue("SELECT has_schema_privilege('og_object_reader','an_private','USAGE') AS v", true))
		run(objectGrantAccessValue("SELECT has_schema_privilege('og_object_other','an_private','USAGE') AS v", false))
		run(objectGrantAccessValue("SELECT has_database_privilege('og_object_reader',$1::oid,'CREATE,CONNECT') AS v", true, objects[0].oid))
		run(fixture.Exec("GRANT TEMP ON "+objects[0].target+" TO og_object_reader", 0))
		for _, token := range []string{"TEMP", "TEMPORARY"} {
			run(objectGrantAccessValue("SELECT has_database_privilege('og_object_reader',$1::oid,$2::text) AS v", true, objects[0].oid, token))
		}
		run(objectGrantAccessValue("SELECT has_schema_privilege(NULL::name,'an_missing','bad') AS v", nil))
		run(objectGrantAccessValue("SELECT has_schema_privilege('og_object_reader',4294967294::oid,'USAGE') AS v", nil))
		run(objectGrantAccessValue("SELECT has_schema_privilege('postgres',4294967294::oid,'USAGE') AS v", true))
		run(accessExecutionError("SELECT has_schema_privilege('an_missing','an_missing','bad') AS v", "42704", `role "an_missing" does not exist`))
		run(accessExecutionError("SELECT has_schema_privilege('og_object_reader','an_missing','USAGE') AS v", "3F000", `schema "an_missing" does not exist`))
		held := accessExecutionHeld(t, p, c, "SELECT has_schema_privilege('og_object_reader','an_private','USAGE') AS v")
		fixture.RunStep(t, p.Ctx, held, b.Recorder, objectGrantAccessValue(held.SQL, true))
		run(fixture.Exec("REVOKE USAGE ON SCHEMA an_private FROM og_object_reader", 0))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, objectGrantAccessValue(held.SQL, false))
		if !b.PG {
			accessNamespacesNativeSmoke(t, p)
		}
	})
}
