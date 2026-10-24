package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGDependTransactionsCreation(t *testing.T) {
	p := fixture.NewScenario(t)
	fixture.PgDependBackends(t, p, func(t *testing.T, b fixture.Backend) {
		fixture.PgDependExec(t, p, b, "CREATE TABLE pgdep_private_tx(a INT)")
		_, owner, peer := fixture.IsolationReferenceConnections(t, p, b.PG)
		defer owner.Close()
		defer peer.Close()
		tx, err := owner.BeginTx(p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec("SAVEPOINT before_create", 0))
		fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec("CREATE TABLE pgdep_tx_rollback(a INT)", 0))
		fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec("ROLLBACK TO before_create", 0))
		fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Select("SELECT count(*) FROM pg_catalog.pg_class WHERE relname='pgdep_tx_rollback'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec("CREATE SEQUENCE pgdep_private_tx_seq OWNED BY pgdep_private_tx.a", 0))
		var table, seq int64
		if err := tx.QueryRowContext(p.Ctx, "SELECT oid FROM pg_catalog.pg_class WHERE relname='pgdep_private_tx'").Scan(&table); err != nil {
			t.Fatal(err)
		}
		if err := tx.QueryRowContext(p.Ctx, "SELECT oid FROM pg_catalog.pg_class WHERE relname='pgdep_private_tx_seq'").Scan(&seq); err != nil {
			t.Fatal(err)
		}
		var ns int64
		if err := tx.QueryRowContext(p.Ctx, "SELECT relnamespace FROM pg_catalog.pg_class WHERE oid=$1", seq).Scan(&ns); err != nil {
			t.Fatal(err)
		}
		q := "SELECT * FROM pg_catalog.pg_depend WHERE classid=1259 AND objid=$1"
		want := [][]any{fixture.PgDependEdge(1259, seq, 0, 2615, ns, 0, "n"), fixture.PgDependEdge(1259, seq, 0, 1259, table, 1, "a")}
		fixture.PgDescriptionExecutorRows(t, p, b, tx, fixture.PgDependSelect(q, want, seq))
		fixture.PgDescriptionExecutorRows(t, p, b, peer, fixture.PgDependSelect(q, [][]any{}, seq))
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		fixture.PgDescriptionExecutorRows(t, p, b, peer, fixture.PgDependSelect(q, want, seq))
		tx, err = owner.BeginTx(p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec("SAVEPOINT before_drop", 0))
		fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec("DROP SEQUENCE pgdep_private_tx_seq", 0))
		fixture.PgDescriptionExecutorRows(t, p, b, tx, fixture.PgDependSelect(q, [][]any{}, seq))
		fixture.PgDescriptionExecutorRows(t, p, b, peer, fixture.PgDependSelect(q, want, seq))
		fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec("ROLLBACK TO before_drop", 0))
		fixture.PgDescriptionExecutorRows(t, p, b, tx, fixture.PgDependSelect(q, want, seq))
		// Failure must leave both graph and data intact after supported rollback.
		fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.ErrorDiagnostic("ALTER SEQUENCE pgdep_private_tx_seq OWNED BY pgdep_private_tx.missing", fixture.ControlsReferenceDiagnostic("42703", "column \"missing\" of relation \"pgdep_private_tx\" does not exist")))
		fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec("ROLLBACK TO before_drop", 0))
		fixture.PgDescriptionExecutorRows(t, p, b, tx, fixture.PgDependSelect(q, want, seq))
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		fixture.PgDependBag(t, p, b, 1259, seq, want...)
	})
}
