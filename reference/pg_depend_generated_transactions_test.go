package reference_test

import "testing"

func TestReferencePGDependTransactionsSequenceGeneratedCascade(t *testing.T) {
	p := newReferenceScenario(t)
	pgDependBackends(t, p, func(t *testing.T, b referenceBackend) {
		pgDependExec(t, p, b, "CREATE SEQUENCE pgdep_tx_provider", "CREATE TABLE pgdep_tx_generated(id INT,a INT,g BOOL GENERATED ALWAYS AS('pgdep_tx_provider'::regclass IS NOT NULL) STORED)")
		referenceRunStep(t, p.ctx, b.db, b.recorder, referenceExec("INSERT INTO pgdep_tx_generated(id,a) VALUES(1,10),(2,20)", 2))
		table := pgDependRelation(t, b, "pgdep_tx_generated")
		seq := pgDependRelation(t, b, "pgdep_tx_provider")
		generation := pgDependDefinition(t, b, table, 3)
		ns := pgDependNamespace(t, b, seq)
		_, owner, peer := isolationReferenceConnections(t, p, b.pg)
		defer owner.Close()
		defer peer.Close()
		old, err := b.db.QueryContext(p.ctx, "SELECT * FROM pg_catalog.pg_depend WHERE classid=2604 AND objid=$1", generation)
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		oldData, err := b.db.QueryContext(p.ctx, "SELECT id,a,g FROM pgdep_tx_generated ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		defer oldData.Close()
		tx, err := owner.BeginTx(p.ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		run := func(q string, count int64) {
			t.Helper()
			referenceRunStep(t, p.ctx, tx, b.recorder, referenceExec(q, count))
		}
		bag := func(x referenceExecutor, exists bool) {
			t.Helper()
			want := [][]any{}
			if exists {
				want = append(want, pgDependEdge(2604, generation, 0, 1259, table, 3, "i"), pgDependEdge(2604, generation, 0, 1259, seq, 0, "n"))
			}
			pgDescriptionExecutorRows(t, p, b, x, pgDependSelect("SELECT * FROM pg_catalog.pg_depend WHERE classid=2604 AND objid=$1", want, generation))
		}
		run("SAVEPOINT before_restrict", 0)
		referenceRunStep(t, p.ctx, tx, b.recorder, referenceErrorDiagnostic("DROP SEQUENCE pgdep_tx_provider", pgDependRestrict("sequence pgdep_tx_provider", "column g of table pgdep_tx_generated depends on sequence pgdep_tx_provider")))
		run("ROLLBACK TO before_restrict", 0)
		bag(tx, true)
		bag(peer, true)
		run("INSERT INTO pgdep_tx_generated(id,a) VALUES(3,30)", 1)
		run("DELETE FROM pgdep_tx_generated WHERE id=1", 1)
		referenceRunStep(t, p.ctx, peer, b.recorder, referenceSelect("SELECT id,a,g FROM pgdep_tx_generated ORDER BY id", []string{"id", "a", "g"}, []string{"INT4", "INT4", "BOOL"}, [][]any{{int64(1), int64(10), true}, {int64(2), int64(20), true}}))
		run("SAVEPOINT before_cascade", 0)
		run("DROP SEQUENCE pgdep_tx_provider CASCADE", 0)
		bag(tx, false)
		bag(peer, true)
		referenceRunStep(t, p.ctx, tx, b.recorder, referenceSelect("SELECT id,a FROM pgdep_tx_generated ORDER BY id", []string{"id", "a"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(20)}, {int64(3), int64(30)}}))
		run("ROLLBACK TO before_cascade", 0)
		bag(tx, true)
		referenceRunStep(t, p.ctx, tx, b.recorder, referenceSelect("SELECT id,a,g FROM pgdep_tx_generated ORDER BY id", []string{"id", "a", "g"}, []string{"INT4", "INT4", "BOOL"}, [][]any{{int64(2), int64(20), true}, {int64(3), int64(30), true}}))
		run("DROP SEQUENCE pgdep_tx_provider CASCADE", 0)
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		bag(peer, false)
		pgDependBag(t, p, b, 1259, seq)
		referenceRunStep(t, p.ctx, peer, b.recorder, referenceSelect("SELECT id,a FROM pgdep_tx_generated ORDER BY id", []string{"id", "a"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(20)}, {int64(3), int64(30)}}))
		pgDependRetained(t, old, [][]any{pgDependEdge(2604, generation, 0, 1259, table, 3, "i"), pgDependEdge(2604, generation, 0, 1259, seq, 0, "n")})
		var got [][]any
		for oldData.Next() {
			var id, a int64
			var g bool
			if err := oldData.Scan(&id, &a, &g); err != nil {
				t.Fatal(err)
			}
			got = append(got, []any{id, a, g})
		}
		if err := oldData.Err(); err != nil {
			t.Fatal(err)
		}
		if _, ok := referenceCompareRows(got, [][]any{{int64(1), int64(10), true}, {int64(2), int64(20), true}}, false); !ok {
			t.Fatal("old data snapshot", got)
		}
		if ns == 0 {
			t.Fatal("missing real sequence namespace")
		}
	})
}
