package reference_test

import (
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePartialIndexUniqueFeatureSmoke(t *testing.T) {
	partialIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TABLE u(id INT,k INT,active BOOL)", 0))
		run(fixture.Exec("INSERT INTO u VALUES(1,7,FALSE),(2,7,NULL)", 2))
		run(fixture.Prepared(fixture.Exec("CREATE UNIQUE INDEX pu ON u(k) WHERE active", 0)))
		run(fixture.Exec("INSERT INTO u VALUES(3,7,TRUE)", 1))
		run(fixture.Prepared(fixture.Exec("INSERT INTO u VALUES(4,7,FALSE)", 1)))
		dup := fixture.UniqueReferenceDuplicate("u", "pu", "Key (k)=(7) already exists.")
		run(fixture.Prepared(fixture.ErrorDiagnostic("INSERT INTO u VALUES(5,7,TRUE)", dup)))
		run(fixture.Select("SELECT id,k,active FROM u ORDER BY id", []string{"id", "k", "active"}, []string{"INT4", "INT4", "BOOL"}, [][]any{{int64(1), int64(7), false}, {int64(2), int64(7), nil}, {int64(3), int64(7), true}, {int64(4), int64(7), false}}))
		run(fixture.Exec("UPDATE u SET active=FALSE WHERE id=3", 1))
		run(fixture.Prepared(fixture.Exec("UPDATE u SET active=TRUE WHERE id=2", 1)))
		run(fixture.ErrorDiagnostic("INSERT INTO u VALUES(5,8,TRUE),(6,8,FALSE),(7,8,TRUE)", fixture.UniqueReferenceDuplicate("u", "pu", "Key (k)=(8) already exists.")))
		run(fixture.PartialIndexIDs("SELECT id FROM u ORDER BY id", 1, 2, 3, 4))
		q := "SELECT id FROM u WHERE active=$1 ORDER BY id"
		stmt, err := b.DB.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, tc := range []struct {
			arg any
			ids []int64
		}{{false, []int64{1, 3, 4}}, {true, []int64{2}}, {nil, nil}, {false, []int64{1, 3, 4}}} {
			s := fixture.PartialIndexIDs(q, tc.ids...)
			s.Args = []any{tc.arg}
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}, b.Recorder, s)
		}
		if err := stmt.Close(); err != nil {
			t.Fatal(err)
		}
		run(fixture.Exec("CREATE TABLE n(id INT,k INT,active BOOL)", 0))
		run(fixture.Exec("CREATE UNIQUE INDEX pn ON n(k) NULLS NOT DISTINCT WHERE active", 0))
		run(fixture.Exec("INSERT INTO n VALUES(1,NULL,FALSE),(2,NULL,NULL),(3,NULL,TRUE)", 3))
		run(fixture.ErrorDiagnostic("INSERT INTO n VALUES(4,NULL,TRUE)", fixture.UniqueReferenceDuplicate("n", "pn", "Key (k)=(null) already exists.")))
		run(fixture.PartialIndexIDs("SELECT id FROM n ORDER BY id", 1, 2, 3))
		run(fixture.Exec("ALTER TABLE u RENAME active TO enabled", 0))
		run(fixture.DeparseRelationsTextStep(t, b, "SELECT pg_get_indexdef('pu'::regclass) AS definition", "CREATE UNIQUE INDEX pu ON public.u USING btree (k) WHERE enabled"))
		namespace := fixture.UniqueReferencePublicNamespace(t, p.Ctx, b.DB, b.PG)
		run(fixture.Select("SELECT c.conname FROM pg_constraint c JOIN pg_class t ON c.conrelid=t.oid WHERE t.relname='u' AND t.relnamespace=$1", []string{"conname"}, []string{"NAME"}, nil, namespace))
		run(fixture.Exec("REINDEX INDEX pu", 0))
		if !b.PG {
			stream := partialIndexDump(t, p)
			if strings.Count(stream, "CREATE UNIQUE INDEX") != 2 {
				t.Fatal("Dump lost unique definitions", stream)
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(stream)); err != nil {
				t.Fatal(err)
			}
			if partialIndexDump(t, p) != stream {
				t.Fatal("Restore changed Dump")
			}
		}
		run(fixture.Prepared(fixture.ErrorDiagnostic("INSERT INTO u VALUES(5,7,TRUE)", dup)))
		run(fixture.PartialIndexIDs("SELECT id FROM u WHERE enabled ORDER BY id", 2))
		run(fixture.PartialIndexIDs("SELECT id FROM u ORDER BY id", 1, 2, 3, 4))
	})
}
