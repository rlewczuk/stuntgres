//go:build reference

package reference_test

import (
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAlterRenameIfExists(t *testing.T) {
	p := fixture.NewPair(t)
	for _, postgres := range []bool{true, false} {
		name := "memory"
		if postgres {
			name = "postgres"
		}
		t.Run(name, func(t *testing.T) {
			var notices []fixture.Diagnostic
			_, conn, record := fixture.NoticeReferencePool(t, p, postgres, &notices)
			for _, target := range []string{"missing", "public.missing", "absent_schema.missing"} {
				for _, suffix := range []string{"RENAME a TO b", "RENAME TO newtable"} {
					q := "ALTER TABLE IF EXISTS " + target + " " + suffix
					notices = nil
					s, err := conn.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal("Prepare", q, err)
					}
					if len(notices) != 0 {
						t.Fatal("Prepare emitted notices", notices)
					}
					result, err := s.ExecContext(p.Ctx)
					s.Close()
					if err != nil {
						t.Fatal(q, err)
					}
					fixture.LockAffected(t, result, 0)
					want := []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `relation "missing" does not exist, skipping`}}
					if !reflect.DeepEqual(notices, want) {
						t.Fatalf("%s notices %#v want %#v", q, notices, want)
					}
					notices = nil
					fixture.RunStep(t, p.Ctx, conn, record, fixture.Exec(q, 0))
					if !reflect.DeepEqual(notices, want) {
						t.Fatal(q, notices, want)
					}
				}
			}
			fixture.RunStep(t, p.Ctx, conn, record, fixture.Exec("CREATE TABLE t(a INT)", 0))
			failure := fixture.ImplicitError("ALTER TABLE IF EXISTS t RENAME missing TO a", fixture.Diagnostic{Code: "42703", Message: `column "missing" does not exist`})
			failure.Prepared = true
			failure.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, conn, record, failure)
			failure = fixture.ImplicitError("ALTER TABLE absent_schema.t RENAME TO u", fixture.Diagnostic{Code: "3F000", Message: `schema "absent_schema" does not exist`})
			failure.Prepared = true
			failure.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, conn, record, failure)
		})
	}
}
