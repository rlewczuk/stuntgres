package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func transactionalRoutineNamespaceRetirement(t *testing.T) {
	for _, family := range []string{"function", "aggregate", "operator"} {
		for _, commit := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/commit=%v", family, commit), func(t *testing.T) {
				fixture.SchemaReferenceCleanup(t, "txr_namespace")
				routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					routineExec(t, p, b, c, "CREATE SCHEMA txr_namespace")
					ns := transactionalRoutineOID(t, p, c, "SELECT 'txr_namespace'::regnamespace::oid")
					peer, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer peer.Close()
					pidC, pidP := 0, 0
					if b.PG {
						pidC, pidP = fixture.LockPID(t, p.Ctx, c), fixture.LockPID(t, p.Ctx, peer)
					}
					create := "CREATE FUNCTION txr_namespace.f(integer) RETURNS integer RETURN $1+1"
					if family == "aggregate" {
						create = "CREATE AGGREGATE txr_namespace.f(integer) (SFUNC=pg_catalog.int4pl,STYPE=integer,INITCOND='0')"
					}
					if family == "operator" {
						create = "CREATE OPERATOR txr_namespace.#~# (LEFTARG=int4,RIGHTARG=int4,FUNCTION=pg_catalog.int4pl)"
					}
					routineExec(t, p, b, c, "BEGIN", create)
					defer c.ExecContext(p.Ctx, "ROLLBACK")
					ctx, cancel := context.WithCancel(p.Ctx)
					defer cancel()
					finished := make(chan struct{})
					join := fixture.LockOperation(t, ctx, func(ctx context.Context) fixture.RelationOutcome {
						defer close(finished)
						r, err := peer.ExecContext(ctx, "DROP SCHEMA txr_namespace CASCADE")
						return fixture.RelationOutcome{Result: r, Err: err}
					})
					joined := false
					defer func() {
						cancel()
						if !joined {
							join()
						}
					}()
					transactionalRoutineObjectWait(t, p, b, pidP, pidC, 2615, ns, finished)
					end := "ROLLBACK"
					if commit {
						end = "COMMIT"
					}
					routineExec(t, p, b, c, end)
					o := join()
					joined = true
					if o.Err != nil {
						t.Fatal(o.Err)
					}
					if n, err := o.Result.RowsAffected(); err != nil || n != 0 {
						t.Fatal(n, err)
					}
					transactionalRoutineCount(t, p, b, c, "SELECT count(*) AS v FROM pg_namespace WHERE nspname='txr_namespace'", 0)
					transactionalRoutineCount(t, p, b, c, fmt.Sprintf("SELECT count(*) AS v FROM pg_proc WHERE pronamespace=%d::oid", ns), 0)
					transactionalRoutineCount(t, p, b, c, fmt.Sprintf("SELECT count(*) AS v FROM pg_operator WHERE oprnamespace=%d::oid", ns), 0)
					// The namespace retirement cannot leave a callable orphan or make
					// the same physical owner's next ordinary creation unhealthy.
					routineExec(t, p, b, c, "CREATE FUNCTION txr_namespace_healthy() RETURNS integer RETURN 5")
					routineValue(t, p, b, c, "SELECT txr_namespace_healthy() AS v", "v", "INT4", int64(5))
				})
			})
		}
	}
}
