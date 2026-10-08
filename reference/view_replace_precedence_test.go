//go:build reference

package reference_test

import (
	"fmt"
	"testing"
)

// SQL GRANT remains a PostgreSQL-only authority control. The native counterpart
// uses the same role/schema ownership scenario through existing engine APIs.
func TestReferenceViewReplaceSelectedTargetOwnershipPrecedence(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, kind := range []string{"table", "sequence"} {
			t.Run(fmt.Sprintf("%s/prepared=%v", kind, prepared), func(t *testing.T) {
				actor := fmt.Sprintf("precedence_actor_%s_%v", kind, prepared)
				roleReferenceCleanup(t, []string{actor})
				p := newReferencePair(t)
				schemaReferenceCleanup(t, "chosen")
				c, err := p.postgres.Conn(p.ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer c.Close()
				run := func(s referenceStep) {
					s.prepared = prepared
					referenceRunStep(t, p.ctx, c, p.postgresRecord, s)
				}
				for _, q := range []string{"CREATE USER " + actor, "CREATE SCHEMA chosen AUTHORIZATION " + actor, "CREATE TABLE chosen.t(id INT)"} {
					run(referenceExec(q, 0))
				}
				run(referenceExec("INSERT INTO chosen.t VALUES(1)", 1))
				kindCode := "r"
				if kind == "table" {
					run(referenceExec("CREATE TABLE chosen.v(id INT)", 0))
					run(referenceExec("INSERT INTO chosen.v VALUES(7)", 1))
				} else {
					kindCode = "S"
					run(referenceExec("CREATE SEQUENCE chosen.v START WITH 7", 0))
				}
				// The actor owns the selected namespace (CREATE/USAGE), but not
				// its selected table or sequence. No body SELECT grant is needed
				// merely to define this view.
				var oid, namespace, owner int64
				if err := c.QueryRowContext(p.ctx, "SELECT oid,relnamespace,relowner FROM pg_class WHERE relname='v'").Scan(&oid, &namespace, &owner); err != nil {
					t.Fatal(err)
				}
				identity := referenceSelect("SELECT oid::BIGINT AS oid,relnamespace::BIGINT AS namespace,relowner::BIGINT AS owner,relkind::TEXT AS kind FROM pg_class WHERE oid=$1", []string{"oid", "namespace", "owner", "kind"}, []string{"INT8", "INT8", "INT8", "TEXT"}, [][]any{{oid, namespace, owner, kindCode}}, oid)
				run(referenceExec("SET SESSION AUTHORIZATION "+actor, 0))
				query := "CREATE OR REPLACE VIEW chosen.v AS SELECT id FROM chosen.t"
				s := referenceImplicitError(query, referenceDiagnostic{Code: "42501", Message: "must be owner of " + kind + " v"})
				s.errorStage = "execute"
				run(s)
				run(identity)
				run(referenceExec("RESET SESSION AUTHORIZATION", 0))
				// Retain independently specified owner/superuser wrong-kind
				// behavior rather than replacing it with the nonowner expectation.
				s = referenceImplicitError(query, referenceDiagnostic{Code: "42809", Message: `"v" is not a view`})
				s.errorStage = "execute"
				run(s)
				run(identity)
				if kind == "table" {
					run(referenceSelect("SELECT id FROM chosen.v", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
				} else {
					sequenceProbeValue(t, p.ctx, c, "SELECT nextval('chosen.v')", 7)
				}
				run(referenceSelect("SELECT id FROM chosen.t", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
			})
		}
	}
}
