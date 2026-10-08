//go:build reference

package reference_test

import (
	"fmt"
	"reflect"
	"testing"
)

// SQL grants/membership are PostgreSQL-only controls; native coverage uses the
// existing internal ACL/membership APIs, never private APIs from this package.
func TestReferenceViewReplaceOwnership(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			owner := fmt.Sprintf("replace_owner_%v", prepared)
			actor := fmt.Sprintf("replace_actor_%v", prepared)
			successor := fmt.Sprintf("replace_successor_%v", prepared)
			roleReferenceCleanup(t, []string{actor, successor, owner})
			p := newReferencePair(t)
			c, err := p.postgres.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			run := func(s referenceStep) { s.prepared = prepared; referenceRunStep(t, p.ctx, c, p.postgresRecord, s) }
			grant := func(q string) {
				if _, err := referenceAdmin.ExecContext(p.ctx, q); err != nil {
					t.Fatal(q, err)
				}
			}
			for _, q := range []string{"CREATE USER " + owner, "CREATE USER " + actor, "CREATE USER " + successor, "CREATE TABLE t(id INT)"} {
				run(referenceExec(q, 0))
			}
			grant("GRANT USAGE,CREATE ON SCHEMA public TO PUBLIC")
			run(referenceExec("SET SESSION AUTHORIZATION "+owner, 0))
			run(referenceExec("CREATE VIEW v AS SELECT id FROM t", 0))
			run(referenceExec("RESET SESSION AUTHORIZATION", 0))
			run(referenceExec("SET SESSION AUTHORIZATION "+actor, 0))
			for _, q := range []string{"CREATE OR REPLACE VIEW v AS SELECT id FROM t", "ALTER VIEW v RENAME TO renamed", "DROP VIEW v"} {
				run(referenceImplicitError(q, referenceDiagnostic{Code: "42501", Message: "must be owner of view v"}))
			}
			run(referenceExec("RESET SESSION AUTHORIZATION", 0))
			run(referenceExec("SET SESSION AUTHORIZATION "+owner, 0))
			run(referenceImplicitError("ALTER VIEW v OWNER TO "+successor, referenceDiagnostic{Code: "42501", Message: "must be able to SET ROLE \"" + successor + "\""}))
			grant("GRANT " + successor + " TO " + owner + " WITH SET TRUE, INHERIT FALSE")
			grant("REVOKE CREATE ON SCHEMA public FROM PUBLIC")
			grant("GRANT CREATE ON SCHEMA public TO " + owner)
			run(referenceImplicitError("ALTER VIEW v OWNER TO "+successor, referenceDiagnostic{Code: "42501", Message: "permission denied for schema public"}))
			grant("GRANT CREATE ON SCHEMA public TO " + successor)
			run(referenceExec("ALTER VIEW v OWNER TO "+successor, 0))
			run(referenceExec("RESET SESSION AUTHORIZATION", 0))
			grant("GRANT " + successor + " TO " + actor + " WITH INHERIT TRUE, SET FALSE")
			run(referenceExec("SET SESSION AUTHORIZATION "+actor, 0))
			run(referenceExec("ALTER VIEW v SET(security_barrier=true)", 0))
			run(referenceExec("DROP VIEW v", 0))
			run(referenceExec("RESET SESSION AUTHORIZATION", 0))
		})
	}
}

func TestReferenceViewReplaceNotices(t *testing.T) {
	p := newReferencePair(t)
	for _, postgres := range []bool{false, true} {
		t.Run(fmt.Sprint(postgres), func(t *testing.T) {
			f := newControlsReferenceFixture(t, p, postgres)
			for _, tc := range []struct{ query, message string }{
				{"ALTER VIEW IF EXISTS absent RENAME TO z", "relation \"absent\" does not exist, skipping"},
				{"DROP VIEW IF EXISTS absent", "view \"absent\" does not exist, skipping"},
				{"DROP VIEW IF EXISTS absent_schema.v", "schema \"absent_schema\" does not exist, skipping"},
			} {
				f.exec(t, f.a, tc.query, false, 0)
				want := []referenceDiagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: tc.message}}
				if !reflect.DeepEqual(*f.notices, want) {
					t.Fatalf("notices got %#v want %#v", *f.notices, want)
				}
				*f.notices = nil
			}
		})
	}
}
