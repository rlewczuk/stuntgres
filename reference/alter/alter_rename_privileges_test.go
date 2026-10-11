//go:build reference

package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAlterRenamePrivileges(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"rename_owner", "rename_other"})
	p := fixture.NewPair(t)
	fixture.SchemaReferenceCleanup(t, "rename_schema")
	for _, backend := range []struct {
		name string
		db   *sql.DB
		pg   bool
	}{{"postgres", p.Postgres, true}, {"memory", p.Memory, false}} {
		t.Run(backend.name, func(t *testing.T) {
			c, err := backend.db.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			record := &fixture.Recorder{}
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, record, s) }
			for _, q := range []string{"CREATE USER rename_owner", "CREATE USER rename_other", "CREATE SCHEMA rename_schema AUTHORIZATION rename_other", "SET SESSION AUTHORIZATION rename_other", "CREATE TABLE rename_schema.other(a INT)", "RESET SESSION AUTHORIZATION"} {
				run(fixture.Exec(q, 0))
			}
			if backend.pg {
				run(fixture.Exec("GRANT USAGE,CREATE ON SCHEMA rename_schema TO rename_owner", 0))
			} else {
				// Native host ACL witnesses live in internal/engine; SQL GRANT is outside this part.
				run(fixture.Exec("CREATE SCHEMA owned AUTHORIZATION rename_owner", 0))
			}
			target := "rename_schema.t"
			if !backend.pg {
				target = "owned.t"
			}
			run(fixture.Exec("SET SESSION AUTHORIZATION rename_owner", 0))
			run(fixture.Exec("CREATE TABLE "+target+"(a INT)", 0))
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			if backend.pg {
				run(fixture.Exec("REVOKE CREATE ON SCHEMA rename_schema FROM rename_owner", 0))
			}
			run(fixture.Exec("SET SESSION AUTHORIZATION rename_owner", 0))
			run(fixture.Prepared(fixture.Exec("ALTER TABLE "+target+" RENAME a TO b", 0)))
			if backend.pg {
				failure := fixture.ImplicitError("ALTER TABLE "+target+" RENAME TO renamed", fixture.Diagnostic{Code: "42501", Message: "permission denied for schema rename_schema"})
				failure.Prepared = true
				failure.ErrorStage = "execute"
				run(failure)
			}
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			run(fixture.Exec("SET SESSION AUTHORIZATION rename_other", 0))
			// A schema owner is not the table owner; owner denial precedes schema CREATE.
			if backend.pg {
				failure := fixture.ImplicitError("ALTER TABLE "+target+" RENAME TO renamed", fixture.Diagnostic{Code: "42501", Message: "must be owner of table t"})
				failure.Prepared = true
				failure.ErrorStage = "execute"
				run(failure)
			}
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			run(fixture.Prepared(fixture.Exec("ALTER TABLE "+target+" RENAME TO renamed", 0)))
			if !backend.pg {
				run(fixture.Exec("DROP SCHEMA owned CASCADE", 0))
			}
		})
	}
}
