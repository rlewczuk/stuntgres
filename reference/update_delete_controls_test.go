package reference_test

import (
	"bytes"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceUpdateCoreDeleteControls(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			db := backend.db
			ctx := p.ctx
			exec := func(q string, prepared bool, n int64) { updateCoreExec(t, ctx, db, q, prepared, n) }
			rows := func(want [][]any) {
				s := referenceSelect("SELECT id,v FROM t", []string{"id", "v"}, []string{"INT4", "INT4"}, want)
				s.bag = true
				referenceRunStep(t, ctx, db, backend.recorder, s)
			}
			exec("CREATE TABLE t(id INT,v INT)", false, 0)
			exec("INSERT INTO t VALUES(1,10),(2,20),(2,20)", false, 3)
			retained, err := db.QueryContext(ctx, "SELECT id,v FROM t")
			if err != nil {
				t.Fatal(err)
			}
			defer retained.Close()
			stmt, err := db.PrepareContext(ctx, "UPDATE t SET v=$1 WHERE id=$2")
			if err != nil {
				t.Fatal(err)
			}
			defer func() { retained.Close(); stmt.Close() }()
			tx, err := db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			updateCoreExec(t, ctx, tx, "UPDATE t SET v=30 WHERE id=1", false, 1)
			updateCoreExec(t, ctx, tx, "DELETE FROM t WHERE v=20", true, 2)
			updateCoreExec(t, ctx, tx, "INSERT INTO t VALUES(3,30)", false, 1)
			updateCoreExec(t, ctx, tx, "UPDATE t SET v=40 WHERE id=3", false, 1)
			updateCoreExec(t, ctx, tx, "DELETE FROM t WHERE id=3", true, 1)
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			live := [][]any{{int64(1), int64(30)}}
			rows(live)
			if !backend.pg {
				var dump bytes.Buffer
				if err := stuntgres.Dump(ctx, p.native, &dump); err != nil {
					t.Fatal(err)
				}
				for _, piece := range []string{"DELETE", "UPDATE", "(2, 20)", "(3,"} {
					if strings.Contains(dump.String(), piece) {
						t.Fatal("dump contains history", dump.String())
					}
				}
				if err := stuntgres.Restore(ctx, p.native, strings.NewReader("CREATE TABLE broken(v INT);DELETE FROM broken;")); err == nil {
					t.Fatal("Restore admitted DML history")
				}
				rows(live)
				if err := stuntgres.Reset(ctx, p.native); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
				rows(live)
				var again bytes.Buffer
				if err := stuntgres.Dump(ctx, p.native, &again); err != nil || again.String() != dump.String() {
					t.Fatal("live-only canonical roundtrip", again.String(), err)
				}
			}
			updateCoreRetained(t, backend.pg, retained, []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}, {int64(2), int64(20)}}, true)
			result, err := stmt.ExecContext(ctx, 31, 1)
			if err != nil {
				t.Fatal(err)
			}
			referenceLockAffected(t, result, 1)
			rows([][]any{{int64(1), int64(31)}})
		})
	}
}
