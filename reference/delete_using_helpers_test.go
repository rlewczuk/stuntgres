package reference_test

import (
	"database/sql"
	"testing"
)

func deleteUsingReference(t *testing.T, body func(*testing.T, *referencePair, referenceBackend, *sql.Conn)) {
	t.Helper()
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			conn, err := backend.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := conn.Close(); err != nil {
					t.Error(err)
				}
			})
			body(t, p, backend, conn)
		})
	}
}

func deleteUsingSeed(run func(referenceStep)) {
	run(referenceExec("CREATE TABLE t(id INT,v INT)", 0))
	run(referenceExec("CREATE TABLE s(k INT,label TEXT)", 0))
	run(referenceExec("INSERT INTO t VALUES(1,10),(2,20),(2,20),(3,30),(4,40),(NULL,50)", 6))
	run(referenceExec("INSERT INTO s VALUES(2,'two'),(2,'two'),(3,'three'),(9,'nine'),(NULL,'null')", 5))
}

func deleteUsingTargets(rows [][]any) referenceStep {
	s := referenceSelect("SELECT id,v FROM t", []string{"id", "v"}, []string{"INT4", "INT4"}, rows)
	s.bag = true
	return s
}

func deleteUsingOriginal() [][]any {
	return [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}, {int64(2), int64(20)}, {int64(3), int64(30)}, {int64(4), int64(40)}, {nil, int64(50)}}
}

func deleteUsingSurvivors() [][]any {
	return [][]any{{int64(1), int64(10)}, {int64(4), int64(40)}, {nil, int64(50)}}
}

func deleteUsingSourcesUnchanged(run func(referenceStep)) {
	s := referenceSelect("SELECT k,label FROM s", []string{"k", "label"}, []string{"INT4", "TEXT"}, [][]any{{int64(2), "two"}, {int64(2), "two"}, {int64(3), "three"}, {int64(9), "nine"}, {nil, "null"}})
	s.bag = true
	run(s)
}
