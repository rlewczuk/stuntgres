package reference_test

import (
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

var transactionSettingsNames = [3]string{"transaction_isolation", "transaction_read_only", "transaction_deferrable"}
var transactionSettingsA = [3]string{"read committed", "off", "off"}
var transactionSettingsB = [3]string{"repeatable read", "on", "on"}
var transactionSettingsC = [3]string{"read uncommitted", "off", "off"}
var transactionSettingsD = [3]string{"serializable", "off", "on"}

// Prepare selected consumers once, before BEGIN. Controls not in queries remain
// direct; every selected mutation/SHOW/query reuses its actual held handle.
func transactionSettingsRunner(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, queries []string) func(fixture.Step) {
	t.Helper()
	held := map[string]*sql.Stmt{}
	if prepared {
		for _, q := range queries {
			if held[q] != nil {
				continue
			}
			s, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatalf("Prepare %q: %v", q, err)
			}
			held[q] = s
			t.Cleanup(func() { s.Close() })
		}
	}
	return func(s fixture.Step) {
		t.Helper()
		var e fixture.Executor = c
		if h := held[s.Query]; h != nil {
			e = fixture.HeldStatement{Executor: c, Statement: h, SQL: s.Query}
		}
		fixture.RunStep(t, p.Ctx, e, b.Recorder, s)
	}
}

func transactionSettingsText(q, column, value string) fixture.Step {
	return fixture.Select(q, []string{column}, []string{"TEXT"}, [][]any{{value}})
}

func transactionSettingsQueries() []string {
	var queries []string
	for _, name := range transactionSettingsNames {
		for _, prefix := range []string{"", "default_"} {
			queries = append(queries, "SHOW "+prefix+name)
		}
	}
	return queries
}

func transactionSettingsShow(run func(fixture.Step), prefix string, values [3]string) {
	for i, name := range transactionSettingsNames {
		name = prefix + name
		run(transactionSettingsText("SHOW "+name, name, values[i]))
		run(fixture.Exec("SHOW "+name, 0))
	}
}

func transactionSettingsSet(scope string, values [3]string) []string {
	var queries []string
	for i, name := range transactionSettingsNames {
		queries = append(queries, "SET "+scope+" default_"+name+" TO '"+values[i]+"'")
	}
	return queries
}

func transactionSettingsApply(run func(fixture.Step), queries []string) {
	for _, q := range queries {
		run(fixture.Exec(q, 0))
	}
}

func TestReferenceTransactionSettingsDefaultsLifecycle(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, end := range []string{"COMMIT", "ROLLBACK"} {
			for _, tc := range []struct {
				name               string
				commands           []string
				visible, committed [3]string
				marks              bool
			}{
				{"Local", transactionSettingsSet("LOCAL", transactionSettingsC), transactionSettingsC, transactionSettingsA, false},
				{"SessionLocal", append(transactionSettingsSet("SESSION", transactionSettingsB), transactionSettingsSet("LOCAL", transactionSettingsC)...), transactionSettingsC, transactionSettingsB, false},
				{"LocalSession", append(transactionSettingsSet("LOCAL", transactionSettingsC), transactionSettingsSet("SESSION", transactionSettingsB)...), transactionSettingsB, transactionSettingsB, false},
				{"Repeated", append(append(append(transactionSettingsSet("SESSION", transactionSettingsD), transactionSettingsSet("LOCAL", transactionSettingsA)...), transactionSettingsSet("SESSION", transactionSettingsB)...), transactionSettingsSet("LOCAL", transactionSettingsC)...), transactionSettingsC, transactionSettingsB, false},
				{"DuplicateMarks", transactionSettingsSet("SESSION", transactionSettingsB), transactionSettingsC, transactionSettingsB, true},
				{"DefaultAll", append(append(transactionSettingsSet("SESSION", transactionSettingsB), []string{"SET LOCAL default_transaction_isolation=DEFAULT", "SET LOCAL default_transaction_read_only=DEFAULT", "SET LOCAL default_transaction_deferrable=DEFAULT"}...), "RESET ALL"), transactionSettingsA, transactionSettingsB, false},
				{"ExplicitDefaultAll", append(append(transactionSettingsSet("SESSION", transactionSettingsB), transactionSettingsSet("LOCAL", transactionSettingsA)...), "RESET ALL"), transactionSettingsA, transactionSettingsA, false},
			} {
				t.Run(fmt.Sprintf("%s/%s/prepared=%v", tc.name, end, prepared), func(t *testing.T) {
					p := fixture.NewScenario(t)
					for _, b := range p.Backends() {
						a, peer := fixture.LockConnections(t, p.Ctx, b.DB)
						plain := transactionSettingsRunner(t, p, b, a, false, nil)
						transactionSettingsApply(plain, transactionSettingsSet("SESSION", transactionSettingsA))
						queries := append(transactionSettingsQueries(), tc.commands...)
						for _, values := range [][3]string{transactionSettingsA, transactionSettingsB, transactionSettingsC, transactionSettingsD} {
							queries = append(queries, transactionSettingsSet("LOCAL", values)...)
						}
						queries = append(queries, "SELECT current_setting('transaction_isolation')")
						run := transactionSettingsRunner(t, p, b, a, prepared, queries)
						transactionSettingsShow(run, "default_", transactionSettingsA)
						run(fixture.Exec("BEGIN ISOLATION LEVEL READ COMMITTED READ WRITE NOT DEFERRABLE", 0))
						transactionSettingsApply(run, tc.commands)
						if tc.marks {
							run(fixture.Exec("SAVEPOINT same", 0))
							transactionSettingsApply(run, transactionSettingsSet("LOCAL", transactionSettingsC))
							run(fixture.Exec("SAVEPOINT same", 0))
							transactionSettingsApply(run, transactionSettingsSet("LOCAL", transactionSettingsD))
							run(fixture.Exec("ROLLBACK TO same", 0))
							transactionSettingsShow(run, "default_", transactionSettingsC)
							run(fixture.Exec("RELEASE same", 0))
							for range 2 {
								run(fixture.Exec("ROLLBACK TO same", 0))
								transactionSettingsShow(run, "default_", transactionSettingsB)
							}
							transactionSettingsApply(run, transactionSettingsSet("LOCAL", transactionSettingsC))
							run(fixture.Exec("RELEASE same", 0))
						}
						transactionSettingsShow(run, "default_", tc.visible)
						transactionSettingsShow(run, "", transactionSettingsA)
						run(transactionSettingsText("SELECT current_setting('transaction_isolation')", "current_setting", "read committed"))
						run(fixture.Exec("SELECT current_setting('transaction_isolation')", 1))
						run(fixture.Exec(end, 0))
						want := transactionSettingsA
						if end == "COMMIT" {
							want = tc.committed
						}
						transactionSettingsShow(run, "default_", want)
						run(fixture.Exec("BEGIN", 0))
						transactionSettingsShow(run, "", want)
						run(fixture.Exec("ROLLBACK", 0))
						peerRun := transactionSettingsRunner(t, p, b, peer, false, nil)
						transactionSettingsShow(peerRun, "default_", transactionSettingsA)
						transactionSettingsShow(peerRun, "", transactionSettingsA)
						transactionSettingsApply(plain, transactionSettingsSet("SESSION", transactionSettingsA))
					}
				})
			}
		}
	}
	// Each reset route must restore the captured default, not merely display a
	// value equal to it. LOCAL function reset retains the session commit candidate.
	for _, prepared := range []bool{false, true} {
		for i, name := range transactionSettingsNames {
			name = "default_" + name
			for _, route := range []string{"RESET " + name, "SET " + name + " TO DEFAULT", "SELECT set_config('" + name + "',NULL,false)", "SELECT set_config('" + name + "',NULL,true)"} {
				for _, end := range []string{"COMMIT", "ROLLBACK"} {
					t.Run(fmt.Sprintf("%s/%s/prepared=%v", route, end, prepared), func(t *testing.T) {
						p := fixture.NewScenario(t)
						for _, b := range p.Backends() {
							a, _ := fixture.LockConnections(t, p.Ctx, b.DB)
							commands := transactionSettingsSet("SESSION", transactionSettingsB)
							run := transactionSettingsRunner(t, p, b, a, prepared, append(append(transactionSettingsQueries(), commands...), route))
							transactionSettingsShow(run, "default_", transactionSettingsA)
							run(fixture.Exec("BEGIN ISOLATION LEVEL READ COMMITTED READ WRITE NOT DEFERRABLE", 0))
							transactionSettingsApply(run, commands)
							if route[0:6] == "SELECT" {
								run(transactionSettingsText(route, "set_config", transactionSettingsA[i]))
								run(fixture.Exec(route, 1))
							} else {
								run(fixture.Exec(route, 0))
							}
							visible := transactionSettingsB
							visible[i] = transactionSettingsA[i]
							transactionSettingsShow(run, "default_", visible)
							transactionSettingsShow(run, "", transactionSettingsA)
							run(fixture.Exec(end, 0))
							want := transactionSettingsA
							if end == "COMMIT" {
								want = visible
								if route == "SELECT set_config('"+name+"',NULL,true)" {
									want = transactionSettingsB
								}
							}
							transactionSettingsShow(run, "default_", want)
							transactionSettingsApply(run, transactionSettingsSet("SESSION", transactionSettingsA))
						}
					})
				}
			}
		}
	}
}

func TestReferenceTransactionSettingsSmoke(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		a, peer := fixture.LockConnections(t, p.Ctx, b.DB)
		run := transactionSettingsRunner(t, p, b, a, false, nil)
		run(fixture.Exec("CREATE TABLE tx_smoke(v INT)", 0))
		run(fixture.Exec("INSERT INTO tx_smoke VALUES(1)", 1))
		transactionSettingsApply(run, transactionSettingsSet("SESSION", transactionSettingsB))
		run(fixture.Exec("BEGIN", 0))
		transactionSettingsShow(run, "", transactionSettingsB)
		run(fixture.Exec("SAVEPOINT s", 0))
		transactionSettingsApply(run, transactionSettingsSet("LOCAL", transactionSettingsA))
		run(fixture.Exec("ROLLBACK TO s", 0))
		transactionSettingsShow(run, "default_", transactionSettingsB)
		transactionSettingsShow(run, "", transactionSettingsB)
		run(fixture.Exec("ROLLBACK", 0))
		transactionSettingsApply(run, transactionSettingsSet("SESSION", transactionSettingsA))
		run(fixture.Exec("BEGIN", 0))
		run(transactionSettingsText("SELECT current_setting('transaction_isolation')", "current_setting", "read committed"))
		run(fixture.ErrorDiagnostic("SET transaction_isolation='repeatable read'", fixture.ControlsReferenceDiagnostic("25001", "SET TRANSACTION ISOLATION LEVEL must be called before any query")))
		run(fixture.ErrorDiagnostic("SHOW transaction_isolation", fixture.TransactionAborted()))
		run(fixture.Exec("ROLLBACK", 0))
		run(fixture.Exec("BEGIN READ ONLY", 0))
		run(fixture.ErrorDiagnostic("INSERT INTO tx_smoke VALUES(2)", fixture.ControlsReferenceDiagnostic("25006", "cannot execute INSERT in a read-only transaction")))
		run(fixture.Exec("ROLLBACK", 0))
		fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Select("SELECT v FROM tx_smoke", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}}))
	}
}
