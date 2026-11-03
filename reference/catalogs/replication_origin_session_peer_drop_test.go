package reference_test

import (
	"context"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

// The same already-parked SQL DROP, not a new request after reset, must finish
// subscription staging, dependency removal and publication after owner release.
func originSessionPeerDropWait(t *testing.T) {
	t.Helper()
	for _, ending := range []string{"reset", "physical_end", "cancel"} {
		t.Run("subscription_peer_"+ending, func(t *testing.T) {
			databases := []string{"session_wait_owner"}
			fixture.DatabaseReferenceCleanup(t, &databases)
			p := subscriptionScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					originStateRun(t, p, b, b.DB, fixture.Exec("CREATE DATABASE session_wait_owner", 0))
					ownerPool, _, ownerRecorder := fixture.DatabaseScenarioPool(t, p, b.PG, "session_wait_owner", "")
					ownerPool.SetMaxOpenConns(1)
					ownerPool.SetMaxIdleConns(1)
					peerPool, _, recorder := fixture.DatabaseScenarioPool(t, p, b.PG, "session_wait_owner", "")
					pb, ob := b, b
					pb.DB, pb.Recorder = peerPool, recorder
					ob.DB, ob.Recorder = ownerPool, ownerRecorder
					const sub = "session_peer_wait_sub"
					originSessionSubscriptionCleanup(t, peerPool, sub)
					originCreate(t, p, pb, peerPool, sub, false)
					id, name := originIdentity(t, p, peerPool, sub)
					subID := originStateNumber(t, p, peerPool, "SELECT oid FROM pg_subscription WHERE subname='session_peer_wait_sub'")
					owner, err := ownerPool.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					ownerLive := true
					t.Cleanup(func() {
						if !ownerLive {
							return
						}
						ctx, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
						defer cancel()
						var selected bool
						if err := owner.QueryRowContext(ctx, "SELECT pg_replication_origin_session_is_setup()").Scan(&selected); err != nil {
							t.Error(err)
						} else if selected {
							if _, err := owner.ExecContext(ctx, "SELECT pg_replication_origin_session_reset()"); err != nil {
								t.Error(err)
							}
						}
						if err := owner.Close(); err != nil {
							t.Error(err)
						}
						if err := ownerPool.Close(); err != nil {
							t.Error(err)
						}
					})
					originSessionSetup(t, p, ob, owner, name)
					originSessionXact(t, p, ob, owner, "0/10")
					originStateRun(t, p, ob, owner, fixture.Exec("SELECT pg_current_xact_id()", 1))
					originSessionProgress(t, p, ob, owner, "0/10")
					ownerPID := fixture.LockPID(t, p.Ctx, owner)
					dropper := originSessionConn(t, p, peerPool)
					pid := fixture.LockPID(t, p.Ctx, dropper)
					const query = "DROP SUBSCRIPTION session_peer_wait_sub"
					ctx, cancel := context.WithCancel(p.Ctx)
					defer cancel()
					completed := make(chan struct{})
					join := fixture.LockOperation(t, ctx, func(ctx context.Context) fixture.RelationOutcome {
						defer close(completed)
						result, err := dropper.ExecContext(ctx, query)
						return fixture.RelationOutcome{Result: result, Err: err}
					})
					fixture.ReplicationOriginDropWait(t, p, pb, pid, query, completed)
					switch ending {
					case "reset":
						originStateRun(t, p, ob, owner, fixture.Exec("SELECT pg_replication_origin_session_reset()", 1))
					case "physical_end":
						if err := owner.Close(); err != nil {
							t.Fatal(err)
						}
						ownerLive = false
						if err := ownerPool.Close(); err != nil {
							t.Fatal(err)
						}
					case "cancel":
						if b.PG {
							var signaled bool
							if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT pg_cancel_backend($1)", pid).Scan(&signaled); err != nil || !signaled {
								t.Fatal("DROP backend cancellation", signaled, err)
							}
						} else {
							cancel()
						}
					}
					outcome := join()
					if ending == "cancel" {
						if outcome.Result != nil || outcome.Rows != nil {
							t.Fatal("canceled DROP published a result")
						}
						if b.PG {
							fixture.AssertDiagnostic(t, outcome.Err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "57014", Message: "canceling statement due to user request"})
						} else {
							fixture.LexicalOrdinary(t, outcome.Err, context.Canceled)
						}
						originCheck(t, p, pb, peerPool, sub, id, name)
						originSessionIsSetup(t, p, ob, owner, true)
						originSessionProgress(t, p, ob, owner, "0/10")
						originStateRun(t, p, pb, dropper, originStateError("SELECT pg_replication_origin_session_setup($1::text)", "55006", fmt.Sprintf("replication origin with ID %d is already active for PID %d", id, ownerPID), "", name))
						originSessionDropSlotCapacity(t, p, pb, true)
						originSessionProgress(t, p, ob, owner, "0/10")
						return
					}
					if outcome.Err != nil || outcome.Result == nil || outcome.Rows != nil {
						t.Fatal("resumed SQL DROP did not finish", outcome.Err)
					}
					if n, err := outcome.Result.RowsAffected(); err != nil || n != 0 {
						t.Fatal("resumed SQL DROP command result", n, err)
					}
					originCheck(t, p, pb, peerPool, sub, 0, "")
					originStateRun(t, p, pb, peerPool, originStateScalar("SELECT count(*) AS v FROM pg_subscription WHERE oid=$1", "INT8", int64(0), subID))
					originStateRun(t, p, pb, peerPool, originStateScalar("SELECT pg_replication_origin_oid($1::text) AS v", "OID", nil, name))
					originSessionDropSlotCapacity(t, p, pb, false)
					for _, catalog := range []string{"pg_depend", "pg_shdepend"} {
						originStateRun(t, p, pb, peerPool, originStateScalar("SELECT count(*) AS v FROM "+catalog+" WHERE classid=6100 AND objid=$1", "INT8", int64(0), subID))
					}
				})
			}
		})
	}
}

// Public progress allocation independently observes the fixture's ten-slot limit:
// a canceled DROP leaves its owner's slot occupied, whereas a completed DROP
// leaves all ten slots available. Registration alone does not allocate a slot.
func originSessionDropSlotCapacity(t *testing.T, p *fixture.Pair, b fixture.Backend, retainedOwner bool) {
	t.Helper()
	names := make([]string, 11)
	for i := range names {
		names[i] = fmt.Sprintf("session_drop_probe_%d", i)
	}
	originStateCleanup(t, b.DB, names...)
	for _, name := range names {
		originStateCreate(t, p, b, b.DB, name)
		originStateProgress(t, p, b, b.DB, name, nil)
	}
	free := 10
	if retainedOwner {
		free--
	}
	for _, name := range names[:free] {
		originStateRun(t, p, b, b.DB, fixture.Exec("SELECT pg_replication_origin_advance($1::text,'0/1')", 1, name))
		originStateProgress(t, p, b, b.DB, name, "0/1")
	}
	originReplacementFull(t, p, b, b.DB, names[free])
	originStateProgress(t, p, b, b.DB, names[free], nil)
	// The same previously refused allocation succeeds when exactly one occupied
	// probe is dropped; neither failure nor this probe may consume the owner slot.
	originStateRun(t, p, b, b.DB, fixture.Exec("SELECT pg_replication_origin_drop($1::text)", 1, names[0]))
	originStateRun(t, p, b, b.DB, fixture.Exec("SELECT pg_replication_origin_advance($1::text,'0/1')", 1, names[free]))
	originStateProgress(t, p, b, b.DB, names[free], "0/1")
}
