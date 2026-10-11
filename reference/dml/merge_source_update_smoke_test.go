package reference_test

import (
	"bytes"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceMergeSourceUpdateFeatureSmoke(t *testing.T) {
	mergeInsertScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := fixture.MergeRun(t, p, b, prepared)
		run(fixture.Exec("CREATE TABLE t(id INT NOT NULL CONSTRAINT t_key UNIQUE,a INT,b INT)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1,10,20),(2,30,40),(3,50,60)", 3))
		q := "MERGE INTO t USING (VALUES(1),(4)) s(id) ON t.id=s.id WHEN NOT MATCHED BY SOURCE AND FALSE THEN UPDATE SET a=999 WHEN NOT MATCHED BY SOURCE AND NULL::boolean THEN UPDATE SET a=998 WHEN NOT MATCHED BY SOURCE AND t.id=2 THEN UPDATE SET a=t.b+1,b=t.a WHEN NOT MATCHED BY SOURCE THEN UPDATE SET a=t.b+1,b=t.a"
		run(fixture.Exec(q, 2))
		read := fixture.Select("SELECT * FROM t ORDER BY id", []string{"id", "a", "b"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(10), int64(20)}, {int64(2), int64(41), int64(30)}, {int64(3), int64(61), int64(50)}})
		run(read)
		held, err := b.DB.PrepareContext(p.Ctx, "MERGE INTO t USING (VALUES(1)) s(id) ON t.id=s.id WHEN NOT MATCHED BY SOURCE THEN UPDATE SET a=t.a+$1::integer")
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		r, err := held.ExecContext(p.Ctx, 3)
		mergeInsertAffected(t, b, r, err, 2)
		read.Rows = [][]any{{int64(1), int64(10), int64(20)}, {int64(2), int64(44), int64(30)}, {int64(3), int64(64), int64(50)}}
		run(read)
		s := fixture.ImplicitError("MERGE INTO t USING (VALUES(1)) s(id) ON t.id=s.id WHEN NOT MATCHED BY SOURCE THEN UPDATE SET a=100/(t.id-3)", fixture.Diagnostic{Code: "22012", Message: "division by zero"})
		s.ErrorStage = "execute"
		run(s)
		run(read)
		metadata := fixture.Select("MERGE INTO t USING (VALUES(1),(2),(3)) s(id) ON t.id=s.id WHEN NOT MATCHED BY SOURCE THEN UPDATE SET a=0", []string{}, []string{}, [][]any{})
		metadata.Prepared = prepared
		run(metadata)
		if !b.PG {
			fixture.InsertSelectRun(t, p.Ctx, p.Memory, p.MemoryRecord, read)
			var dump bytes.Buffer
			if err = stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			owned := bytes.Clone(dump.Bytes())
			if err = stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err = stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(owned)); err != nil {
				t.Fatal(err)
			}
			run(read)
			fixture.InsertSelectRun(t, p.Ctx, p.Memory, p.MemoryRecord, read)
			var redump bytes.Buffer
			if err = stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil || !bytes.Equal(owned, redump.Bytes()) {
				t.Fatal("canonical live-head redump", err)
			}
		}
		r, err = held.ExecContext(p.Ctx, 1)
		mergeInsertAffected(t, b, r, err, 2)
		read.Rows = [][]any{{int64(1), int64(10), int64(20)}, {int64(2), int64(45), int64(30)}, {int64(3), int64(65), int64(50)}}
		run(read)
		if !b.PG {
			fixture.InsertSelectRun(t, p.Ctx, p.Memory, p.MemoryRecord, read)
		}
	})
}
