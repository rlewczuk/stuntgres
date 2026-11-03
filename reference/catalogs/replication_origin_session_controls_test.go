package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func originSessionSubscriptionCleanup(t *testing.T, db *sql.DB, names ...string) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
		defer cancel()
		for _, name := range names {
			if _, err := db.ExecContext(ctx, "DROP SUBSCRIPTION IF EXISTS "+name); err != nil {
				t.Error(err)
			}
		}
	})
}

func TestReferenceReplicationOriginSessionControls(t *testing.T) {
	originSessionDropWait(t)
	originSessionPeerDropWait(t)
	originSessionOrphanControls(t)
	for _, control := range []string{"reset", "restore"} {
		t.Run(control, func(t *testing.T) {
			databases := []string{"session_control_peer"}
			fixture.DatabaseReferenceCleanup(t, &databases)
			p := subscriptionScenario(t, "session_peer_sub", "session_private_sub")
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					name := "session_controls"
					originStateCleanup(t, b.DB, name)
					originStateCreate(t, p, b, b.DB, name)
					a := originSessionConn(t, p, b.DB)
					observer := originSessionConn(t, p, b.DB)
					originStateRun(t, p, b, a, fixture.Exec("CREATE TABLE session_control_rows(v int)", 0))
					originSessionSetup(t, p, b, a, name)
					originSessionXact(t, p, b, a, "0/10")
					originStateRun(t, p, b, a, fixture.Exec("INSERT INTO session_control_rows VALUES(10)", 1))
					originStateRun(t, p, b, observer, fixture.Exec("CREATE DATABASE session_control_peer", 0))
					peer, _, recorder := fixture.DatabaseScenarioPool(t, p, b.PG, "session_control_peer", "")
					pb := b
					pb.DB, pb.Recorder = peer, recorder
					originSessionSubscriptionCleanup(t, peer, "session_peer_sub", "session_private_sub")
					originCreate(t, p, pb, peer, "session_peer_sub", false)
					_, peerName := originIdentity(t, p, peer, "session_peer_sub")
					pa := originSessionConn(t, p, peer)
					originSessionSetup(t, p, pb, pa, peerName)
					originSessionXact(t, p, pb, pa, "0/20")
					originStateRun(t, p, pb, pa, fixture.Exec("SELECT pg_current_xact_id()", 1))
					originSessionProgress(t, p, pb, pa, "0/20")
					// A transaction-private subscription has no committed descriptor to classify.
					// Retained scope must come from its actual reservation/context, not a peer
					// journal read or a sessions scan.
					pending := originSessionConn(t, p, peer)
					originStateRun(t, p, pb, pending, fixture.Exec("BEGIN", 0))
					originCreate(t, p, pb, pending, "session_private_sub", false)
					pendingID, pendingName := originIdentity(t, p, pending, "session_private_sub")
					originSessionSetup(t, p, pb, pending, pendingName)
					originSessionXact(t, p, pb, pending, "0/30")
					old, err := a.QueryContext(p.Ctx, "SELECT pg_replication_origin_session_progress(false) AS v")
					if err != nil {
						t.Fatal(err)
					}
					defer old.Close()
					if !old.Next() {
						t.Fatal(old.Err())
					}
					var retained string
					if err := old.Scan(&retained); err != nil || retained != "0/10" {
						t.Fatal(retained, err)
					}
					metadata, err := old.ColumnTypes()
					if err != nil {
						t.Fatal(err)
					}
					old.Close()
					if !b.PG {
						var dump bytes.Buffer
						if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
							t.Fatal(err)
						}
						if strings.Contains(dump.String(), "pg_replication_origin_session_setup") || strings.Contains(dump.String(), "pg_replication_origin_xact_setup") {
							t.Fatal("dump serialized physical context")
						}
						failure := errors.New("session origin I/O failure")
						if err := stuntgres.Dump(p.Ctx, p.Native, fixture.CatalogControlFailingWriter{Err: failure}); !errors.Is(err, failure) {
							t.Fatal(err)
						}
						if err := stuntgres.Restore(p.Ctx, p.Native, fixture.CatalogControlFailingReader{Err: failure}); !errors.Is(err, failure) {
							t.Fatal(err)
						}
						if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String()+"SELECT 1;")); err == nil {
							t.Fatal("late invalid restore succeeded")
						}
						canceled, cancel := context.WithCancel(p.Ctx)
						cancel()
						if err := stuntgres.Reset(canceled, p.Native); !errors.Is(err, context.Canceled) {
							t.Fatal(err)
						}
						originSessionIsSetup(t, p, b, a, true)
						originSessionXact(t, p, b, a, "0/11")
						originStateRun(t, p, b, a, fixture.Exec("INSERT INTO session_control_rows VALUES(11)", 1))
						originSessionProgress(t, p, b, a, "0/11")
						// Publication after the reader capture must reject, retaining ownership and
						// the new progress rather than invalidating the physical context eagerly.
						err := subscriptionRestoreDuringRead(t, p, dump.Bytes(), func(context.CancelFunc) {
							originSessionXact(t, p, b, a, "0/12")
							originStateRun(t, p, b, a, fixture.Exec("INSERT INTO session_control_rows VALUES(12)", 1))
						})
						if err == nil {
							t.Fatal("stale reader replaced live completion")
						}
						originSessionIsSetup(t, p, b, a, true)
						originSessionProgress(t, p, b, a, "0/12")
						originStateRun(t, p, b, a, fixture.Exec("BEGIN", 0))
						originSessionXact(t, p, b, a, "0/99")
						originStateRun(t, p, b, a, fixture.Exec("INSERT INTO session_control_rows VALUES(99)", 1))
						if control == "reset" {
							err = stuntgres.Reset(p.Ctx, p.Native)
						} else {
							err = stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes()))
						}
						if err != nil {
							t.Fatal(err)
						}
						originStateRun(t, p, b, a, fixture.Exec("ROLLBACK", 0))
						originSessionIsSetup(t, p, b, a, false)
						originStateRun(t, p, b, a, originStateError("SELECT pg_replication_origin_session_reset()", "55000", "no replication origin is configured", ""))
						originStateRun(t, p, b, a, fixture.Exec("SELECT pg_replication_origin_xact_reset()", 1))
						if control == "reset" {
							originStateCreate(t, p, b, observer, name)
						}
						originSessionSetup(t, p, b, a, name)
						var want any = nil
						if control == "restore" {
							want = "0/10"
						}
						originSessionProgress(t, p, b, a, want)
						if retained != "0/10" || len(metadata) != 1 || metadata[0].Name() != "v" || metadata[0].DatabaseTypeName() != "PG_LSN" {
							t.Fatal("acquired scalar metadata mutated", retained, metadata)
						}
					}
					originSessionIsSetup(t, p, pb, pa, true)
					originSessionProgress(t, p, pb, pa, "0/20")
					originSessionIsSetup(t, p, pb, pending, true)
					originSessionProgress(t, p, pb, pending, nil)
					pid := originStateNumber(t, p, pending, "SELECT pg_backend_pid()")
					originStateRun(t, p, pb, pending, originStateError("SELECT pg_replication_origin_advance($1::text,'0/9')", "55006", fmt.Sprintf("replication origin with ID %d is already active for PID %d", pendingID, pid), "", pendingName))
					// Error aborts the assigned private transaction; configured context remains
					// selected and applies at that real abort boundary.
					originStateRun(t, p, pb, pending, fixture.Exec("ROLLBACK", 0))
					originSessionIsSetup(t, p, pb, pending, true)
					originSessionProgress(t, p, pb, pending, "0/30")
					originStateRun(t, p, pb, pending, fixture.Exec("SELECT pg_replication_origin_session_reset()", 1))
					// The orphan slot remains numeric, exactly like ordinary PostgreSQL rollback.
					originCreate(t, p, pb, peer, "session_private_sub", false)
					_, reused := originIdentity(t, p, peer, "session_private_sub")
					originStateProgress(t, p, pb, peer, reused, "0/30")
				})
			}
		})
	}
}

func originSessionOrphanControls(t *testing.T) {
	t.Helper()
	for _, control := range []string{"reset", "restore"} {
		for _, zero := range []bool{false, true} {
			t.Run(fmt.Sprintf("orphan_%s_zero_%t", control, zero), func(t *testing.T) {
				databases := []string{"session_orphan_peer"}
				fixture.DatabaseReferenceCleanup(t, &databases)
				p := subscriptionScenario(t, "session_orphan_sub")
				for _, b := range p.Backends() {
					t.Run(b.Name, func(t *testing.T) {
						originStateRun(t, p, b, b.DB, fixture.Exec("CREATE DATABASE session_orphan_peer", 0))
						peer, _, recorder := fixture.DatabaseScenarioPool(t, p, b.PG, "session_orphan_peer", "")
						pb := b
						pb.DB, pb.Recorder = peer, recorder
						originSessionSubscriptionCleanup(t, peer, "session_orphan_sub")
						a := originSessionConn(t, p, peer)
						originStateRun(t, p, pb, a, fixture.Exec("CREATE TABLE session_orphan_rows(v int)", 0))
						originStateRun(t, p, pb, a, fixture.Exec("BEGIN", 0))
						originCreate(t, p, pb, a, "session_orphan_sub", false)
						orphanID, orphanName := originIdentity(t, p, a, "session_orphan_sub")
						originSessionSetup(t, p, pb, a, orphanName)
						var want any
						if !zero {
							originSessionXact(t, p, pb, a, "0/40")
							want = "0/40"
						}
						originStateRun(t, p, pb, a, fixture.Exec("ROLLBACK", 0))
						originSessionIsSetup(t, p, pb, a, true)
						originSessionProgress(t, p, pb, a, want)
						// No descriptor or reservation remains. Configure a new pending
						// position in the read-only incarnation without consuming the slot.
						originSessionXact(t, p, pb, a, "0/80")
						if !b.PG {
							connector, err := stuntgres.NewDriver().OpenConnector("session-origin-donor")
							if err != nil {
								t.Fatal(err)
							}
							donor := sql.OpenDB(connector)
							defer donor.Close()
							var collision string
							names := make([]string, 10)
							for i := range names {
								names[i] = fmt.Sprintf("session_donor_%d", i)
								var id int64
								if err := donor.QueryRowContext(p.Ctx, "SELECT pg_replication_origin_create($1::text)", names[i]).Scan(&id); err != nil {
									t.Fatal(err)
								}
								if id == orphanID {
									collision = names[i]
								}
								if _, err := donor.ExecContext(p.Ctx, "SELECT pg_replication_origin_advance($1::text,'0/0')", names[i]); err != nil {
									t.Fatal(err)
								}
							}
							if collision == "" {
								t.Fatal("donor did not exercise an actual numeric collision", orphanID)
							}
							var full bytes.Buffer
							if err := stuntgres.Dump(p.Ctx, donor, &full); err != nil {
								t.Fatal(err)
							}
							err = stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(full.Bytes()))
							fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "53400", Message: `could not replace replication origin progress: replacement requires 11 active replication state slots, but "max_active_replication_origins" is 10`, Hint: `Increase "max_active_replication_origins" and try again.`})
							originSessionIsSetup(t, p, pb, a, true)
							originSessionProgress(t, p, pb, a, want)
							remove := names[len(names)-1]
							if remove == collision {
								remove = names[0]
							}
							if _, err := donor.ExecContext(p.Ctx, "SELECT pg_replication_origin_drop($1::text)", remove); err != nil {
								t.Fatal(err)
							}
							var exact bytes.Buffer
							if err := stuntgres.Dump(p.Ctx, donor, &exact); err != nil {
								t.Fatal(err)
							}
							if control == "reset" {
								if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
									t.Fatal(err)
								}
								originSessionIsSetup(t, p, pb, a, true)
								originSessionProgress(t, p, pb, a, want)
							}
							if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(exact.Bytes())); err != nil {
								t.Fatal(err)
							}
							if imported := originStateID(t, p, b.DB, collision); imported == orphanID {
								t.Fatal("import stole active orphan numeric ID", imported)
							}
							originStateProgress(t, p, b, b.DB, collision, nil)
							originSessionIsSetup(t, p, pb, a, true)
							originSessionProgress(t, p, pb, a, want)
						}
						originStateRun(t, p, pb, a, fixture.Exec("INSERT INTO session_orphan_rows VALUES(80)", 1))
						originSessionProgress(t, p, pb, a, "0/80")
						originCreate(t, p, pb, peer, "session_orphan_sub", false)
						reusedID, reusedName := originIdentity(t, p, peer, "session_orphan_sub")
						if reusedID != orphanID {
							t.Fatal("ordinary numeric orphan reuse changed", reusedID, orphanID)
						}
						pid := originStateNumber(t, p, a, "SELECT pg_backend_pid()")
						originStateRun(t, p, pb, peer, originStateError("SELECT pg_replication_origin_session_setup($1::text)", "55006", fmt.Sprintf("replication origin with ID %d is already active for PID %d", reusedID, pid), "", reusedName))
					})
				}
			})
		}
	}
}

func originSessionDropWait(t *testing.T) {
	t.Helper()
	t.Run("subscription_wait_cancel", func(t *testing.T) {
		p := subscriptionScenario(t, "session_drop_wait_sub")
		for _, b := range p.Backends() {
			t.Run(b.Name, func(t *testing.T) {
				originCreate(t, p, b, b.DB, "session_drop_wait_sub", false)
				id, name := originIdentity(t, p, b.DB, "session_drop_wait_sub")
				a := originSessionConn(t, p, b.DB)
				originSessionSetup(t, p, b, a, name)
				originSessionXact(t, p, b, a, "0/10")
				originStateRun(t, p, b, a, fixture.Exec("SELECT pg_current_xact_id()", 1))
				originSessionProgress(t, p, b, a, "0/10")
				// Internal subscription clear waits even for its own backend.
				// The timer proves an actual blocking call without sleeps, polling,
				// invented native wait telemetry or a racing release goroutine.
				originStateRun(t, p, b, a, fixture.Exec("SET statement_timeout='100ms'", 0))
				originStateRun(t, p, b, a, originStateError("DROP SUBSCRIPTION session_drop_wait_sub", "57014", "canceling statement due to statement timeout", ""))
				originStateRun(t, p, b, a, fixture.Exec("SET statement_timeout=0", 0))
				originCheck(t, p, b, b.DB, "session_drop_wait_sub", id, name)
				originSessionIsSetup(t, p, b, a, true)
				originSessionProgress(t, p, b, a, "0/10")
				originStateRun(t, p, b, a, fixture.Exec("SELECT pg_replication_origin_session_reset()", 1))
				originStateRun(t, p, b, b.DB, fixture.Exec("DROP SUBSCRIPTION session_drop_wait_sub", 0))
				originStateRun(t, p, b, b.DB, originStateScalar("SELECT pg_replication_origin_oid($1::text) AS v", "OID", nil, name))
			})
		}
	})
}
