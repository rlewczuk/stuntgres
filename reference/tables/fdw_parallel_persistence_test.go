package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func fdwParallelRestoreTargets(t *testing.T, p *fixture.Pair, r *fdwParallelReader, dump []byte) {
	t.Helper()
	for _, registered := range []bool{false, true} {
		d := stuntgres.NewDriver()
		if registered {
			if err := r.configure(d); err != nil {
				t.Fatal(err)
			}
		}
		connector, err := d.OpenConnector("fdw-parallel-restore")
		if err != nil {
			t.Fatal(err)
		}
		db := sql.OpenDB(connector)
		defer db.Close()
		pool := fdwRecordedPool(t, connector)
		defer pool.Close()
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, pool, p.MemoryRecord, s) }
		run(fixture.Exec("CREATE TABLE sentinel(id integer)", 0))
		run(fixture.Exec("INSERT INTO sentinel VALUES(99)", 1))
		before := r.parallelCounts()
		err = stuntgres.Restore(p.Ctx, db, bytes.NewReader(dump))
		if registered {
			if err != nil {
				t.Fatal(err)
			}
			if r.parallelCounts() != before {
				t.Fatal("Restore started coordinator")
			}
			run(fdwParallelIDs("SELECT id FROM fdw_data WHERE k=2 ORDER BY id", 1, 4, 6))
			// An invalid later statement must not replace the already usable graph.
			bad := append(append([]byte{}, dump...), []byte("\nCREATE TABLE broken(x missing_parallel_type);\n")...)
			if err := stuntgres.Restore(p.Ctx, db, bytes.NewReader(bad)); err == nil {
				t.Fatal("invalid Restore succeeded")
			}
			run(fdwParallelIDs("SELECT id FROM fdw_data WHERE k=2 ORDER BY id", 1, 4, 6))
		} else {
			if err == nil {
				t.Fatal("Restore succeeded without provider registration")
			}
			run(fixture.Select("SELECT id FROM sentinel", []string{"id"}, []string{"INT4"}, [][]any{{int64(99)}}))
		}
	}
}
