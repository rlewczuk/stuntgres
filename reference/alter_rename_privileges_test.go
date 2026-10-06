//go:build reference

package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceAlterRenamePrivileges(t *testing.T) {
	roleReferenceCleanup(t, []string{"rename_owner", "rename_other"})
	p := newReferencePair(t)
	schemaReferenceCleanup(t, "rename_schema")
	for _, backend := range []struct {
		name string
		db   *sql.DB
		pg   bool
	}{{"postgres", p.postgres, true}, {"memory", p.memory, false}} {
		t.Run(backend.name, func(t *testing.T) {
			c, err := backend.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			record := &referenceRecorder{}
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, record, s) }
			for _, q := range []string{"CREATE USER rename_owner", "CREATE USER rename_other", "CREATE SCHEMA rename_schema AUTHORIZATION rename_other", "SET SESSION AUTHORIZATION rename_other", "CREATE TABLE rename_schema.other(a INT)", "RESET SESSION AUTHORIZATION"} {
				run(referenceExec(q, 0))
			}
			if backend.pg {
				run(referenceExec("GRANT USAGE,CREATE ON SCHEMA rename_schema TO rename_owner", 0))
			} else {
				// Native host ACL witnesses live in internal/engine; SQL GRANT is outside this part.
				run(referenceExec("CREATE SCHEMA owned AUTHORIZATION rename_owner", 0))
			}
			target := "rename_schema.t"
			if !backend.pg {
				target = "owned.t"
			}
			run(referenceExec("SET SESSION AUTHORIZATION rename_owner", 0))
			run(referenceExec("CREATE TABLE "+target+"(a INT)", 0))
			run(referenceExec("RESET SESSION AUTHORIZATION", 0))
			if backend.pg {
				run(referenceExec("REVOKE CREATE ON SCHEMA rename_schema FROM rename_owner", 0))
			}
			run(referenceExec("SET SESSION AUTHORIZATION rename_owner", 0))
			run(referencePrepared(referenceExec("ALTER TABLE "+target+" RENAME a TO b", 0)))
			if backend.pg {
				failure := referenceImplicitError("ALTER TABLE "+target+" RENAME TO renamed", referenceDiagnostic{Code: "42501", Message: "permission denied for schema rename_schema"})
				failure.prepared = true
				failure.errorStage = "execute"
				run(failure)
			}
			run(referenceExec("RESET SESSION AUTHORIZATION", 0))
			run(referenceExec("SET SESSION AUTHORIZATION rename_other", 0))
			// A schema owner is not the table owner; owner denial precedes schema CREATE.
			if backend.pg {
				failure := referenceImplicitError("ALTER TABLE "+target+" RENAME TO renamed", referenceDiagnostic{Code: "42501", Message: "must be owner of table t"})
				failure.prepared = true
				failure.errorStage = "execute"
				run(failure)
			}
			run(referenceExec("RESET SESSION AUTHORIZATION", 0))
			run(referencePrepared(referenceExec("ALTER TABLE "+target+" RENAME TO renamed", 0)))
			if !backend.pg {
				run(referenceExec("DROP SCHEMA owned CASCADE", 0))
			}
		})
	}
}
