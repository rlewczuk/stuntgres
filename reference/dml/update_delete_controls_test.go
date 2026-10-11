package reference_test

import (
	"bytes"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceUpdateCoreDeleteControls(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			db := backend.DB
			ctx := p.Ctx
			exec := func(q string, prepared bool, n int64) { fixture.UpdateCoreExec(t, ctx, db, q, prepared, n) }
			rows := func(want [][]any) {
				s := fixture.Select("SELECT id,v FROM t", []string{"id", "v"}, []string{"INT4", "INT4"}, want)
				s.Bag = true
				fixture.RunStep(t, ctx, db, backend.Recorder, s)
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
			fixture.UpdateCoreExec(t, ctx, tx, "UPDATE t SET v=30 WHERE id=1", false, 1)
			fixture.UpdateCoreExec(t, ctx, tx, "DELETE FROM t WHERE v=20", true, 2)
			fixture.UpdateCoreExec(t, ctx, tx, "INSERT INTO t VALUES(3,30)", false, 1)
			fixture.UpdateCoreExec(t, ctx, tx, "UPDATE t SET v=40 WHERE id=3", false, 1)
			fixture.UpdateCoreExec(t, ctx, tx, "DELETE FROM t WHERE id=3", true, 1)
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			live := [][]any{{int64(1), int64(30)}}
			rows(live)
			if !backend.PG {
				var dump bytes.Buffer
				if err := stuntgres.Dump(ctx, p.Native, &dump); err != nil {
					t.Fatal(err)
				}
				for _, piece := range []string{"DELETE", "UPDATE", "(2, 20)", "(3,"} {
					if strings.Contains(dump.String(), piece) {
						t.Fatal("dump contains history", dump.String())
					}
				}
				if err := stuntgres.Restore(ctx, p.Native, strings.NewReader("CREATE TABLE broken(v INT);DELETE FROM broken;")); err == nil {
					t.Fatal("Restore admitted DML history")
				}
				rows(live)
				if err := stuntgres.Reset(ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
				rows(live)
				var again bytes.Buffer
				if err := stuntgres.Dump(ctx, p.Native, &again); err != nil || again.String() != dump.String() {
					t.Fatal("live-only canonical roundtrip", again.String(), err)
				}
			}
			fixture.UpdateCoreRetained(t, backend.PG, retained, []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}, {int64(2), int64(20)}}, true)
			result, err := stmt.ExecContext(ctx, 31, 1)
			if err != nil {
				t.Fatal(err)
			}
			fixture.LockAffected(t, result, 1)
			rows([][]any{{int64(1), int64(31)}})
		})
	}
}
