//go:build reference

package reference_test

import (
	"reflect"
	"testing"
)

func TestReferenceAlterSchemaMissingAndNotices(t *testing.T) {
	for _, postgres := range []bool{true, false} {
		t.Run(map[bool]string{true: "postgres", false: "native"}[postgres], func(t *testing.T) {
			p := newReferencePair(t)
			schemaReferenceCleanup(t, "s1", "s2")
			var notices []referenceDiagnostic
			_, c, r := noticeReferencePool(t, p, postgres, &notices)
			referenceAlterSchemaSetup(t, p, c, r)
			for _, prepared := range []bool{false, true} {
				notices = nil
				step := referenceImplicitError("ALTER TABLE IF EXISTS missing SET SCHEMA s2, DROP COLUMN missing", referenceDiagnostic{Code: "42601", Message: `syntax error at or near ","`, Position: "44"})
				step.prepared = prepared
				if prepared {
					step.errorStage = "prepare"
				}
				referenceRunStep(t, p.ctx, c, r, step)
				if len(notices) != 0 {
					t.Fatal("malformed conditional utility emitted notice", notices)
				}
			}
			for _, target := range []string{"missing", "s1.missing", "absent.missing"} {
				q := "ALTER TABLE IF EXISTS " + target + " SET SCHEMA absent"
				notices = nil
				s, err := c.PrepareContext(p.ctx, q)
				if err != nil || len(notices) != 0 {
					t.Fatal("Prepare", err, notices)
				}
				result, err := s.ExecContext(p.ctx)
				s.Close()
				if err != nil {
					t.Fatal(err)
				}
				referenceLockAffected(t, result, 0)
				want := []referenceDiagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `relation "missing" does not exist, skipping`}}
				if !reflect.DeepEqual(notices, want) {
					t.Fatal(notices, want)
				}
			}
			for _, tc := range []struct{ q, code, message string }{
				{"ALTER TABLE s1.missing SET SCHEMA s2", "42P01", `relation "s1.missing" does not exist`},
				{"ALTER TABLE absent.t SET SCHEMA s2", "3F000", `schema "absent" does not exist`},
				{"ALTER TABLE s1.t SET SCHEMA absent", "3F000", `schema "absent" does not exist`},
			} {
				s := referencePrepared(referenceImplicitError(tc.q, referenceDiagnostic{Code: tc.code, Message: tc.message}))
				s.errorStage = "execute"
				referenceRunStep(t, p.ctx, c, r, s)
			}
			referenceRunStep(t, p.ctx, c, r, referenceExec("CREATE TABLE s2.other(id INT CONSTRAINT nn_id NOT NULL)", 0))
			referenceRunStep(t, p.ctx, c, r, referenceExec("ALTER TABLE s1.t SET SCHEMA s1", 0))
			referenceRunStep(t, p.ctx, c, r, referenceExec("ALTER TABLE s1.t SET SCHEMA s2", 0))
			referenceRunStep(t, p.ctx, c, r, referenceAlterSchemaRows("SELECT id,note FROM s2.t ORDER BY id"))
		})
	}
}
