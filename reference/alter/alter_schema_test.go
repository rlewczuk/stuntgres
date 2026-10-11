//go:build reference

package reference_test

import (
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAlterSchemaMissingAndNotices(t *testing.T) {
	for _, postgres := range []bool{true, false} {
		t.Run(map[bool]string{true: "postgres", false: "native"}[postgres], func(t *testing.T) {
			p := fixture.NewPair(t)
			fixture.SchemaReferenceCleanup(t, "s1", "s2")
			var notices []fixture.Diagnostic
			_, c, r := fixture.NoticeReferencePool(t, p, postgres, &notices)
			referenceAlterSchemaSetup(t, p, c, r)
			for _, prepared := range []bool{false, true} {
				notices = nil
				step := fixture.ImplicitError("ALTER TABLE IF EXISTS missing SET SCHEMA s2, DROP COLUMN missing", fixture.Diagnostic{Code: "42601", Message: `syntax error at or near ","`, Position: "44"})
				step.Prepared = prepared
				if prepared {
					step.ErrorStage = "prepare"
				}
				fixture.RunStep(t, p.Ctx, c, r, step)
				if len(notices) != 0 {
					t.Fatal("malformed conditional utility emitted notice", notices)
				}
			}
			for _, target := range []string{"missing", "s1.missing", "absent.missing"} {
				q := "ALTER TABLE IF EXISTS " + target + " SET SCHEMA absent"
				notices = nil
				s, err := c.PrepareContext(p.Ctx, q)
				if err != nil || len(notices) != 0 {
					t.Fatal("Prepare", err, notices)
				}
				result, err := s.ExecContext(p.Ctx)
				s.Close()
				if err != nil {
					t.Fatal(err)
				}
				fixture.LockAffected(t, result, 0)
				want := []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `relation "missing" does not exist, skipping`}}
				if !reflect.DeepEqual(notices, want) {
					t.Fatal(notices, want)
				}
			}
			for _, tc := range []struct{ q, code, message string }{
				{"ALTER TABLE s1.missing SET SCHEMA s2", "42P01", `relation "s1.missing" does not exist`},
				{"ALTER TABLE absent.t SET SCHEMA s2", "3F000", `schema "absent" does not exist`},
				{"ALTER TABLE s1.t SET SCHEMA absent", "3F000", `schema "absent" does not exist`},
			} {
				s := fixture.Prepared(fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: tc.code, Message: tc.message}))
				s.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, c, r, s)
			}
			fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("CREATE TABLE s2.other(id INT CONSTRAINT nn_id NOT NULL)", 0))
			fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("ALTER TABLE s1.t SET SCHEMA s1", 0))
			fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("ALTER TABLE s1.t SET SCHEMA s2", 0))
			fixture.RunStep(t, p.Ctx, c, r, referenceAlterSchemaRows("SELECT id,note FROM s2.t ORDER BY id"))
		})
	}
}
