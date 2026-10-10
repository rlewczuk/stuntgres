package reference_test

import (
	"strings"
	"stuntgres"
	"testing"
)

func TestReferencePartialIndexUniqueFeatureSmoke(t *testing.T) {
	partialIndexScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, run func(referenceStep)) {
		run(referenceExec("CREATE TABLE u(id INT,k INT,active BOOL)", 0))
		run(referenceExec("INSERT INTO u VALUES(1,7,FALSE),(2,7,NULL)", 2))
		run(referencePrepared(referenceExec("CREATE UNIQUE INDEX pu ON u(k) WHERE active", 0)))
		run(referenceExec("INSERT INTO u VALUES(3,7,TRUE)", 1))
		run(referencePrepared(referenceExec("INSERT INTO u VALUES(4,7,FALSE)", 1)))
		dup := uniqueReferenceDuplicate("u", "pu", "Key (k)=(7) already exists.")
		run(referencePrepared(referenceErrorDiagnostic("INSERT INTO u VALUES(5,7,TRUE)", dup)))
		run(referenceSelect("SELECT id,k,active FROM u ORDER BY id", []string{"id", "k", "active"}, []string{"INT4", "INT4", "BOOL"}, [][]any{{int64(1), int64(7), false}, {int64(2), int64(7), nil}, {int64(3), int64(7), true}, {int64(4), int64(7), false}}))
		run(referenceExec("UPDATE u SET active=FALSE WHERE id=3", 1))
		run(referencePrepared(referenceExec("UPDATE u SET active=TRUE WHERE id=2", 1)))
		run(referenceErrorDiagnostic("INSERT INTO u VALUES(5,8,TRUE),(6,8,FALSE),(7,8,TRUE)", uniqueReferenceDuplicate("u", "pu", "Key (k)=(8) already exists.")))
		run(partialIndexIDs("SELECT id FROM u ORDER BY id", 1, 2, 3, 4))
		q := "SELECT id FROM u WHERE active=$1 ORDER BY id"
		stmt, err := b.db.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, tc := range []struct {
			arg any
			ids []int64
		}{{false, []int64{1, 3, 4}}, {true, []int64{2}}, {nil, nil}, {false, []int64{1, 3, 4}}} {
			s := partialIndexIDs(q, tc.ids...)
			s.args = []any{tc.arg}
			referenceRunStep(t, p.ctx, referenceHeldStatement{referenceExecutor: b.db, statement: stmt, query: q}, b.recorder, s)
		}
		if err := stmt.Close(); err != nil {
			t.Fatal(err)
		}
		run(referenceExec("CREATE TABLE n(id INT,k INT,active BOOL)", 0))
		run(referenceExec("CREATE UNIQUE INDEX pn ON n(k) NULLS NOT DISTINCT WHERE active", 0))
		run(referenceExec("INSERT INTO n VALUES(1,NULL,FALSE),(2,NULL,NULL),(3,NULL,TRUE)", 3))
		run(referenceErrorDiagnostic("INSERT INTO n VALUES(4,NULL,TRUE)", uniqueReferenceDuplicate("n", "pn", "Key (k)=(null) already exists.")))
		run(partialIndexIDs("SELECT id FROM n ORDER BY id", 1, 2, 3))
		run(referenceExec("ALTER TABLE u RENAME active TO enabled", 0))
		run(deparseRelationsTextStep(t, b, "SELECT pg_get_indexdef('pu'::regclass) AS definition", "CREATE UNIQUE INDEX pu ON public.u USING btree (k) WHERE enabled"))
		namespace := uniqueReferencePublicNamespace(t, p.ctx, b.db, b.pg)
		run(referenceSelect("SELECT c.conname FROM pg_constraint c JOIN pg_class t ON c.conrelid=t.oid WHERE t.relname='u' AND t.relnamespace=$1", []string{"conname"}, []string{"NAME"}, nil, namespace))
		run(referenceExec("REINDEX INDEX pu", 0))
		if !b.pg {
			stream := partialIndexDump(t, p)
			if strings.Count(stream, "CREATE UNIQUE INDEX") != 2 {
				t.Fatal("Dump lost unique definitions", stream)
			}
			if err := stuntgres.Reset(p.ctx, p.native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(stream)); err != nil {
				t.Fatal(err)
			}
			if partialIndexDump(t, p) != stream {
				t.Fatal("Restore changed Dump")
			}
		}
		run(referencePrepared(referenceErrorDiagnostic("INSERT INTO u VALUES(5,7,TRUE)", dup)))
		run(partialIndexIDs("SELECT id FROM u WHERE enabled ORDER BY id", 2))
		run(partialIndexIDs("SELECT id FROM u ORDER BY id", 1, 2, 3, 4))
	})
}
