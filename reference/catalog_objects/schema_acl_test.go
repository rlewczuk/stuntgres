//go:build reference

package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSchemaACLOracle(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"sacl_reader"})
	p := fixture.NewPair(t)
	fixture.SchemaReferenceCleanup(t, "sacl_usage", "sacl_create")
	for _, q := range []string{"CREATE USER sacl_reader", "CREATE SCHEMA sacl_usage", "CREATE SCHEMA sacl_create"} {
		p.Step(t, fixture.Exec(q, 0))
	}
	oracle, err := p.Postgres.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer oracle.Close()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, oracle, p.PostgresRecord, s) }
	for _, q := range []string{"GRANT USAGE ON SCHEMA sacl_usage TO sacl_reader", "GRANT CREATE ON SCHEMA sacl_create TO sacl_reader", "SET SESSION AUTHORIZATION sacl_reader"} {
		if _, err = oracle.ExecContext(p.Ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	defer oracle.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
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
	// Native real-ACL counterpart is TestSchemaAuthorizationAndIndependentOwners;
	// production SQL GRANT is deliberately not implemented by this selected part.
}
func TestReferenceSchemaNotices(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := fixture.NewPair(t)
			fixture.SchemaReferenceCleanup(t, "snotice")
			for _, postgres := range []bool{false, true} {
				t.Run(fmt.Sprint(postgres), func(t *testing.T) {
					var notices []fixture.Diagnostic
					_, c, record := fixture.NoticeReferencePool(t, p, postgres, &notices)
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
