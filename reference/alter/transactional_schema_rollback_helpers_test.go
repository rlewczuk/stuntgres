package reference_test

import (
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

// Each control/held combination has a fresh scenario and independent committed
// predecessor. Rollback must restore identity and ACL, not recreate by name.
func transactionalSchemaRollbackRoutes(t *testing.T, schema bool) {
	for _, goTx := range []bool{false, true} {
		for _, held := range []bool{false, true} {
			for _, commit := range []bool{false, true} {
				t.Run(fmt.Sprintf("predecessor/schema=%t/go=%t/held=%t/commit=%t", schema, goTx, held, commit), func(t *testing.T) {
					fixture.RoleReferenceCleanup(t, []string{"tx_predecessor_reader"})
					fixture.SchemaReferenceCleanup(t, "tx_before", "tx_after")
					p := fixture.NewScenario(t)
					for _, b := range p.Backends() {
						t.Run(b.Name, func(t *testing.T) {
							c, peer := fixture.LockConnections(t, p.Ctx, b.DB)
							for _, q := range []string{"CREATE ROLE tx_predecessor_reader", "CREATE SCHEMA tx_before CREATE TABLE original(v INT)", "GRANT USAGE ON SCHEMA tx_before TO tx_predecessor_reader", "GRANT SELECT ON tx_before.original TO tx_predecessor_reader"} {
								fixture.LockExec(t, p.Ctx, c, q, 0)
							}
							fixture.LockExec(t, p.Ctx, c, "INSERT INTO tx_before.original VALUES(6)", 1)
							var oldOID int64
							var oldACL string
							if err := c.QueryRowContext(p.Ctx, "SELECT oid,relacl::text FROM pg_class WHERE oid='tx_before.original'::regclass").Scan(&oldOID, &oldACL); err != nil {
								t.Fatal(err)
							}
							var e fixture.Executor = c
							var tx *sql.Tx
							if goTx {
								var err error
								tx, err = c.BeginTx(p.Ctx, nil)
								if err != nil {
									t.Fatal(err)
								}
								defer tx.Rollback()
								e = tx
							} else {
								fixture.LockExec(t, p.Ctx, c, "BEGIN", 0)
								defer c.ExecContext(p.Ctx, "ROLLBACK")
							}
							q := "DROP TABLE tx_before.original"
							if schema {
								q = "ALTER SCHEMA tx_before RENAME TO tx_after"
							}
							transactionalSchemaRun(t, p, b, e, held, fixture.Exec(q, 0))
							fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Select("SELECT count(*) FROM pg_class WHERE oid=$1", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}, oldOID))
							if goTx {
								var err error
								if commit {
									err = tx.Commit()
								} else {
									err = tx.Rollback()
								}
								if err != nil {
									t.Fatal(err)
								}
							} else {
								end := "ROLLBACK"
								if commit {
									end = "COMMIT"
								}
								fixture.LockExec(t, p.Ctx, c, end, 0)
							}
							if !schema && commit {
								fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Select("SELECT count(*) FROM pg_class WHERE oid=$1", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}, oldOID))
							} else {
								ns := "tx_before"
								if schema && commit {
									ns = "tx_after"
								}
								var oid int64
								var acl string
								if err := peer.QueryRowContext(p.Ctx, "SELECT oid,relacl::text FROM pg_class WHERE oid=$1::regclass", ns+".original").Scan(&oid, &acl); err != nil || oid != oldOID || acl != oldACL {
									t.Fatal(oldOID, oid, oldACL, acl, err)
								}
								fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Select("SELECT v FROM "+ns+".original", []string{"v"}, []string{"INT4"}, [][]any{{int64(6)}}))
							}
						})
					}
				})
			}
		}
	}
}
