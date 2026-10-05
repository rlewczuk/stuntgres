//go:build reference

package reference_test

import (
	"fmt"
	"testing"
)

func TestReferenceSchemaACLOracle(t *testing.T) {
	roleReferenceCleanup(t, []string{"sacl_reader"})
	p := newReferencePair(t)
	schemaReferenceCleanup(t, "sacl_usage", "sacl_create")
	for _, q := range []string{"CREATE USER sacl_reader", "CREATE SCHEMA sacl_usage", "CREATE SCHEMA sacl_create"} {
		p.step(t, referenceExec(q, 0))
	}
	oracle, err := p.postgres.Conn(p.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer oracle.Close()
	run := func(s referenceStep) { referenceRunStep(t, p.ctx, oracle, p.postgresRecord, s) }
	for _, q := range []string{"GRANT USAGE ON SCHEMA sacl_usage TO sacl_reader", "GRANT CREATE ON SCHEMA sacl_create TO sacl_reader", "SET SESSION AUTHORIZATION sacl_reader"} {
		if _, err = oracle.ExecContext(p.ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	defer oracle.ExecContext(p.ctx, "RESET SESSION AUTHORIZATION")
	run(referencePrepared(referenceExec("CREATE TABLE sacl_create.t(id INT)", 0)))
	denied := roleReferenceError("SELECT id FROM sacl_create.t", "42501", "permission denied for schema sacl_create", "", true, "prepare")
	denied.diagnostic.Position = "16"
	run(denied)
	run(referenceExec("SET search_path TO sacl_usage,public", 0))
	run(referenceSelect("SELECT current_schema", []string{"current_schema"}, []string{"NAME"}, [][]any{{"sacl_usage"}}))
	for _, prepared := range []bool{false, true} {
		run(schemaReferenceCreationError("CREATE TABLE denied(id INT)", "42501", "permission denied for schema sacl_usage", prepared, "14"))
		run(schemaReferenceCreationError("/* é */ CREATE TABLE sacl_usage.denied(id INT)", "42501", "permission denied for schema sacl_usage", prepared, "22"))
	}
	// Native real-ACL counterpart is TestSchemaAuthorizationAndIndependentOwners;
	// production SQL GRANT is deliberately not implemented by this selected part.
}
func TestReferenceSchemaNotices(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := newReferencePair(t)
			schemaReferenceCleanup(t, "snotice")
			for _, postgres := range []bool{false, true} {
				t.Run(fmt.Sprint(postgres), func(t *testing.T) {
					var notices []referenceDiagnostic
					_, c, record := noticeReferencePool(t, p, postgres, &notices)
					run := func(s referenceStep) { s.prepared = prepared; referenceRunStep(t, p.ctx, c, record, s) }
					run(referenceExec("CREATE SCHEMA snotice CREATE TABLE t(id INT)", 0))
					run(referenceExec("CREATE SCHEMA IF NOT EXISTS snotice", 0))
					run(referenceExec("DROP SCHEMA IF EXISTS missing_schema_notice", 0))
					run(referenceExec("DROP SCHEMA snotice CASCADE", 0))
					wants := []referenceDiagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42P06", Message: `schema "snotice" already exists, skipping`}, {Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `schema "missing_schema_notice" does not exist, skipping`}, {Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: "drop cascades to table snotice.t"}}
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
