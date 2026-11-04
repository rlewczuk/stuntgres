package reference_test

import (
	"context"
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGStatSSLSnapshots(t *testing.T) {
	for _, mode := range []string{"none", "cache", "snapshot"} {
		for _, first := range []string{"pg_stat_activity", "pg_stat_ssl"} {
			for _, terminal := range []string{"COMMIT", "ROLLBACK"} {
				t.Run(mode+"/"+first+"/"+terminal, func(t *testing.T) {
					activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, o *sql.Conn) {
						a, other := statActivityConn(t, p, b), statActivityConn(t, p, b)
						probe := statActivityConn(t, p, b)
						apid, bpid := statActivityPID(t, p, a), statActivityPID(t, p, other)
						statsExec(t, p, b, o, "SET stats_fetch_consistency="+mode, 0)
						statsExec(t, p, b, o, "BEGIN", 0)
						defer o.ExecContext(context.Background(), "ROLLBACK")
						activityWant := func(pid int64, present bool) {
							var expected [][]any
							if present {
								expected = [][]any{{pid}}
							}
							fixture.RunStep(t, p.Ctx, o, b.Recorder, fixture.Select("SELECT pid FROM pg_stat_activity WHERE pid=$1", []string{"pid"}, []string{"INT4"}, expected, pid))
						}
						if first == "pg_stat_activity" {
							activityWant(apid, true)
						}
						sslPair(t, p, b, o, apid, bpid)
						statsNoTimestamp(t, p, o, nil)
						activityWant(apid, true)
						retained, err := o.QueryContext(p.Ctx, sslPairSQL, apid, bpid)
						if err != nil {
							t.Fatal(err)
						}
						defer retained.Close()
						frozenMetadata, err := retained.ColumnTypes()
						if err != nil {
							t.Fatal(err)
						}
						fixture.StatisticsRetire(t, a)
						statActivityRetired(t, p, probe, apid)
						newcomer := statActivityConn(t, p, b)
						newpid := statActivityPID(t, p, newcomer)
						if newpid <= 0 || newpid == apid || newpid == bpid {
							t.Fatal("successor identity", newpid, apid, bpid)
						}
						// pq streams: consume the retained result before issuing another
						// command on its observer. Retirement cannot alter those rows.
						frozen := sslFrozen(t, retained, b.PG, apid, bpid)
						for _, pid := range []int64{apid, bpid, newpid} {
							sslWant(t, p, b, o, pid, pid != newpid)
							activityWant(pid, pid != newpid)
						}
						fixture.StatisticsClearSnapshot(t, p, b, o)
						for _, pid := range []int64{apid, bpid, newpid} {
							sslWant(t, p, b, o, pid, pid != apid)
							activityWant(pid, pid != apid)
						}
						statsNoTimestamp(t, p, o, nil)
						// Distinguish transaction-end invalidation from the earlier
						// clear: change physical membership again without clearing.
						fixture.StatisticsRetire(t, newcomer)
						statActivityRetired(t, p, probe, newpid)
						successor := statActivityConn(t, p, b)
						lastpid := statActivityPID(t, p, successor)
						if lastpid <= 0 || lastpid == newpid || lastpid == apid || lastpid == bpid {
							t.Fatal("terminal successor identity", lastpid)
						}
						sslWant(t, p, b, o, newpid, true)
						sslWant(t, p, b, o, lastpid, false)
						activityWant(newpid, true)
						activityWant(lastpid, false)
						statsExec(t, p, b, o, terminal, 0)
						statsExec(t, p, b, o, "BEGIN", 0)
						sslWant(t, p, b, o, apid, false)
						sslWant(t, p, b, o, newpid, false)
						sslWant(t, p, b, o, lastpid, true)
						activityWant(apid, false)
						activityWant(newpid, false)
						activityWant(lastpid, true)
						statsNoTimestamp(t, p, o, nil)
						statsExec(t, p, b, o, "ROLLBACK", 0)
						if !reflect.DeepEqual(frozen, sslExpected(apid, bpid)) {
							t.Fatal("observer clear/end changed owned SSL result", frozen)
						}
						for i, typ := range frozenMetadata {
							if typ.Name() != sslNames[i] || typ.DatabaseTypeName() != sslTypes[i] {
								t.Fatal("observer clear/end changed owned SSL metadata", i, typ)
							}
						}
					})
				})
			}
		}
		t.Run(mode+"/canonical_ssl_first", func(t *testing.T) {
			activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, o *sql.Conn) {
				target := statActivityConn(t, p, b)
				pid := statActivityPID(t, p, target)
				statsExec(t, p, b, o, "CREATE TABLE ssl_canonical(v integer)", 0)
				statsExec(t, p, b, o, "SET stats_fetch_consistency="+mode, 0)
				activityExec(t, p, target, "BEGIN ISOLATION LEVEL REPEATABLE READ")
				defer target.ExecContext(context.Background(), "ROLLBACK")
				statsExec(t, p, b, target, "INSERT INTO ssl_canonical VALUES(7)", 1)
				var full, horizon string
				if err := target.QueryRowContext(p.Ctx, "SELECT pg_current_xact_id_if_assigned()::text,pg_snapshot_xmin(pg_current_snapshot())::text").Scan(&full, &horizon); err != nil {
					t.Fatal(err)
				}
				if full == "0" || horizon == "0" {
					t.Fatal("unassigned canonical witness", full, horizon)
				}
				activityExec(t, p, o, "BEGIN")
				defer o.ExecContext(context.Background(), "ROLLBACK")
				sslWant(t, p, b, o, pid, true)
				statsNoTimestamp(t, p, o, nil)
				row := statActivityGet(t, p, o, pid, false)
				if row.text(17) != statActivityLowXID(t, full) || row.text(18) != statActivityLowXID(t, horizon) {
					t.Fatal("SSL-first cut lost canonical visibility", full, horizon, row)
				}
				if mode == "snapshot" {
					statsCount(t, p, o, "ssl_canonical")
					before := statsTimestamp(t, p, o, nil)
					if !before.Valid {
						t.Fatal("missing genuine cumulative snapshot")
					}
					sslWant(t, p, b, o, pid, true)
					if after := statsTimestamp(t, p, o, nil); !after.Valid || !after.Time.Equal(before.Time) {
						t.Fatal("SSL changed cumulative timestamp", before, after)
					}
				}
				activityExec(t, p, o, "ROLLBACK")
				activityExec(t, p, target, "ROLLBACK")
			})
		})
	}
}
