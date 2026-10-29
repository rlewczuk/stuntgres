package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGPartitionedTableLifecycle(t *testing.T) {
	t.Run("defaults_and_nested_membership", func(t *testing.T) {
		pptScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			run := pptRun(t, p, b, b.DB)
			run(fixture.Exec("CREATE TABLE ppt_parent(a INT,b INT) PARTITION BY RANGE(a)", 0))
			pptDefault(run, "ppt_parent", "")
			for _, q := range []string{"CREATE TABLE ppt_default(a INT,b INT)", "ALTER TABLE ppt_parent ATTACH PARTITION ppt_default DEFAULT"} {
				run(fixture.Exec(q, 0))
			}
			pptDefault(run, "ppt_parent", "ppt_default")
			run(fixture.Exec("ALTER TABLE ppt_parent DETACH PARTITION ppt_default", 0))
			pptDefault(run, "ppt_parent", "")
			run(fixture.Exec("ALTER TABLE ppt_parent ATTACH PARTITION ppt_default DEFAULT", 0))
			pptDefault(run, "ppt_parent", "ppt_default")
			run(fixture.Exec("DROP TABLE ppt_default", 0))
			pptDefault(run, "ppt_parent", "")
			for _, q := range []string{
				"CREATE TABLE ppt_mid PARTITION OF ppt_parent FOR VALUES FROM(0) TO(10) PARTITION BY LIST(b)",
				"CREATE TABLE ppt_mid_leaf PARTITION OF ppt_mid FOR VALUES IN(1)",
				"CREATE TABLE ppt_mid_default PARTITION OF ppt_mid DEFAULT",
			} {
				run(fixture.Exec(q, 0))
			}
			pptDefault(run, "ppt_parent", "")
			pptDefault(run, "ppt_mid", "ppt_mid_default")
			membership := func(want [][]any) {
				pptBoth(run, fixture.Select(`SELECT c.relname FROM pg_partitioned_table p JOIN pg_class c ON c.oid=p.partrelid WHERE c.relname IN ('ppt_parent','ppt_mid','ppt_mid_leaf','ppt_mid_default') ORDER BY c.relname`, []string{"relname"}, []string{"NAME"}, want))
			}
			membership([][]any{{"ppt_mid"}, {"ppt_parent"}})
			run(fixture.Exec("ALTER TABLE ppt_parent DETACH PARTITION ppt_mid", 0))
			membership([][]any{{"ppt_mid"}, {"ppt_parent"}})
			pptDefault(run, "ppt_mid", "ppt_mid_default")
			run(fixture.Exec("DROP TABLE ppt_mid CASCADE", 0))
			membership([][]any{{"ppt_parent"}})
		})
	})
	t.Run("transaction_visibility_and_held_reader", func(t *testing.T) {
		pptScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			writer := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
			reader := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
			write := pptRun(t, p, b, writer)
			// Hold before any DDL, but never leave PostgreSQL streaming rows open.
			q := `SELECT partstrat,partnatts,partdefid::bigint,partattrs::text,partexprs IS NULL AS no_expression FROM pg_partitioned_table WHERE partrelid=$1::oid`
			stmt, err := reader.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: reader, Statement: stmt, SQL: q}
			probe := func(e fixture.Executor, id, def int64, exists bool) {
				var want [][]any
				if exists {
					want = [][]any{{"r", int64(1), def, "1", true}}
				}
				s := fixture.Select(q, []string{"partstrat", "partnatts", "partdefid", "partattrs", "no_expression"}, []string{"CHAR", "INT2", "INT8", "TEXT", "BOOL"}, want, id)
				fixture.RunStep(t, p.Ctx, e, b.Recorder, s)
			}
			probe(held, 4294967295, 0, false)
			write(fixture.Exec("BEGIN", 0))
			defer writer.ExecContext(p.Ctx, "ROLLBACK")
			write(fixture.Exec("CREATE TABLE ppt_tx(a INT) PARTITION BY RANGE(a)", 0))
			id := pptID(t, p, writer, "ppt_tx")
			probe(writer, id, 0, true)
			probe(held, id, 0, false)
			write(fixture.Exec("ROLLBACK", 0))
			probe(held, id, 0, false)
			write(fixture.Exec("BEGIN", 0))
			write(fixture.Exec("CREATE TABLE ppt_tx(a INT) PARTITION BY RANGE(a)", 0))
			id = pptID(t, p, writer, "ppt_tx")
			probe(writer, id, 0, true)
			probe(held, id, 0, false)
			write(fixture.Exec("COMMIT", 0))
			probe(held, id, 0, true)
			write(fixture.Exec("CREATE TABLE ppt_tx_default(a INT)", 0))
			def := pptID(t, p, writer, "ppt_tx_default")
			write(fixture.Exec("BEGIN", 0))
			write(fixture.Exec("ALTER TABLE ppt_tx ATTACH PARTITION ppt_tx_default DEFAULT", 0))
			probe(writer, id, def, true)
			probe(held, id, 0, true)
			write(fixture.Exec("ROLLBACK", 0))
			probe(held, id, 0, true)
			write(fixture.Exec("BEGIN", 0))
			write(fixture.Exec("ALTER TABLE ppt_tx ATTACH PARTITION ppt_tx_default DEFAULT", 0))
			write(fixture.Exec("SAVEPOINT ppt_before_detach", 0))
			write(fixture.Exec("ALTER TABLE ppt_tx DETACH PARTITION ppt_tx_default", 0))
			probe(writer, id, 0, true)
			probe(held, id, 0, true)
			write(fixture.Exec("ROLLBACK TO SAVEPOINT ppt_before_detach", 0))
			probe(writer, id, def, true)
			write(fixture.Exec("COMMIT", 0))
			probe(held, id, def, true)
			write(fixture.Exec("BEGIN", 0))
			write(fixture.Exec("ALTER TABLE ppt_tx DETACH PARTITION ppt_tx_default", 0))
			probe(writer, id, 0, true)
			probe(held, id, def, true)
			write(fixture.Exec("ROLLBACK", 0))
			probe(held, id, def, true)
			write(fixture.Exec("BEGIN", 0))
			write(fixture.Exec("DROP TABLE ppt_tx CASCADE", 0))
			probe(writer, id, 0, false)
			probe(held, id, def, true)
			write(fixture.Exec("ROLLBACK", 0))
			probe(held, id, def, true)
			write(fixture.Exec("DROP TABLE ppt_tx CASCADE", 0))
			probe(held, id, 0, false)
			pptEmpty(pptRun(t, p, b, reader), b, "SELECT * FROM pg_partitioned_table WHERE partrelid=$1::oid", id)
		})
	})
}
