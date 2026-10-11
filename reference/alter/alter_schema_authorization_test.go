//go:build reference

package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

// PostgreSQL GRANT/REVOKE is real SQL here. Corresponding native ACLValue
// fixtures are internal, not a fake GRANT implementation in the public driver.
func TestReferenceAlterSchemaPostgresAuthorization(t *testing.T) {
	for _, tc := range []struct{ name, actor, sourceRights, targetRights, destination, code, message string }{
		{"source_usage_only", "move_owner", "USAGE", "USAGE,CREATE", "s2", "", ""},
		{"destination_create_only", "move_owner", "USAGE", "CREATE", "s2", "", ""},
		{"destination_usage_only", "move_owner", "USAGE", "USAGE", "s2", "42501", "permission denied for schema s2"},
		{"destination_neither", "move_owner", "USAGE", "", "s2", "42501", "permission denied for schema s2"},
		{"source_no_usage", "move_owner", "CREATE", "CREATE", "s2", "42501", "permission denied for schema s1"},
		{"same_schema_no_create", "move_owner", "USAGE", "CREATE", "s1", "42501", "permission denied for schema s1"},
		{"schema_owner_not_table_owner", "move_schema_owner", "USAGE", "CREATE", "s2", "42501", "must be owner of table t"},
		{"nonowner_with_table_rights", "move_reader", "USAGE", "CREATE", "s2", "42501", "must be owner of table t"},
		{"superuser", "postgres", "", "", "s2", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"move_owner", "move_schema_owner", "move_reader"})
			p := fixture.NewPair(t)
			fixture.SchemaReferenceCleanup(t, "s1", "s2")
			c, err := p.Postgres.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			r := &fixture.Recorder{}
			run := func(q string, count int64) { fixture.RunStep(t, p.Ctx, c, r, fixture.Exec(q, count)) }
			for _, q := range []string{"CREATE USER move_owner", "CREATE USER move_schema_owner", "CREATE USER move_reader", "CREATE SCHEMA s1 AUTHORIZATION move_schema_owner", "CREATE SCHEMA s2 AUTHORIZATION move_schema_owner", "GRANT USAGE,CREATE ON SCHEMA s1 TO move_owner", "SET SESSION AUTHORIZATION move_owner", "CREATE TABLE s1.t(id INT)", "CREATE SEQUENCE s1.q1 OWNED BY s1.t.id", "CREATE SEQUENCE s1.q2 OWNED BY s1.t.id"} {
				run(q, 0)
			}
			run("INSERT INTO s1.t VALUES(7)", 1)
			for _, q := range []string{"RESET SESSION AUTHORIZATION", "GRANT SELECT,INSERT ON TABLE s1.t TO move_reader", "GRANT USAGE ON SCHEMA s1 TO move_reader", "GRANT CREATE ON SCHEMA s2 TO move_reader", "REVOKE ALL ON SCHEMA s1 FROM move_owner", "REVOKE ALL ON SCHEMA s2 FROM move_owner"} {
				run(q, 0)
			}
			if tc.sourceRights != "" {
				run("GRANT "+tc.sourceRights+" ON SCHEMA s1 TO move_owner", 0)
			}
			if tc.targetRights != "" {
				run("GRANT "+tc.targetRights+" ON SCHEMA s2 TO move_owner", 0)
			}
			run("SET SESSION AUTHORIZATION "+tc.actor, 0)
			q := "ALTER TABLE s1.t SET SCHEMA " + tc.destination
			for _, prepared := range []bool{false, true} {
				if tc.code == "" {
					// A success changes the original qualified name; run this actor's
					// positive fixture once with actual Prepare rather than twice.
					if !prepared {
						continue
					}
					fixture.RunStep(t, p.Ctx, c, r, fixture.Prepared(fixture.Exec(q, 0)))
				} else {
					s := fixture.ImplicitError(q, fixture.Diagnostic{Code: tc.code, Message: tc.message})
					s.Prepared, s.ErrorStage = prepared, "execute"
					fixture.RunStep(t, p.Ctx, c, r, s)
				}
			}
			run("RESET SESSION AUTHORIZATION", 0)
			schema := "s1"
			if tc.code == "" {
				schema = tc.destination
			}
			fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT id FROM "+schema+".t", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
			fixture.GenericRenameState(t, p, c, schema+".q1", 1, false, true)
			fixture.GenericRenameState(t, p, c, schema+".q2", 1, false, true)
			if tc.name == "destination_create_only" {
				run("SET SESSION AUTHORIZATION move_owner", 0)
				s := fixture.ImplicitError("SELECT id FROM s2.t", fixture.Diagnostic{Code: "42501", Message: "permission denied for schema s2", Position: "16"})
				s.SelectRows, s.ErrorStage = true, "execute"
				fixture.RunStep(t, p.Ctx, c, r, s)
				run("RESET SESSION AUTHORIZATION", 0)
			}
		})
	}
}
