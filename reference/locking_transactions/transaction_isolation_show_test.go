package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTransactionIsolationShowAlias(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared=%v", prepared), func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				a, peer := fixture.LockConnections(t, p.Ctx, b.DB)
				queries := []string{
					"SHOW TRANSACTION ISOLATION LEVEL",
					"sHoW /* current mode */ TrAnSaCtIoN IsOlAtIoN LeVeL",
					"SHOW transaction_isolation",
					"SHOW default_transaction_isolation",
				}
				// Hold SHOW handles before any default/current mode changes. Unlike
				// a data-query Prepare, these must not establish a query snapshot.
				run := transactionSettingsRunner(t, p, b, a, prepared, queries)
				show := func(want string) {
					for _, q := range queries[:3] {
						run(transactionSettingsText(q, "transaction_isolation", want))
						run(fixture.Exec(q, 0))
					}
				}
				showDefault := func(want string) {
					run(transactionSettingsText(queries[3], "default_transaction_isolation", want))
				}
				show("read committed")
				showDefault("read committed")
				run(fixture.Exec("SET default_transaction_isolation='repeatable read'", 0))
				run(fixture.Exec("BEGIN", 0))
				show("repeatable read")
				showDefault("repeatable read")
				run(fixture.Exec("SET default_transaction_isolation='serializable'", 0))
				showDefault("serializable")
				show("repeatable read")
				// Reading either spelling must leave a real current-mode change legal.
				run(fixture.Exec("SET TRANSACTION ISOLATION LEVEL READ UNCOMMITTED", 0))
				show("read uncommitted")
				run(transactionSettingsText("SELECT current_setting('transaction_isolation')", "current_setting", "read uncommitted"))
				run(fixture.Exec("COMMIT", 0))
				run(fixture.Exec("BEGIN", 0))
				show("serializable")
				run(fixture.Exec("ROLLBACK", 0))
				run(fixture.Exec("RESET default_transaction_isolation", 0))
				run(fixture.Exec("BEGIN", 0))
				show("read committed")
				showDefault("read committed")
				run(fixture.Exec("ROLLBACK", 0))
				peerRun := transactionSettingsRunner(t, p, b, peer, false, nil)
				peerRun(transactionSettingsText(queries[0], "transaction_isolation", "read committed"))
				peerRun(transactionSettingsText(queries[3], "default_transaction_isolation", "read committed"))
			}
		})
	}
}
