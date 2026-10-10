package reference_test

import (
	"context"
	"testing"
)

func TestReferencePGDependSnapshots(t *testing.T) {
	p := newReferenceScenario(t)
	pgDependBackends(t, p, func(t *testing.T, b referenceBackend) {
		pgDependExec(t, p, b, "CREATE TABLE pgdep_snapshot(a INT DEFAULT 1)")
		table := pgDependRelation(t, b, "pgdep_snapshot")
		d := pgDependDefinition(t, b, table, 1)
		q := "SELECT * FROM pg_catalog.pg_depend WHERE classid=2604 AND objid=$1"
		stmt, err := b.db.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		old, err := stmt.QueryContext(p.ctx, d)
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		types, err := old.ColumnTypes()
		if err != nil || len(types) != 7 {
			t.Fatal(types, err)
		}
		pgDependExec(t, p, b, "ALTER TABLE pgdep_snapshot ALTER a DROP DEFAULT")
		pgDescriptionExecutorRows(t, p, b, referenceHeldStatement{referenceExecutor: b.db, statement: stmt, query: q}, pgDependSelect(q, [][]any{}, d))
		pgDependRetained(t, old, [][]any{pgDependEdge(2604, d, 0, 1259, table, 1, "a")})
		if types[0].DatabaseTypeName() != "OID" {
			t.Fatal("retained descriptor")
		}
		pgDependExec(t, p, b, "ALTER TABLE pgdep_snapshot ALTER a SET DEFAULT 2")
		fresh := pgDependDefinition(t, b, table, 1)
		if fresh == d {
			t.Fatal("fresh default reused identity")
		}
		pgDescriptionExecutorRows(t, p, b, referenceHeldStatement{referenceExecutor: b.db, statement: stmt, query: q}, pgDependSelect(q, [][]any{pgDependEdge(2604, fresh, 0, 1259, table, 1, "a")}, fresh))
	})
}
func TestReferencePGDependTransactions(t *testing.T) {
	p := newReferenceScenario(t)
	pgDependBackends(t, p, func(t *testing.T, b referenceBackend) {
		pgDependExec(t, p, b, "CREATE TABLE pgdep_tx(a INT,b INT)", "CREATE SEQUENCE pgdep_tx_seq OWNED BY pgdep_tx.a")
		table := pgDependRelation(t, b, "pgdep_tx")
		seq := pgDependRelation(t, b, "pgdep_tx_seq")
		ns := pgDependNamespace(t, b, seq)
		_, owner, peer := isolationReferenceConnections(t, p, b.pg)
		defer owner.Close()
		defer peer.Close()
		tx, err := owner.BeginTx(p.ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		exec := func(q string) { t.Helper(); referenceRunStep(t, p.ctx, tx, b.recorder, referenceExec(q, 0)) }
		bag := func(x referenceExecutor, sub int64) {
			t.Helper()
			want := [][]any{pgDependEdge(1259, seq, 0, 2615, ns, 0, "n")}
			if sub != 0 {
				want = append(want, pgDependEdge(1259, seq, 0, 1259, table, sub, "a"))
			}
			pgDescriptionExecutorRows(t, p, b, x, pgDependSelect("SELECT * FROM pg_catalog.pg_depend WHERE classid=1259 AND objid=$1", want, seq))
		}
		exec("SAVEPOINT before_owner")
		exec("ALTER SEQUENCE pgdep_tx_seq OWNED BY pgdep_tx.b")
		bag(tx, 2)
		bag(peer, 1)
		exec("ROLLBACK TO before_owner")
		bag(tx, 1)
		canceled, cancel := context.WithCancel(p.ctx)
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
		tx, err = owner.BeginTx(p.ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		exec("ALTER SEQUENCE pgdep_tx_seq OWNED BY NONE")
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		bag(peer, 0)
		referenceRunStep(t, p.ctx, owner, b.recorder, referenceExec("CREATE TEMP SEQUENCE pgdep_temp_seq", 0))
		var tempSeq, tempNS int64
		if err := owner.QueryRowContext(p.ctx, "SELECT oid FROM pg_catalog.pg_class WHERE relname='pgdep_temp_seq'").Scan(&tempSeq); err != nil {
			t.Fatal(err)
		}
		if err := owner.QueryRowContext(p.ctx, "SELECT relnamespace FROM pg_catalog.pg_class WHERE oid=$1", tempSeq).Scan(&tempNS); err != nil {
			t.Fatal(err)
		}
		pgDescriptionExecutorRows(t, p, b, owner, pgDependSelect("SELECT * FROM pg_catalog.pg_depend WHERE classid=1259 AND objid=$1", [][]any{pgDependEdge(1259, tempSeq, 0, 2615, tempNS, 0, "n")}, tempSeq))
		referenceRunStep(t, p.ctx, owner, b.recorder, referenceExec("CREATE TABLE pg_temp.pgdep_temp(a INT)", 0))
		var tempTable int64
		if err := owner.QueryRowContext(p.ctx, "SELECT oid FROM pg_catalog.pg_class WHERE relname='pgdep_temp'").Scan(&tempTable); err != nil {
			t.Fatal(err)
		}
		referenceRunStep(t, p.ctx, owner, b.recorder, referenceExec("ALTER SEQUENCE pgdep_temp_seq OWNED BY pgdep_temp.a", 0))
		pgDescriptionExecutorRows(t, p, b, owner, pgDependSelect("SELECT * FROM pg_catalog.pg_depend WHERE classid=1259 AND objid=$1", [][]any{pgDependEdge(1259, tempSeq, 0, 2615, tempNS, 0, "n"), pgDependEdge(1259, tempSeq, 0, 1259, tempTable, 1, "a")}, tempSeq))
		referenceRunStep(t, p.ctx, owner, b.recorder, referenceExec("DROP TABLE pgdep_temp", 0))
		pgDescriptionExecutorRows(t, p, b, owner, pgDependSelect("SELECT * FROM pg_catalog.pg_depend WHERE classid=1259 AND objid=$1", [][]any{}, tempSeq))
	})
}
