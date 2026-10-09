package reference_test

import (
	"fmt"
	"testing"
)

// Selected target ownership is checked before the wrong-kind error on both backends.
func TestReferenceViewReplaceSelectedTargetOwnershipPrecedence(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, kind := range []string{"table", "sequence"} {
			t.Run(fmt.Sprintf("%s/prepared=%v", kind, prepared), func(t *testing.T) {
				actor := fmt.Sprintf("precedence_actor_%s_%v", kind, prepared)
				roleReferenceCleanup(t, []string{actor})
				p := newReferenceScenario(t)
				schemaReferenceCleanup(t, "chosen")
				for _, backend := range p.backends() {
					c, err := backend.db.Conn(p.ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer c.Close()
					run := func(s referenceStep) {
						s.prepared = prepared
						referenceRunStep(t, p.ctx, c, backend.recorder, s)
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
					var targetKind string
					if err := c.QueryRowContext(p.ctx, "SELECT oid,relnamespace,relowner,relkind FROM pg_class WHERE relname='v'").Scan(&oid, &namespace, &owner, &targetKind); err != nil {
						t.Fatal(err)
					}
					if targetKind != kindCode {
						t.Fatal("unexpected selected target kind", targetKind, kindCode)
					}
					checkIdentity := func() {
						var retainedOID, retainedNamespace, retainedOwner int64
						var retainedKind string
						if err := c.QueryRowContext(p.ctx, "SELECT oid,relnamespace,relowner,relkind FROM pg_class WHERE relname='v'").Scan(&retainedOID, &retainedNamespace, &retainedOwner, &retainedKind); err != nil {
							t.Fatal(err)
						}
						if retainedOID != oid || retainedNamespace != namespace || retainedOwner != owner || retainedKind != targetKind {
							t.Fatal("failed replacement changed target identity/kind")
						}
					}
					identity := referenceSelect("SELECT oid::BIGINT AS oid,relnamespace::BIGINT AS namespace,relowner::BIGINT AS owner,relkind::TEXT AS kind FROM pg_class WHERE oid=$1", []string{"oid", "namespace", "owner", "kind"}, []string{"INT8", "INT8", "INT8", "TEXT"}, [][]any{{oid, namespace, owner, kindCode}}, oid)
					run(referenceExec("SET SESSION AUTHORIZATION "+actor, 0))
					query := "CREATE OR REPLACE VIEW chosen.v AS SELECT id FROM chosen.t"
					s := referenceImplicitError(query, referenceDiagnostic{Code: "42501", Message: "must be owner of " + kind + " v"})
					s.errorStage = "execute"
					run(s)
					checkIdentity()
					if backend.pg {
						// Keep the legacy PostgreSQL descriptor/cast companion;
						// native uses the original typed catalog Scan above.
						run(identity)
					}
					run(referenceExec("RESET SESSION AUTHORIZATION", 0))
					// Retain independently specified owner/superuser wrong-kind
					// behavior rather than replacing it with the nonowner expectation.
					s = referenceImplicitError(query, referenceDiagnostic{Code: "42809", Message: `"v" is not a view`})
					s.errorStage = "execute"
					run(s)
					checkIdentity()
					if backend.pg {
						run(identity)
					}
					if kind == "table" {
						run(referenceSelect("SELECT id FROM chosen.v", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
					} else {
						sequenceProbeValue(t, p.ctx, c, "SELECT nextval('chosen.v')", 7)
					}
					run(referenceSelect("SELECT id FROM chosen.t", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
				}
			})
		}
	}
}
