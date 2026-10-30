package reference_test

import (
	"bytes"
	"context"
	"fmt"
	"reflect"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func originReplacementFull(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, name string) {
	t.Helper()
	id := originStateID(t, p, e, name)
	s := originStateError("SELECT pg_replication_origin_advance($1::text,'0/1')", "53400", fmt.Sprintf("could not find free replication state slot for replication origin with ID %d", id), "", name)
	s.Diagnostic.Hint = `Increase "max_active_replication_origins" and try again.`
	originStateRun(t, p, b, e, s)
}

func TestReferenceReplicationOriginStatePendingPeerControls(t *testing.T) {
	for _, control := range []string{"reset", "restore"} {
		for _, kind := range []string{"active", "zero", "savepoint"} {
			t.Run(control+"_"+kind, func(t *testing.T) {
				databases := []string{"state_pending_peer"}
				fixture.DatabaseReferenceCleanup(t, &databases)
				p := subscriptionScenario(t, "state_pending_sub", "state_pending_published")
				for _, b := range p.Backends() {
					t.Run(b.Name, func(t *testing.T) {
						originStateCleanup(t, b.DB, "pending_selected")
						originStateCreate(t, p, b, b.DB, "pending_selected")
						originStateRun(t, p, b, b.DB, fixture.Exec("CREATE DATABASE state_pending_peer", 0))
						peer, _, recorder := fixture.DatabaseScenarioPool(t, p, b.PG, "state_pending_peer", "")
						pb := b
						pb.DB, pb.Recorder = peer, recorder
						originCreate(t, p, pb, peer, "state_pending_published", false)
						publishedID, publishedName := originIdentity(t, p, peer, "state_pending_published")
						originStateRun(t, p, pb, peer, fixture.Exec("SELECT pg_replication_origin_advance($1::text,'0/9')", 1, publishedName))
						var dump bytes.Buffer
						if !b.PG {
							if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
								t.Fatal(err)
							}
						}
						tx, err := peer.BeginTx(p.Ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						defer tx.Rollback()
						originCreate(t, p, pb, tx, "state_pending_sub", false)
						id, name := originIdentity(t, p, tx, "state_pending_sub")
						var want any = "80000000/8"
						position := "80000000/8"
						if kind == "zero" {
							position, want = "0/0", nil
						}
						if kind == "savepoint" {
							originStateRun(t, p, pb, tx, fixture.Exec("SAVEPOINT retained", 0))
							originStateRun(t, p, pb, tx, fixture.Exec("SELECT pg_replication_origin_advance($1::text,'0/5')", 1, name))
							position, want = "0/3", "0/3"
						}
						originStateRun(t, p, pb, tx, fixture.Exec("SELECT pg_replication_origin_advance($1::text,$2::pg_lsn)", 1, name, position))
						originStateProgress(t, p, pb, tx, name, want)
						originStateRun(t, p, pb, peer, originStateScalar("SELECT pg_replication_origin_oid($1::text) AS v", "OID", nil, name))
						if !b.PG {
							if control == "reset" {
								err = stuntgres.Reset(p.Ctx, p.Native)
							} else {
								err = subscriptionRestoreDuringRead(t, p, dump.Bytes(), func(context.CancelFunc) {
									// A same-value write still advances its scoped generation:
									// private peer writes must not trip the standalone fence.
									originStateRun(t, p, pb, tx, fixture.Exec("SELECT pg_replication_origin_advance($1::text,$2::pg_lsn)", 1, name, position))
								})
							}
							if err != nil {
								t.Fatal(err)
							}
							if control == "restore" {
								fresh := originStateID(t, p, b.DB, "pending_selected")
								if fresh == id || fresh == publishedID {
									t.Fatal("import collided with retained claims")
								}
							}
						}
						originCheck(t, p, pb, tx, "state_pending_sub", id, name)
						originStateProgress(t, p, pb, tx, name, want)
						originCheck(t, p, pb, peer, "state_pending_published", publishedID, publishedName)
						originStateProgress(t, p, pb, peer, publishedName, "0/9")
						if kind == "savepoint" {
							originStateRun(t, p, pb, tx, fixture.Exec("ROLLBACK TO retained", 0))
							originCheck(t, p, pb, tx, "state_pending_sub", id, name)
							originStateProgress(t, p, pb, tx, name, "0/3")
							originStateRun(t, p, pb, tx, fixture.Exec("RELEASE retained", 0))
						}
						if kind == "zero" {
							names := make([]string, 9)
							for i := range names {
								names[i] = fmt.Sprintf("pending_fill_%d", i)
							}
							originStateCleanup(t, b.DB, names...)
							for i, n := range names {
								originStateCreate(t, p, b, b.DB, n)
								if i < 8 {
									originStateRun(t, p, b, b.DB, fixture.Exec("SELECT pg_replication_origin_advance($1::text,'0/1')", 1, n))
								}
							}
							originReplacementFull(t, p, b, b.DB, names[8])
							originStateRun(t, p, pb, tx, fixture.Exec("SELECT pg_replication_origin_advance($1::text,'0/2')", 1, name))
							want = "0/2"
							originStateRun(t, p, b, b.DB, fixture.Exec("SELECT pg_replication_origin_drop($1::text)", 1, names[0]))
							originStateRun(t, p, b, b.DB, fixture.Exec("SELECT pg_replication_origin_advance($1::text,'0/1')", 1, names[8]))
						}
						if err := tx.Commit(); err != nil {
							t.Fatal(err)
						}
						originCheck(t, p, pb, peer, "state_pending_sub", id, name)
						originStateProgress(t, p, pb, peer, name, want)
						for _, n := range []string{"state_pending_sub", "state_pending_published"} {
							originStateRun(t, p, pb, peer, fixture.Exec("DROP SUBSCRIPTION "+n, 0))
						}
					})
				}
			})
		}
	}
}

func TestReferenceReplicationOriginStateReplacementCapacity(t *testing.T) {
	for _, retained := range []int{9, 8} {
		t.Run(fmt.Sprint(retained), func(t *testing.T) {
			databases := []string{"state_capacity_peer"}
			fixture.DatabaseReferenceCleanup(t, &databases)
			cleanupNames := []string{"capacity_pending"}
			for i := range retained - 1 {
				cleanupNames = append(cleanupNames, fmt.Sprintf("capacity_peer_%d", i))
			}
			p := subscriptionScenario(t, cleanupNames...)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					originStateCleanup(t, b.DB, "capacity_selected", "capacity_extra", "capacity_import_high", "capacity_import_zero")
					selectedID := originStateCreate(t, p, b, b.DB, "capacity_selected")
					originStateRun(t, p, b, b.DB, fixture.Exec("SELECT pg_replication_origin_advance('capacity_selected','0/6')", 1))
					originStateRun(t, p, b, b.DB, fixture.Exec("CREATE TABLE capacity_sentinel(n integer)", 0))
					originStateRun(t, p, b, b.DB, fixture.Exec("INSERT INTO capacity_sentinel VALUES(41)", 1))
					settings, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer settings.Close()
					originStateRun(t, p, b, settings, fixture.Exec("SET application_name='capacity_retained'", 0))
					q := "SELECT n AS v FROM capacity_sentinel"
					stmt, err := settings.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					held := fixture.HeldStatement{Executor: settings, Statement: stmt, SQL: q}
					originStateRun(t, p, b, b.DB, fixture.Exec("CREATE DATABASE state_capacity_peer", 0))
					peer, _, recorder := fixture.DatabaseScenarioPool(t, p, b.PG, "state_capacity_peer", "")
					pb := b
					pb.DB, pb.Recorder = peer, recorder
					names, canonical, positions := []string{}, []string{}, []any{}
					ids := []int64{}
					for i := range retained - 1 {
						n := fmt.Sprintf("capacity_peer_%d", i)
						names = append(names, n)
						originCreate(t, p, pb, peer, n, false)
						id, name := originIdentity(t, p, peer, n)
						ids, canonical = append(ids, id), append(canonical, name)
						var want any = "0/8"
						position := "0/8"
						if i == 0 {
							position, want = "0/0", nil
						}
						positions = append(positions, want)
						originStateRun(t, p, pb, peer, fixture.Exec("SELECT pg_replication_origin_advance($1::text,$2::pg_lsn)", 1, name, position))
					}
					tx, err := peer.BeginTx(p.Ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback()
					originCreate(t, p, pb, tx, "capacity_pending", false)
					pendingID, pendingName := originIdentity(t, p, tx, "capacity_pending")
					originStateRun(t, p, pb, tx, fixture.Exec("SELECT pg_replication_origin_advance($1::text,'0/0')", 1, pendingName))
					originStateProgress(t, p, pb, tx, pendingName, nil)
					before := originBag(t, p, b.DB)
					subscriptionsBefore := originReplacementSubscriptionBag(t, p, b.DB)
					peerBefore := originBag(t, p, peer)
					if !b.PG {
						rows, err := b.DB.QueryContext(p.Ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						defer rows.Close()
						columns, err := rows.ColumnTypes()
						if err != nil || len(columns) != 1 || columns[0].DatabaseTypeName() != "INT4" {
							t.Fatal(columns, err)
						}
						donor := fixture.CatalogControlPool(t, stuntgres.NewDriver(), "capacity_donor")
						for _, sql := range []string{"CREATE TABLE capacity_sentinel(n integer)", "INSERT INTO capacity_sentinel VALUES(7)", "SELECT pg_replication_origin_create('capacity_import_high')", "SELECT pg_replication_origin_create('capacity_import_zero')", "SELECT pg_replication_origin_advance('capacity_import_high','80000000/4')", "SELECT pg_replication_origin_advance('capacity_import_zero','0/0')"} {
							if _, err := donor.ExecContext(p.Ctx, sql); err != nil {
								t.Fatal(err)
							}
						}
						var dump bytes.Buffer
						if err := stuntgres.Dump(p.Ctx, donor, &dump); err != nil {
							t.Fatal(err)
						}
						err = stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes()))
						if retained == 9 {
							want := fixture.ControlsReferenceDiagnostic("53400", `could not replace replication origin progress: replacement requires 11 active replication state slots, but "max_active_replication_origins" is 10`)
							want.Hint = `Increase "max_active_replication_origins" and try again.`
							fixture.AssertDiagnostic(t, err, &want)
							if !reflect.DeepEqual(before, originBag(t, p, b.DB)) || originStateID(t, p, b.DB, "capacity_selected") != selectedID {
								t.Fatal("capacity rejection changed registrations")
							}
							originStateProgress(t, p, b, b.DB, "capacity_selected", "0/6")
							for _, n := range []string{"capacity_import_high", "capacity_import_zero"} {
								originStateRun(t, p, b, b.DB, originStateScalar("SELECT pg_replication_origin_oid($1::text) AS v", "OID", nil, n))
							}
						} else {
							if err != nil {
								t.Fatal(err)
							}
							originStateRun(t, p, b, b.DB, originStateScalar("SELECT pg_replication_origin_oid('capacity_selected') AS v", "OID", nil))
							originStateProgress(t, p, b, b.DB, "capacity_import_high", "80000000/4")
							originStateProgress(t, p, b, b.DB, "capacity_import_zero", nil)
						}
						var n int64
						if !rows.Next() {
							t.Fatal(rows.Err())
						}
						if err := rows.Scan(&n); err != nil || n != 41 || rows.Next() || rows.Err() != nil {
							t.Fatal(n, err, rows.Err())
						}
						if columns[0].DatabaseTypeName() != "INT4" {
							t.Fatal("retained metadata changed")
						}
						rows.Close()
					}
					wantSentinel := int64(41)
					if !b.PG && retained == 8 {
						wantSentinel = 7
					}
					originStateRun(t, p, b, held, originStateScalar(q, "INT4", wantSentinel))
					originStateRun(t, p, b, settings, originStateScalar("SELECT current_setting('application_name') AS v", "TEXT", "capacity_retained"))
					if retained == 9 {
						if !reflect.DeepEqual(before, originBag(t, p, b.DB)) || !reflect.DeepEqual(peerBefore, originBag(t, p, peer)) || !reflect.DeepEqual(subscriptionsBefore, originReplacementSubscriptionBag(t, p, b.DB)) {
							t.Fatal("rejection changed complete shared bags")
						}
					}
					for i, n := range names {
						originCheck(t, p, pb, peer, n, ids[i], canonical[i])
						originStateProgress(t, p, pb, peer, canonical[i], positions[i])
					}
					originCheck(t, p, pb, tx, "capacity_pending", pendingID, pendingName)
					originStateProgress(t, p, pb, tx, pendingName, nil)
					if err := tx.Commit(); err != nil {
						t.Fatal(err)
					}
					originCheck(t, p, pb, peer, "capacity_pending", pendingID, pendingName)
					originStateProgress(t, p, pb, peer, pendingName, nil)
					// PostgreSQL proves ordinary slot presence/capacity without a native
					// replacement equivalent. Bring its independently known total to ten.
					if b.PG && retained == 8 {
						originStateCreate(t, p, b, b.DB, "capacity_import_zero")
						originStateRun(t, p, b, b.DB, fixture.Exec("SELECT pg_replication_origin_advance('capacity_import_zero','0/0')", 1))
					}
					originStateCreate(t, p, b, b.DB, "capacity_extra")
					originReplacementFull(t, p, b, b.DB, "capacity_extra")
					originStateRun(t, p, pb, peer, fixture.Exec("SELECT pg_replication_origin_advance($1::text,'0/2')", 1, pendingName))
					originStateRun(t, p, pb, peer, fixture.Exec("SELECT pg_replication_origin_drop($1::text)", 1, canonical[0]))
					originStateRun(t, p, b, b.DB, fixture.Exec("SELECT pg_replication_origin_advance('capacity_extra','0/1')", 1))
					for _, n := range append(names, "capacity_pending") {
						originStateRun(t, p, pb, peer, fixture.Exec("DROP SUBSCRIPTION "+n, 0))
					}
				})
			}
		})
	}
}

func originReplacementSubscriptionBag(t *testing.T, p *fixture.Pair, e fixture.Executor) map[string]int {
	t.Helper()
	rows, err := e.QueryContext(p.Ctx, "SELECT * FROM pg_subscription")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	bag := make(map[string]int)
	for rows.Next() {
		values := make([]any, len(columns))
		destinations := make([]any, len(columns))
		for i := range values {
			destinations[i] = &values[i]
		}
		if err := rows.Scan(destinations...); err != nil {
			t.Fatal(err)
		}
		for i := range values {
			values[i] = fixture.Normalize(values[i])
		}
		bag[fmt.Sprint(values)]++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return bag
}
