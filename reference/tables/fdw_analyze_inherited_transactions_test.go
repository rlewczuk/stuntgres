package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceFDWAnalyzeInheritedTransactions(t *testing.T) {
	for _, kind := range []string{"ordinary", "partition", "foreign"} {
		for _, ending := range []string{"rollback", "savepoint", "commit", "prepared-commit", "prepared-rollback"} {
			t.Run(kind+"/"+ending, func(t *testing.T) {
				fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
					local, _ := fdwAnalyzeMixedSetup(t, p, b, r, run, kind, 10, 30)
					run(fixture.Exec("ANALYZE ONLY fdw_analyze_parent", 0))
					observer, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer observer.Close()
					observe := func(step fixture.Step) { fixture.RunStep(t, p.Ctx, observer, b.Recorder, step) }
					run(fixture.Exec("BEGIN", 0))
					if ending == "savepoint" {
						run(fixture.Exec("SAVEPOINT analyze_before", 0))
					}
					run(fixture.Exec("UPDATE ONLY "+local+" SET label='transaction'", 10))
					run(fixture.Prepared(fixture.Exec("ANALYZE ONLY fdw_analyze_parent(label)", 0)))
					fdwAnalyzeTransactionFacts(t, p, b, c, run, "transaction")
					fdwAnalyzeTransactionFacts(t, p, b, observer, observe, "local")
					switch ending {
					case "savepoint":
						run(fixture.Exec("ROLLBACK TO SAVEPOINT analyze_before", 0))
						fdwAnalyzeTransactionFacts(t, p, b, c, run, "local")
						run(fixture.Exec("COMMIT", 0))
					case "rollback":
						run(fixture.Exec("ROLLBACK", 0))
					case "commit":
						run(fixture.Exec("COMMIT", 0))
					default:
						gid := "fdw_analyze_inherited_" + kind
						if b.PG {
							// Real postgres_fdw refuses two-phase completion after
							// remote use; the native metadata-only sampler has no
							// external transaction participant. This is not parity.
							run(fixture.ErrorDiagnostic("PREPARE TRANSACTION '"+gid+"'", fdwDiagnostic("0A000", "cannot PREPARE a transaction that has operated on postgres_fdw foreign tables")))
							run(fixture.Exec("ROLLBACK", 0))
							fdwAnalyzeTransactionFacts(t, p, b, c, run, "local")
							return
						}
						defer c.ExecContext(p.Ctx, "ROLLBACK PREPARED '"+gid+"'")
						run(fixture.Exec("PREPARE TRANSACTION '"+gid+"'", 0))
						fdwAnalyzeTransactionFacts(t, p, b, observer, observe, "local")
						completion := "ROLLBACK"
						if ending == "prepared-commit" {
							completion = "COMMIT"
						}
						run(fixture.Exec(completion+" PREPARED '"+gid+"'", 0))
					}
					label := "local"
					if ending == "commit" || ending == "prepared-commit" {
						label = "transaction"
					}
					fdwAnalyzeTransactionFacts(t, p, b, observer, observe, label)
					run(fixture.Prepared(fixture.Select("SELECT count(*) FROM fdw_analyze_parent WHERE label='"+label+"'", []string{"count"}, []string{"INT8"}, [][]any{{int64(10)}})))
				})
			})
		}
	}
}

func fdwAnalyzeTransactionFacts(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), label string) {
	t.Helper()
	if b.PG {
		run(fixture.Select("SELECT most_common_vals::text AS vals,most_common_freqs::text AS freqs FROM pg_stats WHERE schemaname='public' AND tablename='fdw_analyze_parent' AND inherited AND attname='label'", []string{"vals", "freqs"}, []string{"TEXT", "TEXT"}, [][]any{{"{foreign," + label + "}", "{0.75,0.25}"}}))
	} else {
		fdwAnalyzeHierarchyEstimate(t, p, c, "label='"+label+"'", 10)
		fdwAnalyzeHierarchyEstimate(t, p, c, "label='foreign'", 30)
	}
}
