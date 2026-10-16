package reference_test

import (
	"database/sql"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDriverBatchRetainedBlockControls(t *testing.T) {
	batchScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, peer *sql.Conn) {
		f := fixture.NewControlsReferenceFixture(t, p, b.PG)
		a = f.A
		batchSeed(t, p, b, a)
		f.NoticesWant(t, "", "")
		fixture.BatchSets(t, batchQuery(t, p, a, "DECLARE bc CURSOR FOR SELECT i FROM batch_t ORDER BY i; FETCH ALL FROM bc; CLOSE bc; LOCK TABLE batch_t IN ACCESS SHARE MODE; SET LOCAL application_name='local'; SHOW application_name"), fixture.BatchSet{[]string{"i"}, []string{"INT4"}, [][]any{{int64(7)}}, false}, fixture.BatchSet{[]string{"application_name"}, []string{"TEXT"}, [][]any{{"local"}}, false})
		f.NoticesWant(t, "", "")
		fixture.RunStep(t, p.Ctx, a, b.Recorder, fixture.Select("SHOW application_name", []string{"application_name"}, []string{"TEXT"}, [][]any{{""}}))
		batchError(t, p, a, "FETCH ALL FROM bc", batchDiag("34000", `cursor "bc" does not exist`))
		batchExec(t, p, b, a, "SET TRANSACTION ISOLATION LEVEL READ COMMITTED; SET CONSTRAINTS ALL DEFERRED; INSERT INTO batch_t VALUES(8)", 1)
		f.NoticesWant(t, "", "")
		batchExec(t, p, b, a, "CREATE TABLE ex_t(id INT,i INT,CONSTRAINT batch_ex EXCLUDE(i WITH =) DEFERRABLE INITIALLY IMMEDIATE)", 0)
		batchExec(t, p, b, a, "SET CONSTRAINTS batch_ex DEFERRED; INSERT INTO ex_t VALUES(1,1),(2,1); UPDATE ex_t SET i=2 WHERE id=2", 1)
		batchValues(t, p, b, a, "SELECT i FROM ex_t ORDER BY i", 1, 2)
		want := fixture.ExclusionReferenceConflict("ex_t", "batch_ex", "i", "3", "3")
		batchError(t, p, a, "SET CONSTRAINTS batch_ex DEFERRED; INSERT INTO ex_t VALUES(3,3),(4,3); SELECT i FROM ex_t ORDER BY i", want)
		batchValues(t, p, b, a, "SELECT i FROM ex_t ORDER BY i", 1, 2)
	})
	for _, finish := range []string{"COMMIT", "ROLLBACK"} {
		t.Run(finish+"_prepared", func(t *testing.T) {
			batchScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, unused, peer *sql.Conn) {
				f := fixture.NewControlsReferenceFixture(t, p, b.PG)
				a := f.A
				batchSeed(t, p, b, a)
				f.NoticesWant(t, "", "")
				gid := "driver_batch_" + finish
				fixture.PreparedReferenceCleanup(t, b, gid)
				literal := fixture.PreparedReferenceLiteral(gid)
				q := "INSERT INTO batch_t VALUES(1); PREPARE TRANSACTION " + literal + "; INSERT INTO batch_t VALUES(2); SELECT missing FROM batch_t"
				w := batchDiag("42703", `column "missing" does not exist`)
				w.Position = batchPosition(q, "missing")
				batchError(t, p, a, q, w)
				f.NoticesWant(t, "25P01", "there is no transaction in progress")
				batchValues(t, p, b, f.B, "SELECT i FROM batch_t", 7)
				fixture.PreparedReferenceOne(t, p, f.B, gid)
				if !b.PG {
					if err := stuntgres.Reset(p.Ctx, p.Native); err == nil {
						t.Fatal("Reset accepted prepared transaction")
					}
				}
				for _, command := range []string{"COMMIT", "ROLLBACK"} {
					q = "SELECT 1; " + command + " PREPARED " + literal
					batchError(t, p, a, q, batchDiag("25001", command+" PREPARED cannot run inside a transaction block"))
					fixture.PreparedReferenceOne(t, p, f.B, gid)
				}
				batchExec(t, p, b, a, finish+" PREPARED "+literal, 0)
				fixture.PreparedReferenceAbsent(t, p, f.B, gid)
				if finish == "COMMIT" {
					batchValues(t, p, b, f.B, "SELECT i FROM batch_t ORDER BY i", 1, 7)
				} else {
					batchValues(t, p, b, f.B, "SELECT i FROM batch_t", 7)
				}
			})
		})
	}
	batchDetachedPrivateRole(t)
}
