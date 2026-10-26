package reference_test

import (
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceSchemaACLOracle(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"sacl_reader"})
	fixture.SchemaReferenceCleanup(t, "sacl_usage", "sacl_create")
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			c, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, backend.Recorder, s) }
			for _, q := range []string{"CREATE USER sacl_reader", "CREATE SCHEMA sacl_usage", "CREATE SCHEMA sacl_create", "GRANT USAGE ON SCHEMA sacl_usage TO sacl_reader", "GRANT CREATE ON SCHEMA sacl_create TO sacl_reader", "SET SESSION AUTHORIZATION sacl_reader"} {
				run(fixture.Exec(q, 0))
			}
			run(fixture.Prepared(fixture.Exec("CREATE TABLE sacl_create.t(id INT)", 0)))
			denied := fixture.RoleReferenceError("SELECT id FROM sacl_create.t", "42501", "permission denied for schema sacl_create", "", true, "prepare")
			denied.Diagnostic.Position = "16"
			run(denied)
			run(fixture.Exec("SET search_path TO sacl_usage,public", 0))
			run(fixture.Select("SELECT current_schema", []string{"current_schema"}, []string{"NAME"}, [][]any{{"sacl_usage"}}))
			for _, prepared := range []bool{false, true} {
				run(fixture.SchemaReferenceCreationError("CREATE TABLE denied(id INT)", "42501", "permission denied for schema sacl_usage", prepared, "14"))
				run(fixture.SchemaReferenceCreationError("/* é */ CREATE TABLE sacl_usage.denied(id INT)", "42501", "permission denied for schema sacl_usage", prepared, "22"))
			}
		})
	}
}
func TestReferenceSchemaNotices(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			fixture.SchemaReferenceCleanup(t, "snotice")
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					var notices []fixture.Diagnostic
					_, c, record := fixture.NoticeReferencePool(t, p, backend.PG, &notices)
					run := func(s fixture.Step) { s.Prepared = prepared; fixture.RunStep(t, p.Ctx, c, record, s) }
					run(fixture.Exec("CREATE SCHEMA snotice CREATE TABLE t(id INT)", 0))
					run(fixture.Exec("CREATE SCHEMA IF NOT EXISTS snotice", 0))
					run(fixture.Exec("DROP SCHEMA IF EXISTS missing_schema_notice", 0))
					run(fixture.Exec("DROP SCHEMA snotice CASCADE", 0))
					wants := []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42P06", Message: `schema "snotice" already exists, skipping`}, {Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `schema "missing_schema_notice" does not exist, skipping`}, {Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: "drop cascades to table snotice.t"}}
					if len(notices) != len(wants) {
						t.Fatal(notices)
					}
					for i, want := range wants {
						if notices[i] != want {
							t.Fatalf("notice %d %#v want %#v", i, notices[i], want)
						}
					}
				})
			}
		})
	}
}
