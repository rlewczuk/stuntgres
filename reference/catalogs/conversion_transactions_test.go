package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceConversionTransactions(t *testing.T) {
	conversionScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		publicOID := conversionNamespaceOID(t, p, b, "public")
		conversionExec(t, p, b, conversionCreate("conv_tx_existing", false))
		oid := conversionOID(t, p, b, "conv_tx_existing")
		conn := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
		defer conn.ExecContext(p.Ctx, `ROLLBACK`)
		for _, prepared := range []bool{false, true} {
			for _, query := range []string{conversionCreate("conv_tx_new", false), `ALTER CONVERSION conv_tx_existing RENAME TO conv_tx_renamed`, `DROP CONVERSION conv_tx_existing`} {
				fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec(`BEGIN`, 0))
				step := fixture.Exec(query, 0)
				if !b.PG {
					step = fixture.ErrorDiagnostic(query, conversionDiagnostic("0A000", "statement is unsupported in an explicit transaction"))
					step.ErrorStage = "execute"
				}
				step.Prepared = prepared
				fixture.RunStep(t, p.Ctx, conn, b.Recorder, step)
				fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec(`ROLLBACK`, 0))
				conversionStep(t, p, b, conversionMetadata(oid, "conv_tx_existing", publicOID, false))
				conversionStep(t, p, b, conversionCount("conv_tx_new", 0))
				conversionStep(t, p, b, conversionCount("conv_tx_renamed", 0))
			}
			fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec(`BEGIN READ ONLY`, 0))
			step := fixture.ErrorDiagnostic(conversionCreate("conv_tx_readonly", false), conversionDiagnostic("25006", "cannot execute CREATE CONVERSION in a read-only transaction"))
			step.Prepared, step.ErrorStage = prepared, "execute"
			fixture.RunStep(t, p.Ctx, conn, b.Recorder, step)
			fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec(`ROLLBACK`, 0))
			conversionStep(t, p, b, conversionCount("conv_tx_readonly", 0))
		}
	})
}
