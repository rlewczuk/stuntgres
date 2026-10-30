package reference_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceReplicationOriginStateTransactions(t *testing.T) {
	for _, behavior := range []string{"rollback", "orphan_reuse", "prepared", "canonical_drop", "cross_database", "late_failure", "cancel"} {
		t.Run(behavior, func(t *testing.T) {
			databases := []string{"state_origin_peer"}
			fixture.DatabaseReferenceCleanup(t, &databases)
			p := subscriptionScenario(t, "state_canonical", "state_canonical_renamed")
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					originStateCleanup(t, b.DB, "state_tx", "state_private", "state_reused", "state_pending", "state_peer_alloc")
					actor, observer := fixture.LockConnections(t, p.Ctx, b.DB)
					defer actor.ExecContext(context.Background(), "ROLLBACK")
					run := func(e fixture.Executor, s fixture.Step) { originStateRun(t, p, b, e, s) }
					switch behavior {
					case "rollback":
						id := originStateCreate(t, p, b, observer, "state_tx")
						run(observer, fixture.Exec("SELECT pg_replication_origin_advance('state_tx','80000000/0')", 1))
						run(actor, fixture.Exec("BEGIN", 0))
						run(actor, fixture.Exec("SAVEPOINT before_advance", 0))
						run(actor, fixture.Exec("SELECT pg_replication_origin_advance('state_tx','0/3')", 1))
						originStateProgress(t, p, b, observer, "state_tx", "0/3")
						run(actor, fixture.Exec("ROLLBACK TO before_advance", 0))
						originStateProgress(t, p, b, observer, "state_tx", "0/3")
						run(actor, fixture.Exec("SELECT pg_replication_origin_drop('state_tx')", 1))
						originStateProgress(t, p, b, observer, "state_tx", nil)
						run(actor, fixture.Exec("ROLLBACK", 0))
						if originStateID(t, p, observer, "state_tx") != id {
							t.Fatal("rollback lost identity")
						}
						originStateProgress(t, p, b, observer, "state_tx", nil)
						run(actor, fixture.Exec("BEGIN", 0))
						originStateCreate(t, p, b, actor, "state_private")
						run(observer, originStateScalar("SELECT pg_replication_origin_oid('state_private') AS v", "OID", nil))
						run(actor, fixture.Exec("SAVEPOINT private_origin", 0))
						run(actor, fixture.Exec("SELECT pg_replication_origin_advance('state_private','0/8')", 1))
						run(actor, fixture.Exec("ROLLBACK TO private_origin", 0))
						originStateProgress(t, p, b, actor, "state_private", "0/8")
						run(actor, fixture.Exec("ROLLBACK", 0))
						run(observer, originStateScalar("SELECT pg_replication_origin_oid('state_private') AS v", "OID", nil))
						// Retire the orphan numeric slot explicitly by reusing/dropping its ID.
						originStateCreate(t, p, b, observer, "state_private")
						run(observer, fixture.Exec("SELECT pg_replication_origin_drop('state_private')", 1))
					case "orphan_reuse":
						run(actor, fixture.Exec("BEGIN", 0))
						id := originStateCreate(t, p, b, actor, "state_private")
						run(actor, fixture.Exec("SELECT pg_replication_origin_advance('state_private','80000000/2')", 1))
						run(actor, fixture.Exec("ROLLBACK", 0))
						reused := originStateCreate(t, p, b, observer, "state_reused")
						if reused != id {
							t.Fatal("rollback did not release first-free identity", id, reused)
						}
						originStateProgress(t, p, b, observer, "state_reused", "80000000/2")
					case "prepared":
						for _, commit := range []bool{false, true} {
							gid := fmt.Sprintf("state_origin_prepared_%t", commit)
							fixture.PreparedReferenceCleanup(t, b, gid)
							run(actor, fixture.Exec("BEGIN", 0))
							id := originStateCreate(t, p, b, actor, "state_pending")
							run(actor, fixture.Exec("PREPARE TRANSACTION '"+gid+"'", 0))
							run(observer, originStateScalar("SELECT pg_replication_origin_oid('state_pending') AS v", "OID", nil))
							other := originStateCreate(t, p, b, observer, "state_peer_alloc")
							if other == id {
								t.Fatal("prepared exclusion stolen")
							}
							ending := "ROLLBACK"
							if commit {
								ending = "COMMIT"
							}
							run(observer, fixture.Exec(ending+" PREPARED '"+gid+"'", 0))
							if commit {
								if originStateID(t, p, observer, "state_pending") != id {
									t.Fatal("prepared identity changed")
								}
								run(observer, fixture.Exec("SELECT pg_replication_origin_drop('state_pending')", 1))
							}
							run(observer, fixture.Exec("SELECT pg_replication_origin_drop('state_peer_alloc')", 1))
						}
					case "canonical_drop":
						originCreate(t, p, b, observer, "state_canonical", false)
						id, canonical := originIdentity(t, p, observer, "state_canonical")
						run(observer, fixture.Exec("SELECT pg_replication_origin_advance($1::text,'0/9')", 1, canonical))
						run(observer, fixture.Exec("SELECT pg_replication_origin_drop($1::text)", 1, canonical))
						run(observer, originStateScalar("SELECT count(*) AS v FROM pg_subscription WHERE subname='state_canonical'", "INT8", int64(1)))
						run(observer, fixture.Exec("ALTER SUBSCRIPTION state_canonical RENAME TO state_canonical_renamed", 0))
						run(observer, fixture.Exec("ALTER SUBSCRIPTION state_canonical_renamed OWNER TO postgres", 0))
						run(observer, originStateScalar("SELECT pg_replication_origin_oid($1::text) AS v", "OID", nil, canonical))
						reused := originStateCreate(t, p, b, observer, "state_reused")
						if reused != id {
							t.Fatal("canonical drop did not release identity", id, reused)
						}
						run(observer, fixture.Exec("DROP SUBSCRIPTION state_canonical_renamed", 0))
						if originStateID(t, p, observer, "state_reused") != reused {
							t.Fatal("subscription cleanup removed reused unrelated ID")
						}
						originStateProgress(t, p, b, observer, "state_reused", nil)
					case "cross_database":
						run(observer, fixture.Exec("CREATE DATABASE state_origin_peer", 0))
						peer, _, recorder := fixture.DatabaseScenarioPool(t, p, b.PG, "state_origin_peer", "")
						pb := b
						pb.DB = peer
						pb.Recorder = recorder
						id := originStateCreate(t, p, b, observer, "state_tx")
						if originStateID(t, p, peer, "state_tx") != id {
							t.Fatal("cluster registration not shared")
						}
						run(actor, fixture.Exec("BEGIN", 0))
						run(actor, fixture.Exec("SELECT pg_replication_origin_advance('state_tx','80000000/3')", 1))
						originStateProgress(t, p, pb, peer, "state_tx", "80000000/3")
						originStateCreate(t, p, b, actor, "state_private")
						originStateRun(t, p, pb, peer, originStateScalar("SELECT pg_replication_origin_oid('state_private') AS v", "OID", nil))
						run(actor, fixture.Exec("ROLLBACK", 0))
						originStateProgress(t, p, pb, peer, "state_tx", "80000000/3")
					case "late_failure":
						originStateCreate(t, p, b, observer, "state_tx")
						run(observer, fixture.Exec("CREATE TABLE state_runtime_failure(n integer)", 0))
						run(observer, fixture.Exec("INSERT INTO state_runtime_failure VALUES(1)", 1))
						s := originStateError("SELECT pg_replication_origin_advance('state_tx','0/4'),1/(n-n) FROM state_runtime_failure", "22012", "division by zero", "")
						run(observer, s)
						originStateProgress(t, p, b, observer, "state_tx", "0/4")
						run(observer, fixture.Exec("DO $$BEGIN BEGIN PERFORM pg_replication_origin_create('state_private'); PERFORM pg_replication_origin_advance('state_private','0/5'); RAISE EXCEPTION 'rollback identity'; EXCEPTION WHEN OTHERS THEN PERFORM 1; END; END$$", 0))
						run(observer, originStateScalar("SELECT pg_replication_origin_oid('state_private') AS v", "OID", nil))
						originStateCreate(t, p, b, observer, "state_private")
						originStateProgress(t, p, b, observer, "state_private", "0/5")
					case "cancel":
						holder := int(originStateNumber(t, p, actor, "SELECT pg_backend_pid()"))
						waiter := int(originStateNumber(t, p, observer, "SELECT pg_backend_pid()"))
						run(actor, fixture.Exec("BEGIN", 0))
						originStateCreate(t, p, b, actor, "state_pending")
						ctx, cancel := context.WithCancel(p.Ctx)
						defer cancel()
						finished := make(chan struct{})
						join := fixture.LockOperation(t, ctx, func(ctx context.Context) fixture.RelationOutcome {
							defer close(finished)
							result, err := observer.ExecContext(ctx, "SELECT pg_replication_origin_create('state_pending')")
							return fixture.RelationOutcome{Result: result, Err: err}
						})
						fixture.RelationLockPending(t, p, b, waiter, holder, 0, "", finished)
						cancel()
						outcome := join()
						if b.PG {
							fixture.DefinitionIndexWaitDiagnostic(t, outcome.Err, "57014", "canceling statement due to user request", "pg_replication_origin_roname_index", true)
						} else if !errors.Is(outcome.Err, context.Canceled) {
							t.Fatal(outcome.Err)
						}
						run(actor, fixture.Exec("ROLLBACK", 0))
						run(b.DB, originStateScalar("SELECT pg_replication_origin_oid('state_pending') AS v", "OID", nil))
						originStateCreate(t, p, b, b.DB, "state_pending")
						if !b.PG {
							ctx, cancel := context.WithCancel(p.Ctx)
							cancel()
							if _, err := b.DB.ExecContext(ctx, "SELECT pg_replication_origin_advance('state_pending','0/9')"); !errors.Is(err, context.Canceled) {
								t.Fatal(err)
							}
							originStateProgress(t, p, b, b.DB, "state_pending", nil)
						}
					}
				})
			}
		})
	}
}
func TestReferenceReplicationOriginStateCapacity(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			names := make([]string, 11)
			ids := make([]int64, 11)
			for i := range names {
				names[i] = fmt.Sprintf("state_capacity_%d", i)
			}
			originStateCleanup(t, b.DB, names...)
			for i, name := range names {
				ids[i] = originStateCreate(t, p, b, b.DB, name)
				originStateProgress(t, p, b, b.DB, name, nil)
			}
			for i, name := range names[:10] {
				position := "0/1"
				if i == 0 {
					position = "0/0"
				}
				originStateRun(t, p, b, b.DB, fixture.Exec("SELECT pg_replication_origin_advance($1::text,$2::pg_lsn)", 1, name, position))
			}
			originStateProgress(t, p, b, b.DB, names[10], nil)
			want := fixture.Diagnostic{Code: "53400", Message: fmt.Sprintf("could not find free replication state slot for replication origin with ID %d", ids[10]), Hint: `Increase "max_active_replication_origins" and try again.`}
			q := "SELECT pg_replication_origin_advance($1::text,'0/2')"
			s := fixture.ImplicitError(q, want, names[10])
			originStateRun(t, p, b, b.DB, s)
			originStateRun(t, p, b, b.DB, fixture.Prepared(s))
			originStateProgress(t, p, b, b.DB, names[0], nil)
			originStateProgress(t, p, b, b.DB, names[10], nil)
			originStateRun(t, p, b, b.DB, fixture.Exec("SELECT pg_replication_origin_advance($1::text,'0/3')", 1, names[1]))
			originStateRun(t, p, b, b.DB, fixture.Exec("SELECT pg_replication_origin_advance($1::text,'0/1')", 1, names[1]))
			originStateProgress(t, p, b, b.DB, names[1], "0/1")
			originStateRun(t, p, b, b.DB, fixture.Exec("SELECT pg_replication_origin_drop($1::text)", 1, names[0]))
			originStateRun(t, p, b, b.DB, fixture.Exec(q, 1, names[10]))
			originStateProgress(t, p, b, b.DB, names[10], "0/2")
		})
	}
}
