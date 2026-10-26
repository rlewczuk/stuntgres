package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceAccessNamespacesIdentity(t *testing.T) {
	objectGrantScenario(t, []string{"an_private", "an_names", "an_shadow", "an.dot"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		defer accessExecutionResetConnection(t, c)
		accessNamespacesSetup(t, p, b, c, run)
		q := "SELECT has_type_privilege('og_object_reader',$1::text,'USAGE') AS v"
		held := accessExecutionHeld(t, p, c, q)
		for _, tc := range []struct {
			name string
			oid  int64
		}{{"integer", 23}, {"int4", 23}, {"pg_catalog.int4", 23}, {"double precision", 701}, {"float8", 701}, {"varchar(12)", 1043}, {"integer[]", 1007}, {"23", 23}} {
			run(fixture.Select("SELECT $1::regtype::oid::bigint AS oid", []string{"oid"}, []string{"INT8"}, [][]any{{tc.oid}}, tc.name))
			run(objectGrantAccessValue(q, true, tc.name))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, objectGrantAccessValue(q, true, tc.name))
			run(objectGrantAccessValue("SELECT has_type_privilege('og_object_reader',$1::regtype,'USAGE') AS v", true, tc.name))
		}
		for _, ddl := range []string{
			"CREATE SCHEMA an_names", "CREATE SCHEMA an_shadow", `CREATE SCHEMA "an.dot"`,
			`CREATE TYPE an_names."Mixed" AS ENUM('a')`, "CREATE TYPE an_names.base AS ENUM('a')", "CREATE DOMAIN an_names.dom AS an_names.base",
			"CREATE TYPE an_names.r AS RANGE(subtype=integer)", "CREATE DOMAIN an_names.d AS integer", "CREATE DOMAIN an_shadow.d AS integer",
			"GRANT USAGE ON SCHEMA an_names,an_shadow TO og_object_reader",
			"REVOKE USAGE ON TYPE an_names.base,an_names.dom,an_names.r,an_names.d,an_shadow.d FROM PUBLIC",
			"GRANT USAGE ON TYPE an_names.base,an_names.dom,an_names.r,an_names.d TO og_object_reader",
		} {
			run(fixture.Exec(ddl, 0))
		}
		for _, name := range []string{`an_names."Mixed"`, ` an_names . "Mixed" `, "an_names.base", "an_names.base[]", "an_names.dom", "an_names.dom[]", "an_names.r", "an_names.r[]", "an_names.r_multirange", "an_names.r_multirange[]"} {
			run(objectGrantAccessValue(q, true, name))
		}
		run(fixture.Exec("REVOKE USAGE ON TYPE an_names.base FROM og_object_reader", 0))
		for _, tc := range []struct {
			name string
			want bool
		}{{"an_names.base", false}, {"an_names.base[]", false}, {"an_names.dom", true}, {"an_names.dom[]", true}} {
			run(objectGrantAccessValue(q, tc.want, tc.name))
		}
		run(fixture.Exec("REVOKE USAGE ON TYPE an_names.dom FROM og_object_reader", 0))
		run(fixture.Exec("GRANT USAGE ON TYPE an_names.base TO og_object_reader", 0))
		run(objectGrantAccessValue(q, true, "an_names.base"))
		run(objectGrantAccessValue(q, false, "an_names.dom"))
		run(fixture.Exec("REVOKE USAGE ON TYPE an_names.r FROM og_object_reader", 0))
		for _, name := range []string{"an_names.r", "an_names.r[]", "an_names.r_multirange", "an_names.r_multirange[]"} {
			run(objectGrantAccessValue(q, false, name))
		}
		var oid int64
		if err := c.QueryRowContext(p.Ctx, "SELECT 'an_names.d'::regtype::oid").Scan(&oid); err != nil {
			t.Fatal(err)
		}
		oidHeld := accessExecutionHeld(t, p, c, "SELECT has_type_privilege('og_object_reader',$1::oid,'USAGE') AS v")
		fixture.RunStep(t, p.Ctx, held, b.Recorder, objectGrantAccessValue(q, true, "an_names.d"))
		run(fixture.Exec("ALTER DOMAIN an_names.d RENAME TO renamed", 0))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, accessExecutionError(q, "42704", `type "an_names.d" does not exist`, "an_names.d"))
		fixture.RunStep(t, p.Ctx, oidHeld, b.Recorder, objectGrantAccessValue(oidHeld.SQL, true, oid))
		run(fixture.Exec("SET search_path=an_names,public", 0))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, objectGrantAccessValue(q, true, "renamed"))
		run(fixture.Exec("SET search_path=an_shadow,public", 0))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, objectGrantAccessValue(q, false, "d"))
		run(fixture.Exec("SET search_path=public", 0))
		// Lookup uses the invoker, not the actor whose ACL is requested.
		run(fixture.Exec("REVOKE USAGE ON SCHEMA an_names FROM og_object_reader", 0))
		run(objectGrantAccessValue(q, true, "an_names.renamed"))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			run(accessExecutionError(q, "42501", "permission denied for schema an_names", "an_names.renamed"))
			run(objectGrantAccessValue(oidHeld.SQL, true, oid))
		})
		for _, tc := range []struct{ input, code, message, position, context string }{
			{"integer(", "42601", `syntax error at or near "("`, "8", `invalid type name "integer("`},
			{"int4(", "42601", "syntax error at end of input", "6", `invalid type name "int4("`},
			{"4294967296", "22003", `value "4294967296" is out of range for type oid`, "", ""},
			{"an_private.an_missing", "42704", `type "an_private.an_missing" does not exist`, "", ""},
			{"an_missing_schema.d", "3F000", `schema "an_missing_schema" does not exist`, "", ""},
			{"-", "42704", `type "-" does not exist`, "", ""},
			{"0", "42704", `type "0" does not exist`, "", ""},
			{"4294967294", "42704", "type with OID 4294967294 does not exist", "", ""},
		} {
			s := fixture.ImplicitError(q, fixture.Diagnostic{Code: tc.code, Message: tc.message, Position: tc.position, Context: tc.context}, tc.input)
			s.SelectRows = true
			s.ErrorStage = "execute"
			run(s)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, objectGrantAccessValue(q, true, "integer"))
		}
		run(objectGrantAccessValue("SELECT has_type_privilege('postgres','4294967294','USAGE') AS v", true))
		for _, name := range []string{"-", "0"} {
			run(accessExecutionError("SELECT has_type_privilege('postgres',$1::text,'USAGE') AS v", "42704", `type "`+name+`" does not exist`, name))
		}
		// Other families consume exact names, not regtype's identifier grammar.
		run(fixture.Exec(`GRANT USAGE ON SCHEMA "an.dot" TO og_object_reader`, 0))
		run(objectGrantAccessValue("SELECT has_schema_privilege('og_object_reader','an.dot','USAGE') AS v", true))
		run(accessExecutionError("SELECT has_schema_privilege('og_object_reader',$1::text,'USAGE') AS v", "3F000", `schema ""an.dot"" does not exist`, `"an.dot"`))
	})
}
