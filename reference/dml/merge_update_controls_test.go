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

func TestReferenceMergeUpdateControls(t *testing.T) {
	mergeInsertScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := fixture.MergeRun(t, p, b, prepared)
		for _, q := range []string{"CREATE TABLE source(k INT,v INT)", "CREATE VIEW source_view AS SELECT * FROM source", "CREATE TABLE t(id INT GENERATED ALWAYS AS IDENTITY CONSTRAINT t_key UNIQUE,k INT,v INT DEFAULT 8,g INT GENERATED ALWAYS AS(v+1) STORED)"} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("INSERT INTO t(k,v) VALUES(1,10)", 1))
		run(fixture.Exec("INSERT INTO source VALUES(1,7),(2,70)", 2))
		retained, err := b.DB.QueryContext(p.Ctx, "SELECT * FROM t ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		q := "MERGE INTO t USING source_view s ON t.k=s.k WHEN MATCHED THEN UPDATE SET v=t.v+s.v WHEN NOT MATCHED THEN INSERT(k) VALUES(s.k)"
		held, err := b.DB.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		r, err := held.ExecContext(p.Ctx)
		mergeInsertAffected(t, b, r, err, 2)
		read := fixture.Select("SELECT * FROM t ORDER BY id", []string{"id", "k", "v", "g"}, []string{"INT4", "INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(1), int64(17), int64(18)}, {int64(2), int64(2), int64(8), int64(9)}})
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
			var redump bytes.Buffer
			if err = stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil || !bytes.Equal(dump.Bytes(), redump.Bytes()) {
				t.Fatal("canonical live-head redump", err)
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
				t.Fatal("canceled restore", err)
			}
			var unchanged bytes.Buffer
			if err = stuntgres.Dump(p.Ctx, p.Native, &unchanged); err != nil || !bytes.Equal(dump.Bytes(), unchanged.Bytes()) {
				t.Fatal("failed restore changed live heads", err)
			}
			if p.Postgres != nil {
				if _, err = p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
					t.Fatalf("actual emitted script replay: %v\n%s", err, dump.String())
				}
				fixture.InsertSelectRun(t, p.Ctx, p.Postgres, p.PostgresRecord, read)
				if _, err = p.Postgres.ExecContext(p.Ctx, "INSERT INTO t(k) VALUES(3)"); err != nil {
					t.Fatal(err)
				}
				fixture.InsertSelectRun(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Select("SELECT id,v,g FROM t WHERE k=3", []string{"id", "v", "g"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(3), int64(8), int64(9)}}))
				if _, err = p.Postgres.ExecContext(p.Ctx, "DROP VIEW source_view; DROP TABLE source,t"); err != nil {
					t.Fatal(err)
				}
			}
		}
		names, err := retained.Columns()
		if err != nil || len(names) != 4 || names[2] != "v" {
			t.Fatal("retained metadata", names, err)
		}
		if !retained.Next() {
			t.Fatal("retained old row lost", retained.Err())
		}
		var id, k, v, g int64
		if err = retained.Scan(&id, &k, &v, &g); err != nil || id != 1 || k != 1 || v != 10 || g != 11 {
			t.Fatal("retained old payload", id, k, v, g, err)
		}
		if retained.Next() || retained.Err() != nil {
			t.Fatal("retained saw replacements", retained.Err())
		}
		r, err = held.ExecContext(p.Ctx)
		mergeInsertAffected(t, b, r, err, 2)
		read.Rows = [][]any{{int64(1), int64(1), int64(24), int64(25)}, {int64(2), int64(2), int64(78), int64(79)}}
		run(read)
	})
}

func TestReferenceMergeUpdateFeatureSmoke(t *testing.T) {
	mergeInsertScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := fixture.MergeRun(t, p, b, prepared)
		run(fixture.Exec("CREATE TABLE t(id INT NOT NULL CONSTRAINT t_key UNIQUE,a INT,b INT,note TEXT)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1,10,20,'old')", 1))
		q := "MERGE INTO t USING (VALUES(1,1),(2,2)) s(id,d) ON t.id=s.id WHEN MATCHED AND FALSE THEN UPDATE SET a=999 WHEN MATCHED THEN UPDATE SET a=t.b+s.d,b=t.a WHEN NOT MATCHED THEN INSERT VALUES(s.id,s.d,0,'new')"
		run(fixture.Exec(q, 2))
		read := fixture.Select("SELECT * FROM t ORDER BY id", []string{"id", "a", "b", "note"}, []string{"INT4", "INT4", "INT4", "TEXT"}, [][]any{{int64(1), int64(21), int64(10), "old"}, {int64(2), int64(2), int64(0), "new"}})
		run(read)
		held, err := b.DB.PrepareContext(p.Ctx, "MERGE INTO t USING (VALUES(1,$1::integer)) s(id,d) ON t.id=s.id WHEN MATCHED THEN UPDATE SET a=t.b+s.d,b=t.a")
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		r, err := held.ExecContext(p.Ctx, 3)
		mergeInsertAffected(t, b, r, err, 1)
		read.Rows[0] = []any{int64(1), int64(13), int64(21), "old"}
		run(read)
		s := fixture.ImplicitError("MERGE INTO t USING (VALUES(1),(1)) s(id) ON t.id=s.id WHEN MATCHED THEN UPDATE SET a=t.a+1", mergeUpdateCardinalityDiagnostic())
		s.ErrorStage = "execute"
		run(s)
		run(read)
		command := fixture.Select("MERGE INTO t USING (SELECT 1 AS id WHERE FALSE) s ON TRUE WHEN MATCHED THEN UPDATE SET a=0", []string{}, []string{}, [][]any{})
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
