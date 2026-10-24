package reference_test

import (
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTransactionalIndexMaintenanceConcurrentDML(t *testing.T) {
	for _, action := range []string{"rename", "set", "reset"} {
		for _, route := range []string{"direct", "held"} {
			t.Run(fmt.Sprintf("%s/%s", action, route), func(t *testing.T) {
				fixture.RoleReferenceCleanup(t, []string{"imtx_concurrent_peer"})
				p := fixture.NewScenario(t)
				for _, b := range p.Backends() {
					t.Run(b.Name, func(t *testing.T) {
						owner, peer := fixture.LockConnections(t, p.Ctx, b.DB)
						run := transactionalIndexRun(t, p, b, owner)
						other := transactionalIndexRun(t, p, b, peer)
						indexTxMaintenanceSeed(run, action)
						run(fixture.Exec("CREATE UNIQUE INDEX sibling ON t(s)", 0))
						var oid int64
						if err := owner.QueryRowContext(p.Ctx, "SELECT 'ix'::regclass::oid::bigint").Scan(&oid); err != nil {
							t.Fatal(err)
						}
						q := indexTxMaintenanceSQL(action)
						var execute fixture.Executor = owner
						if route == "held" {
							stmt, err := owner.PrepareContext(p.Ctx, q)
							if err != nil {
								t.Fatal(err)
							}
							defer stmt.Close()
							execute = fixture.HeldStatement{Executor: owner, Statement: stmt, SQL: q}
						}
						run(fixture.Exec("BEGIN", 0))
						defer owner.ExecContext(p.Ctx, "ROLLBACK")
						fixture.RunStep(t, p.Ctx, execute, b.Recorder, fixture.Exec(q, 0))
						run(indexTxMaintenanceState(action, true))
						other(indexTxMaintenanceState(action, false))
						// SUE maintenance permits this same-parent RowExclusive writer.
						// These commits must not become stale heap/auth commit fences.
						other(fixture.Exec("INSERT INTO t VALUES(3,'peer')", 1))
						other(fixture.Exec("CREATE ROLE imtx_concurrent_peer", 0))
						run(fixture.Exec("INSERT INTO t VALUES(4,'owner')", 1))
						run(fixture.Exec("COMMIT", 0))
						other(indexTxMaintenanceState(action, true))
						other(fixture.Select("SELECT oid=$1::oid AS same FROM pg_class WHERE relnamespace='public'::regnamespace AND relname IN ('ix','fresh')", []string{"same"}, []string{"BOOL"}, [][]any{{true}}, oid))
						other(transactionalIndexName("sibling", true))
						other(fixture.Select("SELECT n,s FROM t ORDER BY n NULLS LAST", []string{"n", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}, {int64(2), "b"}, {int64(3), "peer"}, {int64(4), "owner"}, {nil, nil}}))
						name := "ix"
						if action == "rename" {
							name = "fresh"
						}
						other(fixture.ErrorDiagnostic("INSERT INTO t VALUES(3,'different')", fixture.UniqueReferenceDuplicate("t", name, "Key (n)=(3) already exists.")))
						other(fixture.ErrorDiagnostic("INSERT INTO t VALUES(5,'owner')", fixture.UniqueReferenceDuplicate("t", "sibling", "Key (s)=(owner) already exists.")))
						other(fixture.Exec("INSERT INTO t VALUES(5,'healthy')", 1))
					})
				}
			})
		}
	}
}
