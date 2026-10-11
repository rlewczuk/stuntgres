package reference_test

import (
	"context"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGDependSnapshots(t *testing.T) {
	p := fixture.NewScenario(t)
	fixture.PgDependBackends(t, p, func(t *testing.T, b fixture.Backend) {
		fixture.PgDependExec(t, p, b, "CREATE TABLE pgdep_snapshot(a INT DEFAULT 1)")
		table := fixture.PgDependRelation(t, b, "pgdep_snapshot")
		d := fixture.PgDependDefinition(t, b, table, 1)
		q := "SELECT * FROM pg_catalog.pg_depend WHERE classid=2604 AND objid=$1"
		stmt, err := b.DB.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		old, err := stmt.QueryContext(p.Ctx, d)
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		types, err := old.ColumnTypes()
		if err != nil || len(types) != 7 {
			t.Fatal(types, err)
		}
		fixture.PgDependExec(t, p, b, "ALTER TABLE pgdep_snapshot ALTER a DROP DEFAULT")
		fixture.PgDescriptionExecutorRows(t, p, b, fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}, fixture.PgDependSelect(q, [][]any{}, d))
		fixture.PgDependRetained(t, old, [][]any{fixture.PgDependEdge(2604, d, 0, 1259, table, 1, "a")})
		if types[0].DatabaseTypeName() != "OID" {
			t.Fatal("retained descriptor")
		}
		fixture.PgDependExec(t, p, b, "ALTER TABLE pgdep_snapshot ALTER a SET DEFAULT 2")
		fresh := fixture.PgDependDefinition(t, b, table, 1)
		if fresh == d {
			t.Fatal("fresh default reused identity")
		}
		fixture.PgDescriptionExecutorRows(t, p, b, fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}, fixture.PgDependSelect(q, [][]any{fixture.PgDependEdge(2604, fresh, 0, 1259, table, 1, "a")}, fresh))
	})
}
func TestReferencePGDependTransactions(t *testing.T) {
	p := fixture.NewScenario(t)
	fixture.PgDependBackends(t, p, func(t *testing.T, b fixture.Backend) {
		fixture.PgDependExec(t, p, b, "CREATE TABLE pgdep_tx(a INT,b INT)", "CREATE SEQUENCE pgdep_tx_seq OWNED BY pgdep_tx.a")
		table := fixture.PgDependRelation(t, b, "pgdep_tx")
		seq := fixture.PgDependRelation(t, b, "pgdep_tx_seq")
		ns := fixture.PgDependNamespace(t, b, seq)
		_, owner, peer := fixture.IsolationReferenceConnections(t, p, b.PG)
		defer owner.Close()
		defer peer.Close()
		tx, err := owner.BeginTx(p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		exec := func(q string) { t.Helper(); fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec(q, 0)) }
		bag := func(x fixture.Executor, sub int64) {
			t.Helper()
			want := [][]any{fixture.PgDependEdge(1259, seq, 0, 2615, ns, 0, "n")}
			if sub != 0 {
				want = append(want, fixture.PgDependEdge(1259, seq, 0, 1259, table, sub, "a"))
			}
			fixture.PgDescriptionExecutorRows(t, p, b, x, fixture.PgDependSelect("SELECT * FROM pg_catalog.pg_depend WHERE classid=1259 AND objid=$1", want, seq))
		}
		exec("SAVEPOINT before_owner")
		exec("ALTER SEQUENCE pgdep_tx_seq OWNED BY pgdep_tx.b")
		bag(tx, 2)
		bag(peer, 1)
		exec("ROLLBACK TO before_owner")
		bag(tx, 1)
		canceled, cancel := context.WithCancel(p.Ctx)
		cancel()
		if _, err := tx.ExecContext(canceled, "ALTER SEQUENCE pgdep_tx_seq OWNED BY NONE"); err == nil {
			t.Fatal("canceled DDL succeeded")
		}
		bag(tx, 1)
		exec("ALTER SEQUENCE pgdep_tx_seq OWNED BY NONE")
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		bag(peer, 1)
		tx, err = owner.BeginTx(p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		exec("ALTER SEQUENCE pgdep_tx_seq OWNED BY NONE")
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		bag(peer, 0)
		fixture.RunStep(t, p.Ctx, owner, b.Recorder, fixture.Exec("CREATE TEMP SEQUENCE pgdep_temp_seq", 0))
		var tempSeq, tempNS int64
		if err := owner.QueryRowContext(p.Ctx, "SELECT oid FROM pg_catalog.pg_class WHERE relname='pgdep_temp_seq'").Scan(&tempSeq); err != nil {
			t.Fatal(err)
		}
		if err := owner.QueryRowContext(p.Ctx, "SELECT relnamespace FROM pg_catalog.pg_class WHERE oid=$1", tempSeq).Scan(&tempNS); err != nil {
			t.Fatal(err)
		}
		fixture.PgDescriptionExecutorRows(t, p, b, owner, fixture.PgDependSelect("SELECT * FROM pg_catalog.pg_depend WHERE classid=1259 AND objid=$1", [][]any{fixture.PgDependEdge(1259, tempSeq, 0, 2615, tempNS, 0, "n")}, tempSeq))
		fixture.RunStep(t, p.Ctx, owner, b.Recorder, fixture.Exec("CREATE TABLE pg_temp.pgdep_temp(a INT)", 0))
		var tempTable int64
		if err := owner.QueryRowContext(p.Ctx, "SELECT oid FROM pg_catalog.pg_class WHERE relname='pgdep_temp'").Scan(&tempTable); err != nil {
			t.Fatal(err)
		}
		fixture.RunStep(t, p.Ctx, owner, b.Recorder, fixture.Exec("ALTER SEQUENCE pgdep_temp_seq OWNED BY pgdep_temp.a", 0))
		fixture.PgDescriptionExecutorRows(t, p, b, owner, fixture.PgDependSelect("SELECT * FROM pg_catalog.pg_depend WHERE classid=1259 AND objid=$1", [][]any{fixture.PgDependEdge(1259, tempSeq, 0, 2615, tempNS, 0, "n"), fixture.PgDependEdge(1259, tempSeq, 0, 1259, tempTable, 1, "a")}, tempSeq))
		fixture.RunStep(t, p.Ctx, owner, b.Recorder, fixture.Exec("DROP TABLE pgdep_temp", 0))
		fixture.PgDescriptionExecutorRows(t, p, b, owner, fixture.PgDependSelect("SELECT * FROM pg_catalog.pg_depend WHERE classid=1259 AND objid=$1", [][]any{}, tempSeq))
	})
}
