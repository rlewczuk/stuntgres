//go:build reference

package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/lib/pq"
	"testing"
	"time"
)

func schemaReferenceCleanup(t *testing.T, names ...string) {
	t.Helper()
	t.Cleanup(func() {
		if !referenceStarted {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		for _, name := range names {
			if _, err := referenceAdmin.ExecContext(ctx, "DROP SCHEMA IF EXISTS "+pq.QuoteIdentifier(name)+" CASCADE"); err != nil {
				t.Errorf("schema cleanup %s: %v", name, err)
			}
		}
	})
}
func schemaReferenceError(query, code, message, detail string, prepared bool, phase, position string) referenceStep {
	s := roleReferenceError(query, code, message, detail, prepared, phase)
	s.diagnostic.Position = position
	return s
}

// Creation destination diagnostics also preserve the AST target position on
// both backends without a backend-specific position override.
func schemaReferenceCreationError(query, code, message string, prepared bool, position string) referenceStep {
	s := roleReferenceError(query, code, message, "", prepared, "execute")
	s.diagnostic.Position = position
	return s
}
func TestReferenceSchemaLifecycle(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := newReferencePair(t)
			schemaReferenceCleanup(t, "sr1", "sr2", "sr_renamed", "sr_failed", "sr_nested", "sr_empty_first", "dot.schema")
			step := func(s referenceStep) { s.prepared = prepared; p.step(t, s) }
			for _, q := range []string{"CREATE SCHEMA sr1", "CREATE SCHEMA sr2", "CREATE TABLE sr1.t(id INT)", "CREATE TABLE sr2.t(id INT)", `CREATE SCHEMA "dot.schema" CREATE TABLE "dot.schema"."t.dot"(id INT)`} {
				step(referenceExec(q, 0))
			}
			step(referenceExec("INSERT INTO sr1.t VALUES(7)", 1))
			step(referenceExec("INSERT INTO sr2.t VALUES(9)", 1))
			for _, tc := range []struct {
				schema string
				value  int64
			}{{"sr1", 7}, {"sr2", 9}} {
				step(referenceSelect("SELECT id FROM "+tc.schema+".t", []string{"id"}, []string{"INT4"}, [][]any{{tc.value}}))
			}
			step(referenceExec("INSERT INTO sr2.t(id) VALUES(10)", 1))
			step(referenceSelect("SELECT id FROM sr2.t WHERE id=10 ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(10)}}))
			p.step(t, schemaReferenceError("CREATE SCHEMA sr1", "42P06", `schema "sr1" already exists`, "", prepared, "execute", ""))
			step(referenceExec("ALTER SCHEMA sr1 RENAME TO sr_renamed", 0))
			step(referenceSelect("SELECT id FROM sr_renamed.t", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
			phase := "execute"
			if prepared {
				phase = "prepare"
			}
			p.step(t, schemaReferenceError("SELECT id FROM sr1.t", "42P01", `relation "sr1.t" does not exist`, "", prepared, phase, "16"))
			restrict := schemaReferenceError("DROP SCHEMA sr_renamed", "2BP01", "cannot drop schema sr_renamed because other objects depend on it", "table sr_renamed.t depends on schema sr_renamed", prepared, "execute", "")
			restrict.diagnostic.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
			p.step(t, restrict)
			step(referenceSelect("SELECT id FROM sr_renamed.t", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
			p.step(t, schemaReferenceError("DROP SCHEMA sr_renamed,sr_absent CASCADE", "3F000", `schema "sr_absent" does not exist`, "", prepared, "execute", ""))
			step(referenceSelect("SELECT id FROM sr_renamed.t", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
			step(referenceExec("CREATE SCHEMA sr_empty_first", 0))
			batchRestrict := schemaReferenceError("DROP SCHEMA sr_empty_first,sr_renamed", "2BP01", "cannot drop desired object(s) because other objects depend on them", "table sr_renamed.t depends on schema sr_renamed", prepared, "execute", "")
			batchRestrict.diagnostic.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
			p.step(t, batchRestrict)
			step(referenceExec("CREATE TABLE sr_empty_first.probe(id INT)", 0))
			step(referenceSelect("SELECT id FROM sr_renamed.t", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
			step(referenceExec("CREATE SCHEMA sr_nested CREATE TABLE t(id INT) CREATE TABLE u(id INT)", 0))
			p.step(t, schemaReferenceError("CREATE SCHEMA sr_failed CREATE TABLE t(id INT) CREATE TABLE t(id INT)", "42P07", `relation "t" already exists`, "", prepared, "execute", ""))
			p.step(t, schemaReferenceCreationError("CREATE TABLE sr_failed.x(id INT)", "3F000", `schema "sr_failed" does not exist`, prepared, "14"))
			p.step(t, schemaReferenceError("CREATE SCHEMA sr_failed CREATE TABLE sr2.x(id INT)", "42P15", "CREATE specifies a schema (sr2) different from the one being created (sr_failed)", "", prepared, "execute", ""))
			p.step(t, schemaReferenceError("CREATE SCHEMA pg_sr", "42939", `unacceptable schema name "pg_sr"`, `The prefix "pg_" is reserved for system schemas.`, prepared, "execute", ""))
			step(referenceExec("DROP SCHEMA sr_renamed CASCADE", 0))
			step(referenceSelect("SELECT id FROM sr2.t ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(9)}, {int64(10)}}))
		})
	}
}

func TestReferenceSchemaOwnerTransfer(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			roleReferenceCleanup(t, []string{"sr_owner"})
			p := newReferencePair(t)
			schemaReferenceCleanup(t, "sr_owned")
			p.step(t, referenceExec("CREATE ROLE sr_owner NOLOGIN", 0))
			// Privileged oracle setup independently proves the destination has no CREATE.
			if _, err := referenceAdmin.ExecContext(p.ctx, "REVOKE CREATE ON DATABASE stuntgres_reference FROM sr_owner"); err != nil {
				t.Fatal(err)
			}
			var allowed bool
			if err := referenceAdmin.QueryRowContext(p.ctx, "SELECT has_database_privilege('sr_owner','stuntgres_reference','CREATE')").Scan(&allowed); err != nil || allowed {
				t.Fatal("destination CREATE", allowed, err)
			}
			p.step(t, referenceExec("CREATE SCHEMA sr_owned", 0))
			s := referenceExec("ALTER SCHEMA sr_owned OWNER TO sr_owner", 0)
			s.prepared = prepared
			p.step(t, s)
			for _, backend := range []struct {
				db     *sql.DB
				record *referenceRecorder
			}{{p.memory, p.memoryRecord}, {p.postgres, p.postgresRecord}} {
				c, err := backend.db.Conn(p.ctx)
				if err != nil {
					t.Fatal(err)
				}
				referenceRunStep(t, p.ctx, c, backend.record, referenceExec("SET SESSION AUTHORIZATION sr_owner", 0))
				referenceRunStep(t, p.ctx, c, backend.record, s)
				referenceRunStep(t, p.ctx, c, backend.record, referenceExec("RESET SESSION AUTHORIZATION", 0))
				c.Close()
			}
			p.step(t, referenceExec("DROP SCHEMA sr_owned", 0))
		})
	}
}
