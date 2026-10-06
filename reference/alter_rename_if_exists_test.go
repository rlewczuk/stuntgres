//go:build reference

package reference_test

import (
	"reflect"
	"testing"
)

func TestReferenceAlterRenameIfExists(t *testing.T) {
	p := newReferencePair(t)
	for _, postgres := range []bool{true, false} {
		name := "memory"
		if postgres {
			name = "postgres"
		}
		t.Run(name, func(t *testing.T) {
			var notices []referenceDiagnostic
			_, conn, record := noticeReferencePool(t, p, postgres, &notices)
			for _, target := range []string{"missing", "public.missing", "absent_schema.missing"} {
				for _, suffix := range []string{"RENAME a TO b", "RENAME TO newtable"} {
					q := "ALTER TABLE IF EXISTS " + target + " " + suffix
					notices = nil
					s, err := conn.PrepareContext(p.ctx, q)
					if err != nil {
						t.Fatal("Prepare", q, err)
					}
					if len(notices) != 0 {
						t.Fatal("Prepare emitted notices", notices)
					}
					result, err := s.ExecContext(p.ctx)
					s.Close()
					if err != nil {
						t.Fatal(q, err)
					}
					referenceLockAffected(t, result, 0)
					want := []referenceDiagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `relation "missing" does not exist, skipping`}}
					if !reflect.DeepEqual(notices, want) {
						t.Fatalf("%s notices %#v want %#v", q, notices, want)
					}
					notices = nil
					referenceRunStep(t, p.ctx, conn, record, referenceExec(q, 0))
					if !reflect.DeepEqual(notices, want) {
						t.Fatal(q, notices, want)
					}
				}
			}
			referenceRunStep(t, p.ctx, conn, record, referenceExec("CREATE TABLE t(a INT)", 0))
			failure := referenceImplicitError("ALTER TABLE IF EXISTS t RENAME missing TO a", referenceDiagnostic{Code: "42703", Message: `column "missing" does not exist`})
			failure.prepared = true
			failure.errorStage = "execute"
			referenceRunStep(t, p.ctx, conn, record, failure)
			failure = referenceImplicitError("ALTER TABLE absent_schema.t RENAME TO u", referenceDiagnostic{Code: "3F000", Message: `schema "absent_schema" does not exist`})
			failure.prepared = true
			failure.errorStage = "execute"
			referenceRunStep(t, p.ctx, conn, record, failure)
		})
	}
}
