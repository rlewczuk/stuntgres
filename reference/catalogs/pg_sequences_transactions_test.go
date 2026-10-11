package reference_test

import (
	"context"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGSequencesTransactions(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			a, peer := fixture.LockConnections(t, p.Ctx, b.DB)
			owner := pgSequencesOwner(t, p, a)
			defer a.ExecContext(context.Background(), "ROLLBACK")
			pgSequenceExec(t, p, b, a, "CREATE SEQUENCE pgsv_tx")
			pgSequencesScalar(t, p, b, a, "SELECT setval('pgsv_tx',42,true)", "setval", 42)
			row := pgSequencesDefault(owner, "pgsv_tx", int64(42))
			pgSequenceExec(t, p, b, a, "BEGIN")
			pgSequenceExec(t, p, b, a, "ALTER SEQUENCE pgsv_tx START 7 RESTART 9")
			changed := append([]any(nil), row...)
			changed[4] = int64(7)
			changed[10] = nil
			pgSequencesCheck(t, p, b, a, "pgsv_tx", changed)
			// The raw definition catalog does not open the generator held by RESTART.
			fixture.PgDescriptionExecutorRows(t, p, b, peer, fixture.Select("SELECT s.seqstart FROM pg_sequence s JOIN pg_class c ON c.oid=s.seqrelid WHERE c.relname='pgsv_tx'", []string{"seqstart"}, []string{"INT8"}, [][]any{{int64(1)}}))
			pgSequenceExec(t, p, b, a, "SAVEPOINT pgsv_save")
			pgSequenceExec(t, p, b, a, "DROP SEQUENCE pgsv_tx")
			pgSequencesCheck(t, p, b, a, "pgsv_tx", nil)
			pgSequenceExec(t, p, b, a, "CREATE SEQUENCE pgsv_tx_private")
			pgSequencesCheck(t, p, b, a, "pgsv_tx_private", pgSequencesDefault(owner, "pgsv_tx_private", nil))
			pgSequencesCheck(t, p, b, peer, "pgsv_tx_private", nil)
			pgSequenceExec(t, p, b, a, "ROLLBACK TO pgsv_save")
			pgSequencesCheck(t, p, b, a, "pgsv_tx", changed)
			pgSequencesCheck(t, p, b, a, "pgsv_tx_private", nil)
			pgSequenceExec(t, p, b, a, "ROLLBACK")
			pgSequencesCheck(t, p, b, a, "pgsv_tx", row)
			pgSequenceExec(t, p, b, a, "BEGIN")
			pgSequencesScalar(t, p, b, a, "SELECT nextval('pgsv_tx')", "nextval", 43)
			pgSequenceExec(t, p, b, a, "ROLLBACK")
			row[10] = int64(43)
			pgSequencesCheck(t, p, b, peer, "pgsv_tx", row)
			pgSequenceExec(t, p, b, a, "BEGIN")
			pgSequencesScalar(t, p, b, a, "SELECT setval('pgsv_tx',50,true)", "setval", 50)
			pgSequenceExec(t, p, b, a, "ROLLBACK")
			row[10] = int64(50)
			pgSequencesCheck(t, p, b, peer, "pgsv_tx", row)
			pgSequenceExec(t, p, b, a, "BEGIN")
			pgSequenceExec(t, p, b, a, "ALTER SEQUENCE pgsv_tx RESTART 11")
			pgSequenceExec(t, p, b, a, "COMMIT")
			row[10] = nil
			pgSequencesCheck(t, p, b, peer, "pgsv_tx", row)
			pgSequenceExec(t, p, b, a, "BEGIN ISOLATION LEVEL REPEATABLE READ")
			fixture.PgDescriptionExecutorRows(t, p, b, a, fixture.Select("SELECT s.seqstart FROM pg_sequence s JOIN pg_class c ON c.oid=s.seqrelid WHERE c.relname='pgsv_tx'", []string{"seqstart"}, []string{"INT8"}, [][]any{{int64(1)}}))
			pgSequenceExec(t, p, b, peer, "ALTER SEQUENCE pgsv_tx START 7")
			pgSequencesScalar(t, p, b, peer, "SELECT nextval('pgsv_tx')", "nextval", 11)
			row[10] = int64(11)
			pgSequencesCheck(t, p, b, a, "pgsv_tx", row)
			pgSequencesScalar(t, p, b, peer, "SELECT setval('pgsv_tx',60,true)", "setval", 60)
			row[10] = int64(60)
			pgSequencesCheck(t, p, b, a, "pgsv_tx", row)
			pgSequenceExec(t, p, b, a, "COMMIT")
			row[4] = int64(7)
			pgSequencesCheck(t, p, b, a, "pgsv_tx", row)
			pgSequencesRRIdentity(t, p, b)
			pgSequencesCurrentAuthority(t, p, b)
		})
	}
}

// Capture the definition snapshot without opening the volatile helper before
// peer DDL. A dropped/recreated name must not replace the original identity.
func pgSequencesRRIdentity(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	for _, c := range []struct {
		name    string
		changes []string
		dropped bool
	}{
		{"Restart", []string{"ALTER SEQUENCE pgsv_rr_probe RESTART 19"}, false},
		{"Rename", []string{"ALTER SEQUENCE pgsv_rr_probe RENAME TO pgsv_rr_renamed"}, false},
		{"Drop", []string{"DROP SEQUENCE pgsv_rr_probe"}, true},
		{"Recreate", []string{"DROP SEQUENCE pgsv_rr_probe", "CREATE SEQUENCE pgsv_rr_probe START 99"}, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			a, peer := fixture.LockConnections(t, p.Ctx, b.DB)
			defer peer.ExecContext(context.Background(), "DROP SEQUENCE IF EXISTS pgsv_rr_probe,pgsv_rr_renamed")
			defer a.ExecContext(context.Background(), "ROLLBACK")
			pgSequenceExec(t, p, b, peer, "CREATE SEQUENCE pgsv_rr_probe")
			pgSequenceExec(t, p, b, a, "BEGIN ISOLATION LEVEL REPEATABLE READ")
			var originalOID int64
			if err := a.QueryRowContext(p.Ctx, "SELECT c.oid FROM pg_sequence s JOIN pg_class c ON c.oid=s.seqrelid WHERE c.relname='pgsv_rr_probe'").Scan(&originalOID); err != nil {
				t.Fatal(err)
			}
			if originalOID <= 0 {
				t.Fatal("missing original sequence identity", originalOID)
			}
			for _, change := range c.changes {
				pgSequenceExec(t, p, b, peer, change)
			}
			for i, s := range []fixture.Step{
				fixture.Select("SELECT sequencename,start_value FROM pg_sequences WHERE sequencename='pgsv_rr_probe'", []string{"sequencename", "start_value"}, []string{"NAME", "INT8"}, [][]any{{"pgsv_rr_probe", int64(1)}}),
				fixture.Select("SELECT sequencename,start_value,last_value FROM pg_sequences WHERE sequencename='pgsv_rr_probe'", []string{"sequencename", "start_value", "last_value"}, []string{"NAME", "INT8", "INT8"}, [][]any{{"pgsv_rr_probe", int64(1), nil}}),
			} {
				if !c.dropped {
					fixture.PgDescriptionExecutorRows(t, p, b, a, s)
					continue
				}
				want := fixture.Diagnostic{Code: "XX000", Message: fmt.Sprintf("could not open relation with OID %d", originalOID)}
				if i == 1 {
					want = fixture.Diagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}
				}
				failure := fixture.ImplicitError(s.Query, want)
				failure.SelectRows = true
				failure.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, a, b.Recorder, failure)
			}
			pgSequenceExec(t, p, b, a, "ROLLBACK")
			if c.name == "Rename" {
				pgSequencesCheck(t, p, b, a, "pgsv_rr_renamed", pgSequencesDefault(pgSequencesOwner(t, p, a), "pgsv_rr_renamed", nil))
			}
			if c.name == "Drop" {
				pgSequencesCheck(t, p, b, a, "pgsv_rr_probe", nil)
			}
			if c.name == "Recreate" {
				var replacementOID int64
				if err := peer.QueryRowContext(p.Ctx, "SELECT oid FROM pg_class WHERE relname='pgsv_rr_probe'").Scan(&replacementOID); err != nil {
					t.Fatal(err)
				}
				if replacementOID == originalOID {
					t.Fatal("recreated sequence reused original identity", originalOID)
				}
				row := pgSequencesDefault(pgSequencesOwner(t, p, a), "pgsv_rr_probe", nil)
				row[4] = int64(99)
				pgSequencesCheck(t, p, b, a, "pgsv_rr_probe", row)
			}
		})
	}
}
