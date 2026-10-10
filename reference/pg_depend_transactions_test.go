package reference_test

import "testing"

func TestReferencePGDependTransactionsCreation(t *testing.T) {
	p := newReferenceScenario(t)
	pgDependBackends(t, p, func(t *testing.T, b referenceBackend) {
		pgDependExec(t, p, b, "CREATE TABLE pgdep_private_tx(a INT)")
		_, owner, peer := isolationReferenceConnections(t, p, b.pg)
		defer owner.Close()
		defer peer.Close()
		tx, err := owner.BeginTx(p.ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		if !b.pg {
			referenceRunStep(t, p.ctx, tx, b.recorder, referenceExec("SAVEPOINT unsupported_create", 0))
			referenceRunStep(t, p.ctx, tx, b.recorder, referenceErrorDiagnostic("CREATE TABLE pgdep_tx_unsupported(a INT)", controlsReferenceDiagnostic("0A000", "statement is unsupported in an explicit transaction")))
			referenceRunStep(t, p.ctx, tx, b.recorder, referenceExec("ROLLBACK TO unsupported_create", 0))
		}
		referenceRunStep(t, p.ctx, tx, b.recorder, referenceExec("CREATE SEQUENCE pgdep_private_tx_seq OWNED BY pgdep_private_tx.a", 0))
		var table, seq int64
		if err := tx.QueryRowContext(p.ctx, "SELECT oid FROM pg_catalog.pg_class WHERE relname='pgdep_private_tx'").Scan(&table); err != nil {
			t.Fatal(err)
		}
		if err := tx.QueryRowContext(p.ctx, "SELECT oid FROM pg_catalog.pg_class WHERE relname='pgdep_private_tx_seq'").Scan(&seq); err != nil {
			t.Fatal(err)
		}
		var ns int64
		if err := tx.QueryRowContext(p.ctx, "SELECT relnamespace FROM pg_catalog.pg_class WHERE oid=$1", seq).Scan(&ns); err != nil {
			t.Fatal(err)
		}
		q := "SELECT * FROM pg_catalog.pg_depend WHERE classid=1259 AND objid=$1"
		want := [][]any{pgDependEdge(1259, seq, 0, 2615, ns, 0, "n"), pgDependEdge(1259, seq, 0, 1259, table, 1, "a")}
		pgDescriptionExecutorRows(t, p, b, tx, pgDependSelect(q, want, seq))
		pgDescriptionExecutorRows(t, p, b, peer, pgDependSelect(q, [][]any{}, seq))
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		pgDescriptionExecutorRows(t, p, b, peer, pgDependSelect(q, want, seq))
		tx, err = owner.BeginTx(p.ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		referenceRunStep(t, p.ctx, tx, b.recorder, referenceExec("SAVEPOINT before_drop", 0))
		referenceRunStep(t, p.ctx, tx, b.recorder, referenceExec("DROP SEQUENCE pgdep_private_tx_seq", 0))
		pgDescriptionExecutorRows(t, p, b, tx, pgDependSelect(q, [][]any{}, seq))
		pgDescriptionExecutorRows(t, p, b, peer, pgDependSelect(q, want, seq))
		referenceRunStep(t, p.ctx, tx, b.recorder, referenceExec("ROLLBACK TO before_drop", 0))
		pgDescriptionExecutorRows(t, p, b, tx, pgDependSelect(q, want, seq))
		// Failure must leave both graph and data intact after supported rollback.
		referenceRunStep(t, p.ctx, tx, b.recorder, referenceErrorDiagnostic("ALTER SEQUENCE pgdep_private_tx_seq OWNED BY pgdep_private_tx.missing", controlsReferenceDiagnostic("42703", "column \"missing\" of relation \"pgdep_private_tx\" does not exist")))
		referenceRunStep(t, p.ctx, tx, b.recorder, referenceExec("ROLLBACK TO before_drop", 0))
		pgDescriptionExecutorRows(t, p, b, tx, pgDependSelect(q, want, seq))
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		pgDependBag(t, p, b, 1259, seq, want...)
	})
}
