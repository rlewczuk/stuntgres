package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePartitionAttachTransactions(t *testing.T) {
	for _, finish := range []string{"commit", "rollback", "savepoint"} {
		for _, prepared := range []bool{false, true} {
			name := finish + "/direct"
			if prepared {
				name = finish + "/prepared"
			}
			t.Run(name, func(t *testing.T) {
				p := fixture.NewScenario(t)
				for _, b := range p.Backends() {
					t.Run(b.Name, func(t *testing.T) {
						run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
						run(fixture.Exec("CREATE TABLE pa_tx_parent(k INT,CONSTRAINT pa_tx_key UNIQUE(k)) PARTITION BY RANGE(k)", 0))
						run(fixture.Exec("CREATE TABLE pa_tx_sibling PARTITION OF pa_tx_parent FOR VALUES FROM(20) TO(30)", 0))
						run(fixture.Exec("INSERT INTO pa_tx_sibling VALUES(21)", 1))
						run(fixture.Exec("CREATE TABLE pa_tx_candidate(k INT)", 0))
						run(fixture.Exec("INSERT INTO pa_tx_candidate VALUES(1),(2)", 2))
						tx, err := b.DB.BeginTx(p.Ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						defer tx.Rollback()
						own := func(s fixture.Step) { s.Prepared = prepared; fixture.RunStep(t, p.Ctx, tx, b.Recorder, s) }
						if finish == "savepoint" {
							own(fixture.Exec("SAVEPOINT before_attach", 0))
						}
						own(fixture.Exec("ALTER TABLE pa_tx_parent ATTACH PARTITION pa_tx_candidate FOR VALUES FROM(1) TO(3)", 0))
						own(referenceRangeRows("SELECT k FROM pa_tx_parent ORDER BY k", 1, 2, 21))
						own(partitionConstraintEdge("pa_tx_candidate_k_key", "pa_tx_key", "pa_tx_candidate"))
						run(referenceRangeRows("SELECT k FROM pa_tx_parent ORDER BY k", 21))
						if finish == "commit" {
							if err := tx.Commit(); err != nil {
								t.Fatal(err)
							}
							run(referenceRangeRows("SELECT k FROM pa_tx_parent ORDER BY k", 1, 2, 21))
							run(partitionConstraintDuplicate("INSERT INTO pa_tx_parent VALUES(1)", "pa_tx_candidate", "pa_tx_candidate_k_key", "Key (k)=(1) already exists."))
						} else {
							if finish == "savepoint" {
								own(fixture.Exec("ROLLBACK TO SAVEPOINT before_attach", 0))
								own(referenceRangeRows("SELECT k FROM pa_tx_parent ORDER BY k", 21))
								// The candidate AX and newly created index disappear at
								// the same canonical savepoint boundary, before outer end.
								run(referenceRangeRows("SELECT k FROM pa_tx_candidate ORDER BY k", 1, 2))
								own(fixture.Exec("RELEASE SAVEPOINT before_attach", 0))
								if err := tx.Commit(); err != nil {
									t.Fatal(err)
								}
							} else {
								if err := tx.Rollback(); err != nil {
									t.Fatal(err)
								}
							}
							run(referenceRangeRows("SELECT k FROM pa_tx_parent ORDER BY k", 21))
							run(fixture.Select("SELECT relispartition FROM pg_class WHERE relname='pa_tx_candidate'", []string{"relispartition"}, []string{"BOOL"}, [][]any{{false}}))
							run(fixture.Select("SELECT COUNT(*) FROM pg_class WHERE relname='pa_tx_candidate_k_key'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
							run(fixture.Exec("INSERT INTO pa_tx_candidate VALUES(8)", 1))
							run(fixture.Exec("ALTER TABLE pa_tx_parent ATTACH PARTITION pa_tx_candidate FOR VALUES FROM(1) TO(10)", 0))
							run(referenceRangeRows("SELECT k FROM pa_tx_parent ORDER BY k", 1, 2, 8, 21))
						}
					})
				}
			})
		}
	}
	t.Run("read_only_and_aborted", func(t *testing.T) {
		p := fixture.NewScenario(t)
		for _, b := range p.Backends() {
			t.Run(b.Name, func(t *testing.T) {
				conn, err := b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer conn.Close()
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
				run(fixture.Exec("BEGIN READ ONLY", 0))
				defer conn.ExecContext(p.Ctx, "ROLLBACK")
				q := "ALTER TABLE missing_parent ATTACH PARTITION missing_candidate DEFAULT"
				run(fixture.ImplicitError(q, fixture.Diagnostic{Code: "25006", Message: "cannot execute ALTER TABLE in a read-only transaction"}))
				run(fixture.ImplicitError(q, fixture.Diagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}))
				run(fixture.Exec("ROLLBACK", 0))
			})
		}
	})
	partitionAttachValidationSnapshots(t)
}
