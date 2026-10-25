package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceConversionTransactionDefaultMove(t *testing.T) {
	for _, commit := range []bool{false, true} {
		name := "rollback"
		if commit {
			name = "commit"
		}
		t.Run(name, func(t *testing.T) {
			conversionScenario(t, []string{"conv_tx_old", "conv_tx_new"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				conversionExec(t, p, b, "CREATE SCHEMA conv_tx_old", "CREATE SCHEMA conv_tx_new", fixture.ConversionCreate("conv_tx_old.a", true), fixture.ConversionCreate("conv_tx_new.b", true))
				oid := conversionOID(t, p, b, "a")
				old := conversionNamespaceOID(t, p, b, "conv_tx_old")
				dest := conversionNamespaceOID(t, p, b, "conv_tx_new")
				c := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
				defer c.ExecContext(p.Ctx, "ROLLBACK")
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				q := "ALTER CONVERSION conv_tx_old.a SET SCHEMA conv_tx_new"
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				run(fixture.Exec("BEGIN", 0))
				run(fixture.Exec("SAVEPOINT original", 0))
				for range 2 {
					fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, fixture.Exec(q, 0))
					run(fixture.Select("SELECT conname,condefault FROM pg_conversion WHERE connamespace=$1 ORDER BY conname", []string{"conname", "condefault"}, []string{"NAME", "BOOL"}, [][]any{{"a", true}, {"b", true}}, dest))
					run(fixture.Exec("SET LOCAL search_path=conv_tx_new,pg_catalog", 0))
					run(conversionValue())
					run(fixture.Exec("ROLLBACK TO original", 0))
					run(fixture.Select("SELECT oid::bigint,connamespace::bigint FROM pg_conversion WHERE oid=$1", []string{"oid", "connamespace"}, []string{"INT8", "INT8"}, [][]any{{oid, old}}, oid))
				}
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, fixture.Exec(q, 0))
				run(fixture.Exec("RELEASE original", 0))
				end := "ROLLBACK"
				if commit {
					end = "COMMIT"
				}
				run(fixture.Exec(end, 0))
				namespace := old
				if commit {
					namespace = dest
				}
				conversionStep(t, p, b, conversionMetadata(oid, "a", namespace, true))
			})
		})
	}
}
