package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTransactionSettingsTemporaryMergeReadOnly(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared=%v", prepared), func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				a, peer := fixture.LockConnections(t, p.Ctx, b.DB)
				plain := transactionSettingsRunner(t, p, b, a, false, nil)
				plain(fixture.Exec("CREATE TEMP TABLE tx_tmp(v INT)", 0))
				plain(fixture.Exec("CREATE TABLE tx_perm(v INT)", 0))
				plain(fixture.Exec("INSERT INTO tx_tmp VALUES(1)", 1))
				plain(fixture.Exec("INSERT INTO tx_perm VALUES(1)", 1))
				temp := "MERGE INTO tx_tmp USING (SELECT 1 AS v) s ON tx_tmp.v=s.v WHEN MATCHED THEN UPDATE SET v=2"
				perm := "MERGE INTO tx_perm USING (SELECT 1 AS v) s ON tx_perm.v=s.v WHEN MATCHED THEN UPDATE SET v=2"
				run := transactionSettingsRunner(t, p, b, a, prepared, []string{temp, perm, "SELECT v FROM tx_tmp"})
				// Preparing both consumers neither mutates rows nor captures RW mode.
				row := func(q string, value int64) fixture.Step {
					return fixture.Select(q, []string{"v"}, []string{"INT4"}, [][]any{{value}})
				}
				run(row("SELECT v FROM tx_tmp", 1))
				fixture.RunStep(t, p.Ctx, peer, b.Recorder, row("SELECT v FROM tx_perm", 1))
				// Match fixture.MergeRun's public adapter contract: lib/pq does
				// not decode MERGE command tags. Exact rows below prove the
				// PostgreSQL effect; native must report the actual count.
				affected := int64(1)
				if b.PG {
					affected = 0
				}
				for _, end := range []string{"COMMIT", "ROLLBACK"} {
					plain(fixture.Exec("UPDATE tx_tmp SET v=1", 1))
					run(fixture.Exec("BEGIN READ ONLY", 0))
					run(fixture.Exec(temp, affected))
					run(row("SELECT v FROM tx_tmp", 2))
					run(fixture.Exec(end, 0))
					want := int64(1)
					if end == "COMMIT" {
						want = 2
					}
					run(row("SELECT v FROM tx_tmp", want))
				}
				run(fixture.Exec("BEGIN READ ONLY", 0))
				run(fixture.ErrorDiagnostic(perm, fixture.ControlsReferenceDiagnostic("25006", "cannot execute MERGE in a read-only transaction")))
				run(fixture.ErrorDiagnostic("SHOW transaction_read_only", fixture.TransactionAborted()))
				run(fixture.Exec("ROLLBACK", 0))
				transactionSettingsShow(run, "", transactionSettingsA)
				fixture.RunStep(t, p.Ctx, peer, b.Recorder, row("SELECT v FROM tx_perm", 1))
			}
		})
	}
}
