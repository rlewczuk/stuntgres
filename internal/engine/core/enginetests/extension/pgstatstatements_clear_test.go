package extension_test

import (
	"database/sql/driver"
	"fmt"
	"reflect"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/statistics"
	"stuntgres/internal/sqltype"
)

func pgssClearRead(t *testing.T, s *session.Session, query string) [][]driver.Value {
	t.Helper()
	result := enginetest.SessionSQL(t, s, query)
	rows := pgssClearTuples(t, result)
	if err := result.Rows.Close(); err != nil {
		t.Fatal(err)
	}
	return rows
}

func pgssClearTuples(t *testing.T, result core.Result) [][]driver.Value {
	t.Helper()
	cursor := enginetest.IndependentFinalRows(result.Rows)
	defer cursor.Close()
	values := make([]sqltype.Value, len(cursor.Schema()))
	var rows [][]driver.Value
	for {
		present, err := cursor.Next(t.Context(), values)
		if err != nil {
			t.Fatal(err)
		}
		if !present {
			return rows
		}
		row := make([]driver.Value, len(values))
		for i, value := range values {
			if !value.IsNull() && value.Type().ID() == sqltype.Float8 {
				row[i], err = value.Float()
				if err != nil {
					t.Fatal(err)
				}
			} else {
				row[i] = enginetest.Export(value)
			}
		}
		rows = append(rows, row)
	}
}

// This is a composed owner proof: both database engines retain the exact same
// collector, real sessions publish the entries, and genuine installed consumers
// in both databases observe Clear. It does not add the separately owned SQL reset.
func TestPgStatStatementsClearComposedSessions(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		name := "direct"
		if prepared {
			name = "prepared"
		}
		t.Run(name, func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			pgssOwnerExec(t, e, s, "SET pg_stat_statements.track='none'")
			pgssOwnerExec(t, e, s, "CREATE DATABASE pgss_clear_peer")
			peer := e.Cluster.Targets["pgss_clear_peer"]
			if peer == nil || peer.Cluster != e.Cluster || peer.Cluster.StatementStatistics != e.Cluster.StatementStatistics || peer.Catalog.DatabaseAddress() == e.Catalog.DatabaseAddress() {
				t.Fatal("database attachment did not retain cluster statement owner")
			}
			ps := enginetest.StartSession(t, peer, nil)
			owners := []struct {
				engine  *core.Engine
				session *session.Session
			}{{e, s}, {peer, ps}}
			for _, owner := range owners {
				for _, query := range []string{
					"SET pg_stat_statements.track='none'",
					"CREATE EXTENSION pg_stat_statements",
					"CREATE TABLE pgss_clear_data(k integer)",
					"INSERT INTO pgss_clear_data VALUES(1),(2)",
					"SET pg_stat_statements.track_utility=off",
					"SET pg_stat_statements.track_planning=on",
					"SET pg_stat_statements.track='top'",
				} {
					pgssOwnerExec(t, owner.engine, owner.session, query)
				}
			}
			// The first SET track=none is itself a valid enabled utility. End
			// completed setup through the genuine shared reset seam before either
			// session starts target execution, rather than hiding that real event.
			phaseStart := time.Now()
			if n := e.Cluster.StatementStatistics.Clear(statistics.StatementFilter{}, false, phaseStart); n != 2 {
				t.Fatal("setup did not publish its two real utility events", n)
			}
			if info := e.Cluster.StatementStatistics.Info(); info != (statistics.StatementInfo{StatsReset: phaseStart}) {
				t.Fatal("setup clear did not establish fresh phase metadata", info)
			}
			for _, owner := range owners {
				if prepared {
					source := "SELECT k AS pgss_clear_target FROM pgss_clear_data WHERE k >= $1::integer"
					ast, description := enginetest.PreparedDescription(t, owner.engine, owner.session, source)
					for input := int64(1); input <= 2; input++ {
						if err := pgssPreparedPhase(t, owner.engine, owner.session, ast, &description, source, input, nil); err != nil {
							t.Fatal(err)
						}
					}
				} else {
					pgssOwnerExec(t, owner.engine, owner.session, "SELECT k AS pgss_clear_target FROM pgss_clear_data WHERE k >= 1")
					pgssOwnerExec(t, owner.engine, owner.session, "SELECT k AS pgss_clear_target FROM pgss_clear_data WHERE k >= 2")
				}
				pgssOwnerExec(t, owner.engine, owner.session, "SET pg_stat_statements.track='none'")
			}
			before := e.Cluster.StatementStatistics.Snapshot()
			if len(before) != 2 {
				t.Fatal("real execution did not produce one entry per database", before)
			}
			var target, untouched statistics.StatementEntry
			for _, entry := range before {
				if entry.Execution.Count != 2 || entry.Rows != 3 || entry.Planning.Count != 2 || !entry.Key.TopLevel || entry.Key.QueryID == 0 {
					t.Fatal("real phase publication missing", entry)
				}
				if entry.Key.DatabaseID == uint32(peer.Catalog.DatabaseAddress().ObjectID) {
					target = entry
				} else if entry.Key.DatabaseID == uint32(e.Catalog.DatabaseAddress().ObjectID) {
					untouched = entry
				} else {
					t.Fatal("entry has unrelated database identity", entry)
				}
			}
			if target.Key.QueryID == 0 || untouched.Key.QueryID == 0 || target.Key.UserID != untouched.Key.UserID {
				t.Fatal("composed principal/database provenance", target, untouched)
			}
			query := "SELECT dbid::bigint,calls,rows,min_exec_time,max_exec_time FROM pg_stat_statements ORDER BY dbid"
			baseline := pgssClearRead(t, s, query)
			if len(baseline) != 2 || !reflect.DeepEqual(pgssClearRead(t, ps, query), baseline) {
				t.Fatal("installed consumers do not observe the same cluster history", baseline)
			}
			for _, row := range baseline {
				if row[1] != int64(2) || row[2] != int64(3) {
					t.Fatal("session observation lost actual calls/rows", row)
				}
			}
			held := enginetest.SessionSQL(t, ps, query)
			info := peer.Cluster.StatementStatistics.Info()
			later := time.Now().Add(time.Second)
			filter := statistics.StatementFilter{UserID: target.Key.UserID, DatabaseID: target.Key.DatabaseID, QueryID: target.Key.QueryID}
			if n := peer.Cluster.StatementStatistics.Clear(filter, true, later); n != 1 {
				t.Fatal("shared minmax seam did not reach real peer execution", n)
			}
			for _, entry := range e.Cluster.StatementStatistics.Snapshot() {
				if entry.Key == untouched.Key {
					if !reflect.DeepEqual(entry, untouched) {
						t.Fatal("peer minmax changed unrelated database", entry, untouched)
					}
					continue
				}
				if entry.Key != target.Key || entry.Rows != target.Rows || entry.StatsSince != target.StatsSince || entry.MinMaxStatsSince != later {
					t.Fatal("minmax changed real entry identity/history", entry, target)
				}
				for i, moments := range []statistics.StatementMoments{entry.Planning, entry.Execution} {
					old := []statistics.StatementMoments{target.Planning, target.Execution}[i]
					if moments.Count != old.Count || moments.Total != old.Total || moments.Mean != old.Mean || moments.M2 != old.M2 || moments.Stddev() != old.Stddev() || moments.Min != 0 || moments.Max != 0 {
						t.Fatal("real minmax clear changed cumulative samples", moments, old)
					}
				}
			}
			if peer.Cluster.StatementStatistics.Info() != info {
				t.Fatal("nonempty minmax reset global info")
			}
			fromMain, fromPeer := pgssClearRead(t, s, query), pgssClearRead(t, ps, query)
			if !reflect.DeepEqual(fromMain, fromPeer) || len(fromMain) != 2 {
				t.Fatal("sessions disagree after shared minmax", fromMain, fromPeer)
			}
			for _, row := range fromPeer {
				if row[0] == int64(target.Key.DatabaseID) && (row[1] != int64(2) || row[2] != int64(3) || row[3] != float64(0) || row[4] != float64(0)) {
					t.Fatal("peer session did not observe minmax-only reset", row)
				}
			}
			if n := e.Cluster.StatementStatistics.Clear(filter, false, later.Add(time.Second)); n != 1 {
				t.Fatal("filtered removal did not reach real peer entry", n)
			}
			remainingQuery := fmt.Sprintf("SELECT calls,rows FROM pg_stat_statements WHERE dbid=%d::oid", untouched.Key.DatabaseID)
			for _, owner := range owners {
				if got := pgssClearRead(t, owner.session, remainingQuery); !reflect.DeepEqual(got, [][]driver.Value{{int64(2), int64(3)}}) {
					t.Fatal("filtered clear lost unaffected session history", got)
				}
				if got := pgssClearRead(t, owner.session, fmt.Sprintf("SELECT calls FROM pg_stat_statements WHERE dbid=%d::oid", target.Key.DatabaseID)); len(got) != 0 {
					t.Fatal("cleared entry remains visible across sessions", got)
				}
			}
			if e.Cluster.StatementStatistics.Info() != info {
				t.Fatal("peer-preserving removal changed global info")
			}
			if got := pgssClearTuples(t, held); !reflect.DeepEqual(got, baseline) {
				t.Fatal("shared clears changed already returned session rows", got, baseline)
			}
			if err := held.Rows.Close(); err != nil {
				t.Fatal(err)
			}
			last := later.Add(2 * time.Second)
			if n := peer.Cluster.StatementStatistics.Clear(statistics.StatementFilter{DatabaseID: untouched.Key.DatabaseID}, false, last); n != 1 || len(e.Cluster.StatementStatistics.Snapshot()) != 0 || e.Cluster.StatementStatistics.Info() != (statistics.StatementInfo{StatsReset: last}) {
				t.Fatal("last-database filtered clear did not reset actual shared info", n, e.Cluster.StatementStatistics.Info())
			}
		})
	}
}
