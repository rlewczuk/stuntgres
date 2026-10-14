package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePartitionDetachTransactions(t *testing.T) {
	for _, finish := range []string{"commit", "rollback", "savepoint", "dependent_abort"} {
		for _, prepared := range []bool{false, true} {
			name := finish + "/direct"
			if prepared {
				name = finish + "/prepared"
			}
			t.Run(name, func(t *testing.T) {
				partitionDetachScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
					run(fixture.Exec("CREATE TABLE pd_tx(k INT,CONSTRAINT pd_tx_key UNIQUE(k)) PARTITION BY RANGE(k)", 0))
					run(fixture.Exec("CREATE TABLE pd_tx_a PARTITION OF pd_tx FOR VALUES FROM(1) TO(3)", 0))
					run(fixture.Exec("CREATE TABLE pd_tx_b PARTITION OF pd_tx DEFAULT", 0))
					run(fixture.Exec("INSERT INTO pd_tx VALUES(1),(3)", 2))
					run(fixture.Exec("CREATE VIEW pd_tx_view AS SELECT k FROM pd_tx_a", 0))
					tx, err := b.DB.BeginTx(p.Ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback()
					own := func(s fixture.Step) { s.Prepared = prepared; fixture.RunStep(t, p.Ctx, tx, b.Recorder, s) }
					own(fixture.Exec("INSERT INTO pd_tx_a VALUES(2)", 1))
					if finish == "savepoint" {
						own(fixture.Exec("SAVEPOINT before_detach", 0))
					}
					own(fixture.Exec("ALTER TABLE pd_tx DETACH PARTITION pd_tx_a", 0))
					own(referenceRangeRows("SELECT k FROM pd_tx", 3))
					own(partitionDetachClass("pd_tx_a", false))
					own(partitionDetachTopEdges("pd_tx_a_k_key", false))
					own(fixture.Exec("INSERT INTO pd_tx_a VALUES(4)", 1))
					own(referenceRangeRows("SELECT k FROM pd_tx_a ORDER BY k", 1, 2, 4))
					if finish == "dependent_abort" {
						drop := fixture.Diagnostic{Code: "2BP01", Message: "cannot drop table pd_tx_a because other objects depend on it", Detail: "view pd_tx_view depends on table pd_tx_a", Hint: "Use DROP ... CASCADE to drop the dependent objects too."}
						if !b.PG {
							// DROP in an explicit native transaction remains outside
							// its admitted utility set; do not enable it for DETACH.
							drop = fixture.Diagnostic{Code: "0A000", Message: "statement is unsupported in an explicit transaction"}
						}
						dropStep := fixture.ImplicitError("DROP TABLE pd_tx_a RESTRICT", drop)
						dropStep.ErrorStage = "execute"
						own(dropStep)
						own(fixture.ImplicitError("SELECT k FROM pd_tx", fixture.Diagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}))
					}
					if finish == "commit" {
						if err := tx.Commit(); err != nil {
							t.Fatal(err)
						}
						run(referenceRangeRows("SELECT k FROM pd_tx", 3))
						run(referenceRangeRows("SELECT k FROM pd_tx_a ORDER BY k", 1, 2, 4))
						run(partitionDetachMember("pd_tx_a", "pd_tx", false))
						run(partitionConstraintDuplicate("INSERT INTO pd_tx_a VALUES(2)", "pd_tx_a", "pd_tx_a_k_key", "Key (k)=(2) already exists."))
						return
					}
					if finish == "savepoint" {
						own(fixture.Exec("ROLLBACK TO SAVEPOINT before_detach", 0))
						own(referenceRangeRows("SELECT k FROM pd_tx ORDER BY k", 1, 2, 3))
						own(partitionConstraintEdge("pd_tx_a_k_key", "pd_tx_key", "pd_tx_a"))
						// No AX remains after rollback-to: an independent reader can
						// complete before outer end (it cannot see private insert2).
						run(referenceRangeRows("SELECT k FROM pd_tx ORDER BY k", 1, 3))
						own(fixture.Exec("RELEASE SAVEPOINT before_detach", 0))
						if err := tx.Commit(); err != nil {
							t.Fatal(err)
						}
						run(referenceRangeRows("SELECT k FROM pd_tx ORDER BY k", 1, 2, 3))
					} else {
						if err := tx.Rollback(); err != nil {
							t.Fatal(err)
						}
						run(referenceRangeRows("SELECT k FROM pd_tx ORDER BY k", 1, 3))
						run(referenceRangeRows("SELECT k FROM pd_tx_a", 1))
					}
					run(partitionDetachClass("pd_tx_a", true))
					run(partitionConstraintEdge("pd_tx_a_k_key", "pd_tx_key", "pd_tx_a"))
					run(partitionDetachMember("pd_tx_a", "pd_tx", true))
					run(partitionConstraintDuplicate("INSERT INTO pd_tx VALUES(1)", "pd_tx_a", "pd_tx_a_k_key", "Key (k)=(1) already exists."))
					run(fixture.Exec("ALTER TABLE pd_tx DETACH PARTITION pd_tx_a", 0))
					run(referenceRangeRows("SELECT k FROM pd_tx", 3))
					if finish == "dependent_abort" {
						// After rollback and the supported standalone DETACH,
						// both servers protect the surviving dependent view.
						run(fixture.ImplicitError("DROP TABLE pd_tx_a RESTRICT", fixture.Diagnostic{Code: "2BP01", Message: "cannot drop table pd_tx_a because other objects depend on it", Detail: "view pd_tx_view depends on table pd_tx_a", Hint: "Use DROP ... CASCADE to drop the dependent objects too."}))
						run(referenceRangeRows("SELECT k FROM pd_tx_view", 1))
						run(partitionDetachMember("pd_tx_a", "pd_tx", false))
					}
				})
			})
		}
	}
	t.Run("read_only", func(t *testing.T) {
		partitionDetachScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			conn, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { conn.Close() })
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
			run(fixture.Exec("BEGIN READ ONLY", 0))
			defer conn.ExecContext(p.Ctx, "ROLLBACK")
			q := "ALTER TABLE pd_missing DETACH PARTITION pd_missing_child"
			held := partitionDetachHold(t, p, conn, q)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.ImplicitError(q, fixture.Diagnostic{Code: "25006", Message: "cannot execute ALTER TABLE in a read-only transaction"}))
			run(fixture.ImplicitError(q, fixture.Diagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}))
			run(fixture.Exec("ROLLBACK", 0))
		})
	})
}
