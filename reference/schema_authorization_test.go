//go:build reference

package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceSchemaAuthorization(t *testing.T) {
	roleReferenceCleanup(t, []string{"sa_owner", "sa_other"})
	p := newReferencePair(t)
	schemaReferenceCleanup(t, "sa_owned", "sa_other_schema")
	for _, q := range []string{"CREATE USER sa_owner", "CREATE USER sa_other", "CREATE SCHEMA sa_owned AUTHORIZATION sa_owner CREATE TABLE t(id INT)", "CREATE SCHEMA sa_other_schema AUTHORIZATION sa_other CREATE TABLE t(id INT)"} {
		p.step(t, referenceExec(q, 0))
	}
	p.step(t, referenceExec("INSERT INTO sa_other_schema.t VALUES(9)", 1))
	// Ordinary-role database CREATE is absent on both fixtures. Native has no ACL.
	if _, err := referenceAdmin.ExecContext(p.ctx, "REVOKE CREATE ON DATABASE stuntgres_reference FROM PUBLIC,sa_owner,sa_other"); err != nil {
		t.Fatal(err)
	}
	for _, backend := range []struct {
		name     string
		db       *sql.DB
		record   *referenceRecorder
		database string
	}{{"memory", p.memory, p.memoryRecord, "stuntgres"}, {"postgres", p.postgres, p.postgresRecord, "stuntgres_reference"}} {
		t.Run(backend.name, func(t *testing.T) {
			c, err := backend.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, backend.record, s) }
			run(referenceExec("SET SESSION AUTHORIZATION sa_owner", 0))
			run(referencePrepared(referenceExec("CREATE TABLE sa_owned.u(id INT)", 0)))
			run(referenceExec("INSERT INTO sa_owned.t VALUES(7)", 1))
			run(referencePrepared(referenceSelect("SELECT id FROM sa_owned.t", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}})))
			s := roleReferenceError("CREATE SCHEMA sa_denied", "42501", "permission denied for database "+backend.database, "", true, "execute")
			run(s)
			run(roleReferenceError("ALTER SCHEMA sa_owned RENAME TO sa_new", "42501", "permission denied for database "+backend.database, "", true, "execute"))
			denied := roleReferenceError("SELECT id FROM sa_other_schema.t", "42501", "permission denied for schema sa_other_schema", "", true, "prepare")
			denied.diagnostic.Position = "16"
			run(denied)
			run(referenceExec("SET search_path TO sa_other_schema,sa_owned", 0))
			run(referenceSelect("SELECT id FROM t", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
			run(referenceSelect("SELECT current_schema()", []string{"current_schema"}, []string{"NAME"}, [][]any{{"sa_owned"}}))
			run(referencePrepared(referenceExec("ALTER SCHEMA sa_owned OWNER TO sa_owner", 0)))
			for _, prepared := range []bool{false, true} {
				run(roleReferenceError("DROP SCHEMA sa_owned,sa_other_schema CASCADE", "42501", "must be owner of schema sa_other_schema", "", prepared, "execute"))
				run(referencePrepared(referenceSelect("SELECT id FROM sa_owned.t", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}})))
			}
			run(referenceExec("DROP SCHEMA sa_owned CASCADE", 0))
			run(referenceExec("RESET SESSION AUTHORIZATION", 0))
			run(referenceSelect("SELECT id FROM sa_other_schema.t", []string{"id"}, []string{"INT4"}, [][]any{{int64(9)}}))
		})
	}
}
