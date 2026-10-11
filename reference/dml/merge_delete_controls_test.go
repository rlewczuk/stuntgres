package reference_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceMergeDeleteControls(t *testing.T) {
	mergeInsertScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := fixture.MergeRun(t, p, b, prepared)
		for _, q := range []string{"CREATE TABLE source(k INT)", "CREATE VIEW source_view AS SELECT * FROM source", "CREATE TABLE t(id INT GENERATED ALWAYS AS IDENTITY CONSTRAINT t_key UNIQUE,k INT,v INT DEFAULT 8,g INT GENERATED ALWAYS AS(v+1) STORED)"} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("INSERT INTO t(k,v) VALUES(1,10),(2,20)", 2))
		run(fixture.Exec("INSERT INTO source VALUES(1)", 1))
		retained, err := b.DB.QueryContext(p.Ctx, "SELECT * FROM t ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		q := "MERGE INTO t USING source_view s ON t.k=s.k WHEN MATCHED THEN DELETE"
		held, err := b.DB.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		r, err := held.ExecContext(p.Ctx)
		mergeInsertAffected(t, b, r, err, 1)
		read := fixture.Select("SELECT * FROM t ORDER BY id", []string{"id", "k", "v", "g"}, []string{"INT4", "INT4", "INT4", "INT4"}, [][]any{{int64(2), int64(2), int64(20), int64(21)}})
		run(read)
		if !b.PG {
			var dump bytes.Buffer
			if err = stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if err = stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err = stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			run(read)
			fixture.InsertSelectRun(t, p.Ctx, p.Memory, p.MemoryRecord, read)
			clone := fixture.CatalogControlPool(t, stuntgres.NewDriver(), t.Name()+"/clone")
			if err = stuntgres.Restore(p.Ctx, clone, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			fixture.CatalogControlTuples(t, clone, read.Query, read.Rows)
			var redump bytes.Buffer
			if err = stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil || !bytes.Equal(dump.Bytes(), redump.Bytes()) {
				t.Fatal("canonical survivor redump", err)
			}
			err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE TABLE broken(id INT NOT NULL); INSERT INTO broken DEFAULT VALUES;"))
			d := fixture.ControlsReferenceDiagnostic("23502", `null value in column "id" of relation "broken" violates not-null constraint`)
			d.Detail = "Failing row contains (null)."
			d.Schema = "public"
			d.Table = "broken"
			d.Column = "id"
			fixture.AssertDiagnostic(t, err, &d)
			ctx, cancel := context.WithCancel(p.Ctx)
			err = stuntgres.Restore(ctx, p.Native, &fixture.CatalogControlCancelReader{Cancel: cancel})
			cancel()
			if !errors.Is(err, context.Canceled) {
				t.Fatal("cancelled restore", err)
			}
			var unchanged bytes.Buffer
			if err = stuntgres.Dump(p.Ctx, p.Native, &unchanged); err != nil || !bytes.Equal(dump.Bytes(), unchanged.Bytes()) {
				t.Fatal("failed restore published", err)
			}
			if p.Postgres != nil {
				if _, err = p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
					t.Fatalf("actual emitted script replay: %v\n%s", err, dump.String())
				}
				fixture.InsertSelectRun(t, p.Ctx, p.Postgres, p.PostgresRecord, read)
				fixture.InsertSelectRun(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Exec("INSERT INTO t(k) VALUES(3)", 1))
				fixture.InsertSelectRun(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Select("SELECT id,v,g FROM t WHERE k=3", []string{"id", "v", "g"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(3), int64(8), int64(9)}}))
				if _, err = p.Postgres.ExecContext(p.Ctx, "DROP VIEW source_view; DROP TABLE source,t"); err != nil {
					t.Fatal(err)
				}
			}
		}
		names, err := retained.Columns()
		if err != nil || len(names) != 4 || names[3] != "g" {
			t.Fatal("retained schema", names, err)
		}
		for _, want := range [][]int64{{1, 1, 10, 11}, {2, 2, 20, 21}} {
			if !retained.Next() {
				t.Fatal("retained row lost", retained.Err())
			}
			var id, k, v, g int64
			if err = retained.Scan(&id, &k, &v, &g); err != nil || id != want[0] || k != want[1] || v != want[2] || g != want[3] {
				t.Fatal("retained payload", id, k, v, g, err)
			}
		}
		if retained.Next() || retained.Err() != nil {
			t.Fatal("retained changed", retained.Err())
		}
		retained.Close()
		r, err = held.ExecContext(p.Ctx)
		mergeInsertAffected(t, b, r, err, 0)
		run(fixture.Exec("INSERT INTO source VALUES(2)", 1))
		r, err = held.ExecContext(p.Ctx)
		mergeInsertAffected(t, b, r, err, 1)
		run(fixture.Exec("INSERT INTO t(k) VALUES(3)", 1))
		run(fixture.Select("SELECT * FROM t", []string{"id", "k", "v", "g"}, []string{"INT4", "INT4", "INT4", "INT4"}, [][]any{{int64(3), int64(3), int64(8), int64(9)}}))
	})
}

func TestReferenceMergeDeleteFeatureSmoke(t *testing.T) {
	mergeInsertScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := fixture.MergeRun(t, p, b, prepared)
		run(fixture.Exec("CREATE TABLE t(id INT CONSTRAINT t_key UNIQUE,v INT)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1,10),(2,20),(3,30)", 3))
		run(fixture.Exec("MERGE INTO t USING (VALUES(1,0),(2,1),(4,1)) s(id,flag) ON t.id=s.id WHEN MATCHED AND s.flag=0 THEN DELETE WHEN MATCHED THEN UPDATE SET v=t.v+1 WHEN NOT MATCHED BY SOURCE THEN DELETE WHEN NOT MATCHED BY TARGET THEN INSERT VALUES(s.id,40)", 4))
		run(fixture.Select("SELECT * FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(21)}, {int64(4), int64(40)}}))
		held, err := b.DB.PrepareContext(p.Ctx, "MERGE INTO t USING (VALUES(2)) s(id) ON t.id=s.id WHEN MATCHED AND $1::boolean THEN DELETE")
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		for _, v := range []any{false, nil} {
			r, e := held.ExecContext(p.Ctx, v)
			mergeInsertAffected(t, b, r, e, 0)
		}
		r, err := held.ExecContext(p.Ctx, true)
		mergeInsertAffected(t, b, r, err, 1)
		run(fixture.Exec("INSERT INTO t VALUES(5,50)", 1))
		s := fixture.ImplicitError("MERGE INTO t USING (VALUES(5),(5)) s(id) ON t.id=s.id WHEN MATCHED THEN DELETE", mergeUpdateCardinalityDiagnostic())
		s.ErrorStage = "execute"
		run(s)
		read := fixture.Select("SELECT * FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(4), int64(40)}, {int64(5), int64(50)}})
		run(read)
		command := fixture.Select("MERGE INTO t USING (SELECT 1 AS id WHERE FALSE) s ON t.id=s.id WHEN MATCHED THEN DELETE", []string{}, []string{}, [][]any{})
		command.Prepared = prepared
		run(command)
		if !b.PG {
			var dump bytes.Buffer
			if err = stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if err = stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err = stuntgres.Restore(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			run(read)
		}
	})
}
