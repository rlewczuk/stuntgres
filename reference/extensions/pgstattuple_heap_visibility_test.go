package reference_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func TestReferencePgstattupleHeapVisibility(t *testing.T) {
	for _, tc := range []struct {
		name string
		seed bool
		writes []string
		own, peer, ownApprox, peerApprox [2]int64
		committed [2]int64
	}{
		{"insert", false, []string{"INSERT INTO hs_part.heap VALUES(1)"}, [2]int64{1, 0}, [2]int64{1, 0}, [2]int64{0, 1}, [2]int64{0, 1}, [2]int64{1, 0}},
		{"delete", true, []string{"DELETE FROM hs_part.heap"}, [2]int64{0, 1}, [2]int64{1, 0}, [2]int64{1, 0}, [2]int64{1, 0}, [2]int64{0, 1}},
		{"update", true, []string{"UPDATE hs_part.heap SET k=2"}, [2]int64{1, 1}, [2]int64{2, 0}, [2]int64{1, 1}, [2]int64{1, 1}, [2]int64{1, 1}},
		{"repeated_update", true, []string{"UPDATE hs_part.heap SET k=2", "UPDATE hs_part.heap SET k=3"}, [2]int64{1, 2}, [2]int64{3, 0}, [2]int64{2, 1}, [2]int64{1, 2}, [2]int64{1, 2}},
		{"insert_update_delete", false, []string{"INSERT INTO hs_part.heap VALUES(1)", "UPDATE hs_part.heap SET k=2", "DELETE FROM hs_part.heap"}, [2]int64{0, 2}, [2]int64{2, 0}, [2]int64{2, 0}, [2]int64{0, 2}, [2]int64{0, 2}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			heapStatsCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, writer *sql.Conn) {
				horizon := heapStatsHorizon(t, p, b)
				defer horizon.Close()
				defer horizon.ExecContext(p.Ctx, "ROLLBACK")
				peer := heapStatsConn(t, p, b)
				defer peer.Close()
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, writer, b.Recorder, s) }
				if tc.seed { run(fixture.Exec("INSERT INTO hs_part.heap VALUES(1)", 1)) }
				run(fixture.Exec("BEGIN", 0))
				defer writer.ExecContext(p.Ctx, "ROLLBACK")
				for _, q := range tc.writes { run(fixture.Exec(q, 1)) }
				// Vacuum classifies own inserted-and-ended versions as
				// DELETE_IN_PROGRESS (approx live), but a peer sees the same
				// xmin as INSERT_IN_PROGRESS (approx dead), before xmax.
				heapStatsCounts(t, p, b, writer, "hs_part.heap", tc.own[0], tc.own[1], tc.ownApprox[0], tc.ownApprox[1])
				// This call completes while the writer transaction is still open;
				// it must neither await peer admission nor use SELECT visibility.
				heapStatsCounts(t, p, b, peer, "hs_part.heap", tc.peer[0], tc.peer[1], tc.peerApprox[0], tc.peerApprox[1])
				var rows [][]any
				if tc.seed { rows = [][]any{{int64(1)}} }
				fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Select("SELECT k FROM hs_part.heap", []string{"k"}, []string{"INT4"}, rows))
				run(fixture.Exec("COMMIT", 0))
				heapStatsCounts(t, p, b, peer, "hs_part.heap", tc.committed[0], tc.committed[1], tc.committed[0], tc.committed[1])
			})
		})
	}
	for _, finish := range []string{"COMMIT", "ROLLBACK"} {
		t.Run("prepared_"+finish, func(t *testing.T) {
			heapStatsCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, writer *sql.Conn) {
				horizon := heapStatsHorizon(t, p, b)
				defer horizon.Close()
				defer horizon.ExecContext(p.Ctx, "ROLLBACK")
				peer := heapStatsConn(t, p, b)
				defer peer.Close()
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, writer, b.Recorder, s) }
				run(fixture.Exec("INSERT INTO hs_part.heap VALUES(1)", 1))
				run(fixture.Exec("BEGIN", 0))
				defer writer.ExecContext(p.Ctx, "ROLLBACK")
				run(fixture.Exec("UPDATE hs_part.heap SET k=2", 1))
				run(fixture.Exec("UPDATE hs_part.heap SET k=3", 1))
				prepared := false
				defer func() {
					if prepared {
						ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
						defer cancel()
						if _, err := peer.ExecContext(ctx, "ROLLBACK PREPARED 'hs_heap_prepared'"); err != nil { t.Error(err) }
					}
				}()
				run(fixture.Exec("PREPARE TRANSACTION 'hs_heap_prepared'", 0))
				prepared = true
				heapStatsCounts(t, p, b, peer, "hs_part.heap", 3, 0, 1, 2)
				fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Select("SELECT k FROM hs_part.heap", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}}))
				fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Exec(finish+" PREPARED 'hs_heap_prepared'", 0))
				prepared = false
				if finish == "COMMIT" {
					heapStatsCounts(t, p, b, peer, "hs_part.heap", 1, 2, 1, 2)
					fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Select("SELECT k FROM hs_part.heap", []string{"k"}, []string{"INT4"}, [][]any{{int64(3)}}))
				} else {
					heapStatsLive(t, p, b, peer, 1)
					fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Select("SELECT k FROM hs_part.heap", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}}))
				}
			})
		})
	}
}

func TestReferencePgstattupleHeapRollback(t *testing.T) {
	heapStatsCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		horizon := heapStatsHorizon(t, p, b)
		defer horizon.Close()
		defer horizon.ExecContext(p.Ctx, "ROLLBACK")
		run(fixture.Exec("INSERT INTO hs_part.heap VALUES(1)", 1))
		run(fixture.Exec("BEGIN", 0))
		defer c.ExecContext(p.Ctx, "ROLLBACK")
		run(fixture.Exec("UPDATE hs_part.heap SET k=2", 1))
		run(fixture.Exec("SAVEPOINT hs_save", 0))
		run(fixture.Exec("UPDATE hs_part.heap SET k=3", 1))
		heapStatsCounts(t, p, b, c, "hs_part.heap", 1, 2, 2, 1)
		run(fixture.Exec("ROLLBACK TO SAVEPOINT hs_save", 0))
		heapStatsLive(t, p, b, c, 1)
		run(fixture.Select("SELECT k FROM hs_part.heap", []string{"k"}, []string{"INT4"}, [][]any{{int64(2)}}))
		// Append after rollback-to: discarded private roots must not be reused
		// as current heads, nor overwrite the surviving first successor.
		run(fixture.Exec("UPDATE hs_part.heap SET k=4", 1))
		run(fixture.Exec("COMMIT", 0))
		heapStatsLive(t, p, b, c, 1)
		run(fixture.Select("SELECT k FROM hs_part.heap", []string{"k"}, []string{"INT4"}, [][]any{{int64(4)}}))
		// The protected predecessor checks are complete. End the snapshot now:
		// its heap AccessShare lock would block our later AccessExclusive DDL.
		fixture.RunStep(t, p.Ctx, horizon, b.Recorder, fixture.Exec("ROLLBACK", 0))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("INSERT INTO hs_part.heap VALUES(8)", 1))
		run(fixture.Exec("UPDATE hs_part.heap SET k=9 WHERE k=8", 1))
		run(fixture.Exec("ROLLBACK", 0))
		heapStatsLive(t, p, b, c, 1)
		// A failing multirow statement must publish neither partial live rows
		// nor private retained roots when the transaction later recovers.
		run(fixture.Exec("ALTER TABLE hs_part.heap ADD CONSTRAINT hs_positive CHECK(k>0)", 0))
		q := "INSERT INTO hs_part.heap VALUES(10),(-1)"
		s := pgstatindexError(q, "23514", "new row for relation \"heap\" violates check constraint \"hs_positive\"")
		s.Diagnostic.Detail = "Failing row contains (-1)."
		s.Diagnostic.Schema = "hs_part"
		s.Diagnostic.Table = "heap"
		s.Diagnostic.Constraint = "hs_positive"
		run(s)
		heapStatsLive(t, p, b, c, 1)
		run(fixture.Select("SELECT k FROM hs_part.heap", []string{"k"}, []string{"INT4"}, [][]any{{int64(4)}}))
	})
}
