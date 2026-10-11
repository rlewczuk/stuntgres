package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

// Selected target ownership is checked before the wrong-kind error on both backends.
func TestReferenceViewReplaceSelectedTargetOwnershipPrecedence(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, kind := range []string{"table", "sequence"} {
			t.Run(fmt.Sprintf("%s/prepared=%v", kind, prepared), func(t *testing.T) {
				actor := fmt.Sprintf("precedence_actor_%s_%v", kind, prepared)
				fixture.RoleReferenceCleanup(t, []string{actor})
				p := fixture.NewScenario(t)
				fixture.SchemaReferenceCleanup(t, "chosen")
				for _, backend := range p.Backends() {
					c, err := backend.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer c.Close()
					run := func(s fixture.Step) {
						s.Prepared = prepared
						fixture.RunStep(t, p.Ctx, c, backend.Recorder, s)
					}
					for _, q := range []string{"CREATE USER " + actor, "CREATE SCHEMA chosen AUTHORIZATION " + actor, "CREATE TABLE chosen.t(id INT)"} {
						run(fixture.Exec(q, 0))
					}
					run(fixture.Exec("INSERT INTO chosen.t VALUES(1)", 1))
					kindCode := "r"
					if kind == "table" {
						run(fixture.Exec("CREATE TABLE chosen.v(id INT)", 0))
						run(fixture.Exec("INSERT INTO chosen.v VALUES(7)", 1))
					} else {
						kindCode = "S"
						run(fixture.Exec("CREATE SEQUENCE chosen.v START WITH 7", 0))
					}
					// The actor owns the selected namespace (CREATE/USAGE), but not
					// its selected table or sequence. No body SELECT grant is needed
					// merely to define this view.
					var oid, namespace, owner int64
					var targetKind string
					if err := c.QueryRowContext(p.Ctx, "SELECT oid,relnamespace,relowner,relkind FROM pg_class WHERE relname='v'").Scan(&oid, &namespace, &owner, &targetKind); err != nil {
						t.Fatal(err)
					}
					if targetKind != kindCode {
						t.Fatal("unexpected selected target kind", targetKind, kindCode)
					}
					checkIdentity := func() {
						var retainedOID, retainedNamespace, retainedOwner int64
						var retainedKind string
						if err := c.QueryRowContext(p.Ctx, "SELECT oid,relnamespace,relowner,relkind FROM pg_class WHERE relname='v'").Scan(&retainedOID, &retainedNamespace, &retainedOwner, &retainedKind); err != nil {
							t.Fatal(err)
						}
						if retainedOID != oid || retainedNamespace != namespace || retainedOwner != owner || retainedKind != targetKind {
							t.Fatal("failed replacement changed target identity/kind")
						}
					}
					identity := fixture.Select("SELECT oid::BIGINT AS oid,relnamespace::BIGINT AS namespace,relowner::BIGINT AS owner,relkind::TEXT AS kind FROM pg_class WHERE oid=$1", []string{"oid", "namespace", "owner", "kind"}, []string{"INT8", "INT8", "INT8", "TEXT"}, [][]any{{oid, namespace, owner, kindCode}}, oid)
					run(fixture.Exec("SET SESSION AUTHORIZATION "+actor, 0))
					query := "CREATE OR REPLACE VIEW chosen.v AS SELECT id FROM chosen.t"
					s := fixture.ImplicitError(query, fixture.Diagnostic{Code: "42501", Message: "must be owner of " + kind + " v"})
					s.ErrorStage = "execute"
					run(s)
					checkIdentity()
					if backend.PG {
						// Keep the legacy PostgreSQL descriptor/cast companion;
						// native uses the original typed catalog Scan above.
						run(identity)
					}
					run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
					// Retain independently specified owner/superuser wrong-kind
					// behavior rather than replacing it with the nonowner expectation.
					s = fixture.ImplicitError(query, fixture.Diagnostic{Code: "42809", Message: `"v" is not a view`})
					s.ErrorStage = "execute"
					run(s)
					checkIdentity()
					if backend.PG {
						run(identity)
					}
					if kind == "table" {
						run(fixture.Select("SELECT id FROM chosen.v", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
					} else {
						fixture.SequenceProbeValue(t, p.Ctx, c, "SELECT nextval('chosen.v')", 7)
					}
					run(fixture.Select("SELECT id FROM chosen.t", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
				}
			})
		}
	}
}
