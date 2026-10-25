//go:build reference

package reference_test

import (
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSchemaLifecycle(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := fixture.NewPair(t)
			fixture.SchemaReferenceCleanup(t, "sr1", "sr2", "sr_renamed", "sr_failed", "sr_nested", "sr_empty_first", "dot.schema")
			step := func(s fixture.Step) { s.Prepared = prepared; p.Step(t, s) }
			for _, q := range []string{"CREATE SCHEMA sr1", "CREATE SCHEMA sr2", "CREATE TABLE sr1.t(id INT)", "CREATE TABLE sr2.t(id INT)", `CREATE SCHEMA "dot.schema" CREATE TABLE "dot.schema"."t.dot"(id INT)`} {
				step(fixture.Exec(q, 0))
			}
			step(fixture.Exec("INSERT INTO sr1.t VALUES(7)", 1))
			step(fixture.Exec("INSERT INTO sr2.t VALUES(9)", 1))
			for _, tc := range []struct {
				schema string
				value  int64
			}{{"sr1", 7}, {"sr2", 9}} {
				step(fixture.Select("SELECT id FROM "+tc.schema+".t", []string{"id"}, []string{"INT4"}, [][]any{{tc.value}}))
			}
			step(fixture.Exec("INSERT INTO sr2.t(id) VALUES(10)", 1))
			step(fixture.Select("SELECT id FROM sr2.t WHERE id=10 ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(10)}}))
			p.Step(t, fixture.SchemaReferenceError("CREATE SCHEMA sr1", "42P06", `schema "sr1" already exists`, "", prepared, "execute", ""))
			step(fixture.Exec("ALTER SCHEMA sr1 RENAME TO sr_renamed", 0))
			step(fixture.Select("SELECT id FROM sr_renamed.t", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
			phase := "execute"
			if prepared {
				phase = "prepare"
			}
			p.Step(t, fixture.SchemaReferenceError("SELECT id FROM sr1.t", "42P01", `relation "sr1.t" does not exist`, "", prepared, phase, "16"))
			restrict := fixture.SchemaReferenceError("DROP SCHEMA sr_renamed", "2BP01", "cannot drop schema sr_renamed because other objects depend on it", "table sr_renamed.t depends on schema sr_renamed", prepared, "execute", "")
			restrict.Diagnostic.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
			p.Step(t, restrict)
			step(fixture.Select("SELECT id FROM sr_renamed.t", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
			p.Step(t, fixture.SchemaReferenceError("DROP SCHEMA sr_renamed,sr_absent CASCADE", "3F000", `schema "sr_absent" does not exist`, "", prepared, "execute", ""))
			step(fixture.Select("SELECT id FROM sr_renamed.t", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
			step(fixture.Exec("CREATE SCHEMA sr_empty_first", 0))
			batchRestrict := fixture.SchemaReferenceError("DROP SCHEMA sr_empty_first,sr_renamed", "2BP01", "cannot drop desired object(s) because other objects depend on them", "table sr_renamed.t depends on schema sr_renamed", prepared, "execute", "")
			batchRestrict.Diagnostic.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
			p.Step(t, batchRestrict)
			step(fixture.Exec("CREATE TABLE sr_empty_first.probe(id INT)", 0))
			step(fixture.Select("SELECT id FROM sr_renamed.t", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
			step(fixture.Exec("CREATE SCHEMA sr_nested CREATE TABLE t(id INT) CREATE TABLE u(id INT)", 0))
			p.Step(t, fixture.SchemaReferenceError("CREATE SCHEMA sr_failed CREATE TABLE t(id INT) CREATE TABLE t(id INT)", "42P07", `relation "t" already exists`, "", prepared, "execute", ""))
			p.Step(t, fixture.SchemaReferenceCreationError("CREATE TABLE sr_failed.x(id INT)", "3F000", `schema "sr_failed" does not exist`, prepared, "14"))
			p.Step(t, fixture.SchemaReferenceError("CREATE SCHEMA sr_failed CREATE TABLE sr2.x(id INT)", "42P15", "CREATE specifies a schema (sr2) different from the one being created (sr_failed)", "", prepared, "execute", ""))
			p.Step(t, fixture.SchemaReferenceError("CREATE SCHEMA pg_sr", "42939", `unacceptable schema name "pg_sr"`, `The prefix "pg_" is reserved for system schemas.`, prepared, "execute", ""))
			step(fixture.Exec("DROP SCHEMA sr_renamed CASCADE", 0))
			step(fixture.Select("SELECT id FROM sr2.t ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(9)}, {int64(10)}}))
		})
	}
}

func TestReferenceSchemaOwnerTransfer(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"sr_owner"})
			p := fixture.NewPair(t)
			fixture.SchemaReferenceCleanup(t, "sr_owned")
			p.Step(t, fixture.Exec("CREATE ROLE sr_owner NOLOGIN", 0))
			// Privileged oracle setup independently proves the destination has no CREATE.
			if _, err := fixture.Admin.ExecContext(p.Ctx, "REVOKE CREATE ON DATABASE stuntgres_reference FROM sr_owner"); err != nil {
				t.Fatal(err)
			}
			var allowed bool
			if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT has_database_privilege('sr_owner','stuntgres_reference','CREATE')").Scan(&allowed); err != nil || allowed {
				t.Fatal("destination CREATE", allowed, err)
			}
			p.Step(t, fixture.Exec("CREATE SCHEMA sr_owned", 0))
			s := fixture.Exec("ALTER SCHEMA sr_owned OWNER TO sr_owner", 0)
			s.Prepared = prepared
			p.Step(t, s)
			for _, backend := range []struct {
				db     *sql.DB
				record *fixture.Recorder
			}{{p.Memory, p.MemoryRecord}, {p.Postgres, p.PostgresRecord}} {
				c, err := backend.db.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				fixture.RunStep(t, p.Ctx, c, backend.record, fixture.Exec("SET SESSION AUTHORIZATION sr_owner", 0))
				fixture.RunStep(t, p.Ctx, c, backend.record, s)
				fixture.RunStep(t, p.Ctx, c, backend.record, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
				c.Close()
			}
			p.Step(t, fixture.Exec("DROP SCHEMA sr_owned", 0))
		})
	}
}
