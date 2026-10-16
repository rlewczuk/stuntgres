package reference_test

import (
	"fmt"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDriverBatchPreparedBoundaries(t *testing.T) {
	for _, finish := range []string{"COMMIT", "ROLLBACK"} {
		for _, end := range []string{"", "COMMIT", "ROLLBACK"} {
			for _, exists := range []bool{false, true} {
				name := fmt.Sprintf("%s_after_%s_existing_%t", finish, end, exists)
				t.Run(name, func(t *testing.T) {
					batchReviewControlsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, f fixture.ControlsReferenceFixture) {
						batchSeed(t, p, b, f.A)
						gid := "batch_boundary_" + name
						literal := fixture.PreparedReferenceLiteral(gid)
						// Cleanup uses the exact notice connector, hence the same native engine.
						cleanupBackend := b
						cleanupBackend.DB = f.DB
						fixture.PreparedReferenceCleanup(t, cleanupBackend, gid)
						if exists {
							batchExec(t, p, b, f.A, "BEGIN; INSERT INTO batch_t VALUES(8); PREPARE TRANSACTION "+literal, 0)
						}
						f.NoticesWant(t, "", "")
						var before any
						if exists {
							before = fixture.PreparedReferenceOne(t, p, f.B, gid)
						} else {
							fixture.PreparedReferenceAbsent(t, p, f.B, gid)
						}
						q := finish + " PREPARED " + literal + "; INSERT INTO batch_t VALUES(99)"
						want := []int64{7}
						if end != "" {
							q = "INSERT INTO batch_t VALUES(9); " + end + "; " + q
							if end == "COMMIT" {
								want = []int64{7, 9}
							}
						}
						batchError(t, p, f.A, q, batchDiag("25001", finish+" PREPARED cannot run inside a transaction block"))
						if end == "" {
							f.NoticesWant(t, "", "")
						} else {
							f.NoticesWant(t, "25P01", "there is no transaction in progress")
						}
						batchValues(t, p, b, f.B, "SELECT i FROM batch_t ORDER BY i", want...)
						if exists {
							after := fixture.PreparedReferenceOne(t, p, f.B, gid)
							if !reflect.DeepEqual(before, after) {
								t.Fatalf("rejected completion changed prepared entry: before=%#v after=%#v", before, after)
							}
							// The established SELECT-prefix restriction and singleton completion remain intact.
							batchError(t, p, f.A, "SELECT 1; "+finish+" PREPARED "+literal, batchDiag("25001", finish+" PREPARED cannot run inside a transaction block"))
							fixture.PreparedReferenceOne(t, p, f.B, gid)
							batchExec(t, p, b, f.A, finish+" PREPARED "+literal, 0)
							if finish == "COMMIT" {
								want = append([]int64{7, 8}, want[1:]...)
							}
						} else {
							batchError(t, p, f.A, finish+" PREPARED "+literal, batchDiag("42704", "prepared transaction with identifier \""+gid+"\" does not exist"))
						}
						fixture.PreparedReferenceAbsent(t, p, f.B, gid)
						batchValues(t, p, b, f.B, "SELECT i FROM batch_t ORDER BY i", want...)
						f.NoticesWant(t, "", "")
					})
				})
			}
		}
	}
}

func TestReferenceDriverBatchInitialPrepare(t *testing.T) {
	for _, finish := range []string{"COMMIT", "ROLLBACK"} {
		for _, fail := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s_suffix_failure_%t", finish, fail), func(t *testing.T) {
				batchReviewControlsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, f fixture.ControlsReferenceFixture) {
					batchSeed(t, p, b, f.A)
					gid := fmt.Sprintf("batch_initial_%s_%t", finish, fail)
					literal := fixture.PreparedReferenceLiteral(gid)
					cleanupBackend := b
					cleanupBackend.DB = f.DB
					fixture.PreparedReferenceCleanup(t, cleanupBackend, gid)
					f.NoticesWant(t, "", "")
					q := "PREPARE TRANSACTION " + literal + "; INSERT INTO batch_t VALUES(9)"
					want := []int64{7, 9}
					if fail {
						q += "; SELECT missing FROM batch_t"
						w := batchDiag("42703", `column "missing" does not exist`)
						w.Position = batchPosition(q, "missing")
						batchError(t, p, f.A, q, w)
						want = []int64{7}
					} else {
						batchExec(t, p, b, f.A, q, 1)
					}
					f.NoticesWant(t, "25P01", "there is no transaction in progress")
					fixture.PreparedReferenceOne(t, p, f.B, gid)
					batchValues(t, p, b, f.B, "SELECT i FROM batch_t ORDER BY i", want...)
					batchExec(t, p, b, f.A, finish+" PREPARED "+literal, 0)
					fixture.PreparedReferenceAbsent(t, p, f.B, gid)
					batchValues(t, p, b, f.B, "SELECT i FROM batch_t ORDER BY i", want...)
					f.NoticesWant(t, "", "")
					// A genuine singleton idle PREPARE is not this retained implicit segment.
					batchExec(t, p, b, f.A, "PREPARE TRANSACTION "+literal, 0)
					f.NoticesWant(t, "25P01", "there is no transaction in progress")
					fixture.PreparedReferenceAbsent(t, p, f.B, gid)
				})
			})
		}
	}
}
