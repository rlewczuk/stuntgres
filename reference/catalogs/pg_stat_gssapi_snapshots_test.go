package reference_test

import (
	"context"
	"database/sql"
	"strconv"
	"testing"

	"stuntgres/reference/fixture"
)

func gssapiTimestamp(t *testing.T, p *fixture.Pair, c *sql.Conn) sql.NullTime {
	t.Helper()
	var stamp sql.NullTime
	if err := c.QueryRowContext(p.Ctx, "SELECT pg_catalog.pg_stat_get_snapshot_timestamp()").Scan(&stamp); err != nil {
		t.Fatal(err)
	}
	return stamp
}

func TestReferencePGStatGSSAPISnapshots(t *testing.T) {
	for _, mode := range []string{"none", "cache", "snapshot"} {
		t.Run(mode, func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"gss_snapshot_peer"})
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					gssapiCreateRoles(t, p, b, "gss_snapshot_peer")
					a, admin := gssapiConn(t, p, b.DB), gssapiConn(t, p, b.DB)
					retainedOwner := gssapiConn(t, p, b.DB)
					peerPool, peer := gssapiLogin(t, p, b, "gss_snapshot_peer", 0)
					aPID, peerPID := gssapiPID(t, p, a), gssapiPID(t, p, peer)
					gssapiExec(t, p, a, "SET stats_fetch_consistency="+mode)
					statement, err := a.PrepareContext(p.Ctx, gssapiPIDQuery)
					if err != nil {
						t.Fatal(err)
					}
					defer statement.Close()
					held := fixture.HeldStatement{Executor: a, Statement: statement, SQL: gssapiPIDQuery}
					gssapiExec(t, p, a, "BEGIN")
					defer a.ExecContext(context.Background(), "ROLLBACK")
					if stamp := gssapiTimestamp(t, p, a); stamp.Valid {
						t.Fatal("eager cumulative snapshot", stamp)
					}
					// Even an empty filtered observation captures raw activity membership.
					gssapiBoth(t, p, b, a, gssapiPIDQuery, gssapiTuples(), int64(-1))
					gssapiBoth(t, p, b, a, gssapiPIDQuery, gssapiTuples(peerPID), peerPID)
					if stamp := gssapiTimestamp(t, p, a); stamp.Valid {
						t.Fatal("activity initialized cumulative timestamp", stamp)
					}
					newPool, newcomer := gssapiLogin(t, p, b, "gss_snapshot_peer", 0)
					newPID := gssapiPID(t, p, newcomer)
					if newPID == peerPID || newPID == aPID {
						t.Fatal("duplicate live PID", newPID)
					}
					retained, err := retainedOwner.QueryContext(p.Ctx, gssapiPIDQuery, peerPID)
					if err != nil {
						t.Fatal(err)
					}
					defer retained.Close()
					gssapiMetadata(t, retained, b.PG)
					gssapiRetire(t, p, b, admin, peerPool, peer, peerPID)
					gssapiConsume(t, retained, b.PG, gssapiTuples(peerPID))
					gssapiBoth(t, p, b, a, gssapiPIDQuery, gssapiTuples(peerPID), peerPID)
					gssapiRead(t, p, b, held, false, gssapiPIDQuery, gssapiTuples(peerPID), peerPID)
					gssapiBoth(t, p, b, a, gssapiPIDQuery, gssapiTuples(), newPID)
					gssapiClear(t, p, a)
					gssapiBoth(t, p, b, a, gssapiPIDQuery, gssapiTuples(), peerPID)
					gssapiBoth(t, p, b, a, gssapiPIDQuery, gssapiTuples(newPID), newPID)
					gssapiRetire(t, p, b, admin, newPool, newcomer, newPID)
					gssapiBoth(t, p, b, a, gssapiPIDQuery, gssapiTuples(newPID), newPID)
					gssapiExec(t, p, a, "COMMIT")
					gssapiExec(t, p, a, "BEGIN")
					query := gssapiQuery + " WHERE pid IN ($1,$2,$3)"
					gssapiBoth(t, p, b, a, query, gssapiTuples(aPID), aPID, peerPID, newPID)
					gssapiExec(t, p, a, "ROLLBACK")

					// A preexisting cumulative snapshot is neither replaced nor advanced
					// by this activity-only view, including prepared execution.
					gssapiExec(t, p, admin, "CREATE TABLE gssapi_cumulative(n integer)")
					gssapiExec(t, p, a, "SET stats_fetch_consistency=snapshot")
					gssapiExec(t, p, a, "BEGIN")
					step := fixture.Select("SELECT n_tup_ins FROM pg_catalog.pg_stat_user_tables WHERE relid='public.gssapi_cumulative'::regclass", []string{"n_tup_ins"}, []string{"INT8"}, [][]any{{int64(0)}})
					fixture.RunStep(t, p.Ctx, a, b.Recorder, step)
					before := gssapiTimestamp(t, p, a)
					if !before.Valid {
						t.Fatal("missing cumulative capture")
					}
					gssapiBoth(t, p, b, a, gssapiPIDQuery, gssapiTuples(aPID), aPID)
					if after := gssapiTimestamp(t, p, a); !after.Valid || !after.Time.Equal(before.Time) {
						t.Fatal("GSSAPI changed cumulative timestamp", before, after)
					}
					gssapiExec(t, p, a, "COMMIT")
					gssapiSharedActivity(t, p, b, mode)
				})
			}
		})
	}
}

// Each first-consumer route gets a fresh transaction cut. Expected XID/xmin
// values come from the peers' public transaction APIs, never the monitored view.
func gssapiSharedActivity(t *testing.T, p *fixture.Pair, b fixture.Backend, mode string) {
	t.Helper()
	observer := gssapiConn(t, p, b.DB)
	writer := gssapiConn(t, p, b.DB)
	reader := gssapiConn(t, p, b.DB)
	writerPID, readerPID := gssapiPID(t, p, writer), gssapiPID(t, p, reader)
	if writerPID == readerPID || writerPID == gssapiPID(t, p, observer) || readerPID == gssapiPID(t, p, observer) {
		t.Fatal("shared activity requires distinct physical peers")
	}
	gssapiExec(t, p, observer, "CREATE TABLE gssapi_shared_activity(n integer)")
	gssapiExec(t, p, observer, "SET stats_fetch_consistency="+mode)
	lowXID := func(c *sql.Conn, query string) string {
		t.Helper()
		var full string
		if err := c.QueryRowContext(p.Ctx, query).Scan(&full); err != nil {
			t.Fatal(err)
		}
		value, err := strconv.ParseUint(full, 10, 64)
		if err != nil || uint32(value) == 0 {
			t.Fatal("missing actual peer transaction authority", full, err)
		}
		return strconv.FormatUint(uint64(uint32(value)), 10)
	}
	membership := gssapiQuery + " WHERE pid IN ($1,$2)"
	activity := "SELECT pid=$1 AS writer,backend_xid::text=$3 AS xid_matches,backend_xid IS NULL AS xid_absent,backend_xmin::text=$4 AS xmin_matches,backend_xmin IS NULL AS xmin_absent FROM pg_catalog.pg_stat_activity WHERE pid IN ($1,$2)"
	for _, first := range []string{"gssapi_first", "activity_first"} {
		for _, route := range []struct {
			name     string
			prepared bool
		}{{"direct", false}, {"prepared", true}} {
			for _, refresh := range []string{"clear", "end"} {
				t.Run(first+"/"+route.name+"/"+refresh, func(t *testing.T) {
					gssapiExec(t, p, writer, "BEGIN")
					defer writer.ExecContext(context.Background(), "ROLLBACK")
					fixture.RunStep(t, p.Ctx, writer, b.Recorder, fixture.Exec("INSERT INTO gssapi_shared_activity VALUES(7)", 1))
					xid := lowXID(writer, "SELECT pg_current_xact_id()::text")
					gssapiExec(t, p, reader, "BEGIN ISOLATION LEVEL REPEATABLE READ")
					defer reader.ExecContext(context.Background(), "ROLLBACK")
					fixture.RunStep(t, p.Ctx, reader, b.Recorder, fixture.Select("SELECT n FROM gssapi_shared_activity", []string{"n"}, []string{"INT4"}, [][]any{}))
					xmin := lowXID(reader, "SELECT pg_snapshot_xmin(pg_current_snapshot())::text")
					gssapiExec(t, p, observer, "BEGIN")
					defer observer.ExecContext(context.Background(), "ROLLBACK")
					checkActivity := func(prepared, completed bool) {
						t.Helper()
						want := [][]any{{true, true, false, nil, true}, {false, nil, true, true, false}}
						if completed {
							want = [][]any{{true, nil, true, nil, true}, {false, nil, true, nil, true}}
						}
						step := fixture.Select(activity, []string{"writer", "xid_matches", "xid_absent", "xmin_matches", "xmin_absent"}, []string{"BOOL", "BOOL", "BOOL", "BOOL", "BOOL"}, want, writerPID, readerPID, xid, xmin)
						step.Bag = true
						step.Prepared = prepared
						fixture.RunStep(t, p.Ctx, observer, b.Recorder, step)
					}
					if first == "gssapi_first" {
						gssapiRead(t, p, b, observer, route.prepared, membership, gssapiTuples(writerPID, readerPID), writerPID, readerPID)
						checkActivity(route.prepared, false)
					} else {
						checkActivity(route.prepared, false)
						gssapiRead(t, p, b, observer, route.prepared, membership, gssapiTuples(writerPID, readerPID), writerPID, readerPID)
					}
					if stamp := gssapiTimestamp(t, p, observer); stamp.Valid {
						t.Fatal("shared activity initialized cumulative timestamp", stamp)
					}
					// The shared raw cut must not retain execution privileges, in
					// either consumer order or either actual preparation route.
					gssapiExec(t, p, observer, "SET ROLE pg_signal_backend")
					gssapiRead(t, p, b, observer, route.prepared, membership, gssapiTuples(), writerPID, readerPID)
					gssapiExec(t, p, observer, "RESET ROLE")
					gssapiRead(t, p, b, observer, route.prepared, membership, gssapiTuples(writerPID, readerPID), writerPID, readerPID)
					gssapiExec(t, p, writer, "ROLLBACK")
					gssapiExec(t, p, reader, "COMMIT")
					for _, prepared := range []bool{false, true} {
						checkActivity(prepared, false)
					}
					if refresh == "clear" {
						gssapiClear(t, p, observer)
					} else {
						gssapiExec(t, p, observer, "COMMIT")
						gssapiExec(t, p, observer, "BEGIN")
					}
					// Reinitialize through GSSAPI so refreshed activity cannot
					// accidentally depend on which view captures completion.
					gssapiRead(t, p, b, observer, route.prepared, membership, gssapiTuples(writerPID, readerPID), writerPID, readerPID)
					for _, prepared := range []bool{false, true} {
						checkActivity(prepared, true)
					}
					if stamp := gssapiTimestamp(t, p, observer); stamp.Valid {
						t.Fatal("refreshed activity initialized cumulative timestamp", stamp)
					}
					gssapiExec(t, p, observer, "COMMIT")
				})
			}
		}
	}
}
