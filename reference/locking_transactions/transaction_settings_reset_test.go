package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTransactionSettingsResetRules(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) { transactionSettingsResetRules(t, p, b) })
	}
}

// Also called by the original IsolationResetRules entry point. Keep its RESET,
// SET DEFAULT, NULL function and post-query RESET ALL/rollback assertions here.
func transactionSettingsResetRules(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	for _, prepared := range []bool{false, true} {
		for _, name := range transactionSettingsNames {
			for _, query := range []string{
				"RESET " + name,
				"SET " + name + " TO DEFAULT",
				"SET LOCAL " + name + " TO DEFAULT",
				"SELECT set_config('" + name + "',NULL,false)",
				"SELECT set_config('" + name + "',NULL,true)",
			} {
				t.Run(fmt.Sprintf("%s/prepared=%v", query, prepared), func(t *testing.T) {
					a, _ := fixture.LockConnections(t, p.Ctx, b.DB)
					run := transactionSettingsRunner(t, p, b, a, prepared, append(transactionSettingsQueries(), query))
					transactionSettingsShow(run, "", transactionSettingsA)
					transactionSettingsShow(run, "default_", transactionSettingsA)
					run(fixture.Exec("BEGIN", 0))
					transactionSettingsShow(run, "", transactionSettingsA)
					want := fixture.ControlsReferenceDiagnostic("0A000", "parameter \""+name+"\" cannot be reset")
					s := fixture.ErrorDiagnostic(query, want)
					if query[0:6] == "SELECT" {
						s.SelectRows = true
					}
					run(s)
					run(fixture.ErrorDiagnostic("SHOW transaction_isolation", fixture.TransactionAborted()))
					run(fixture.Exec("ROLLBACK", 0))
					transactionSettingsShow(run, "", transactionSettingsA)
					transactionSettingsShow(run, "default_", transactionSettingsA)
				})
			}
		}
		for _, end := range []string{"COMMIT", "ROLLBACK"} {
			t.Run(fmt.Sprintf("ResetAll/%s/prepared=%v", end, prepared), func(t *testing.T) {
				a, _ := fixture.LockConnections(t, p.Ctx, b.DB)
				plain := transactionSettingsRunner(t, p, b, a, false, nil)
				plain(fixture.Exec("SET SESSION CHARACTERISTICS AS TRANSACTION ISOLATION LEVEL REPEATABLE READ, READ ONLY, DEFERRABLE", 0))
				run := transactionSettingsRunner(t, p, b, a, prepared, append(transactionSettingsQueries(), "RESET ALL"))
				transactionSettingsShow(run, "default_", transactionSettingsB)
				run(fixture.Exec("BEGIN", 0))
				run(fixture.Exec("SELECT num_nonnulls(1)", 1))
				run(fixture.Exec("RESET ALL", 0))
				transactionSettingsShow(run, "", transactionSettingsB)
				transactionSettingsShow(run, "default_", transactionSettingsA)
				run(fixture.Exec(end, 0))
				want := transactionSettingsA
				if end == "ROLLBACK" {
					want = transactionSettingsB
				}
				transactionSettingsShow(run, "default_", want)
				transactionSettingsShow(run, "", want)
				transactionSettingsApply(plain, transactionSettingsSet("SESSION", transactionSettingsA))
			})
		}
	}
}
