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

func TestReferenceMergeNothingControls(t *testing.T) {
	mergeNothingRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := fixture.MergeRun(t, p, b, prepared)
		for _, q := range []string{"CREATE TABLE source(k INT)", "CREATE VIEW source_view AS SELECT * FROM source", "CREATE TABLE t(id INT GENERATED ALWAYS AS IDENTITY CONSTRAINT t_key UNIQUE,k INT,v INT DEFAULT 8,g INT GENERATED ALWAYS AS(v+1) STORED)"} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("INSERT INTO t(k,v) VALUES(1,10),(2,20),(3,30)", 3))
		run(fixture.Exec("INSERT INTO source VALUES(1),(2),(4),(5)", 4))
		q := "MERGE INTO t USING source_view s ON t.k=s.k WHEN MATCHED AND s.k=1 THEN DO NOTHING WHEN MATCHED THEN UPDATE SET v=t.v+1 WHEN NOT MATCHED BY SOURCE THEN DO NOTHING WHEN NOT MATCHED AND s.k=4 THEN DO NOTHING WHEN NOT MATCHED THEN INSERT(k) VALUES(s.k) RETURNING merge_action(),t.id,t.k,t.v,t.g"
		run(insertReturningBag(q, []string{"merge_action", "id", "k", "v", "g"}, []string{"TEXT", "INT4", "INT4", "INT4", "INT4"}, [][]any{{"UPDATE", int64(2), int64(2), int64(21), int64(22)}, {"INSERT", int64(4), int64(5), int64(8), int64(9)}}, prepared))
		read := fixture.Select("SELECT * FROM t ORDER BY id", []string{"id", "k", "v", "g"}, []string{"INT4", "INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(1), int64(10), int64(11)}, {int64(2), int64(2), int64(21), int64(22)}, {int64(3), int64(3), int64(30), int64(31)}, {int64(4), int64(5), int64(8), int64(9)}})
		run(read)
		if !b.PG {
			heldSQL := "MERGE INTO t USING (VALUES(1)) s(k) ON t.k=s.k WHEN MATCHED AND $1::boolean THEN DO NOTHING WHEN MATCHED THEN UPDATE SET v=t.v+1 RETURNING old.v,new.v"
			held, e := b.DB.PrepareContext(p.Ctx, heldSQL)
			if e != nil {
				t.Fatal(e)
			}
			defer held.Close()
			retained, e := b.DB.QueryContext(p.Ctx, "MERGE INTO t USING (VALUES(2)) s(k) ON t.k=s.k WHEN MATCHED THEN UPDATE SET v=t.v RETURNING t.id,t.v")
			if e != nil {
				t.Fatal(e)
			}
			defer retained.Close()
			var dump bytes.Buffer
			if e = stuntgres.Dump(p.Ctx, p.Native, &dump); e != nil {
				t.Fatal(e)
			}
			if e = stuntgres.Reset(p.Ctx, p.Native); e != nil {
				t.Fatal(e)
			}
			if e = stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); e != nil {
				t.Fatal(e)
			}
			run(read)
			clone := fixture.CatalogControlPool(t, stuntgres.NewDriver(), t.Name()+"/clone")
			if e = stuntgres.Restore(p.Ctx, clone, bytes.NewReader(dump.Bytes())); e != nil {
				t.Fatal(e)
			}
			fixture.CatalogControlTuples(t, clone, read.Query, read.Rows)
			var canonical bytes.Buffer
			if e = stuntgres.Dump(p.Ctx, p.Native, &canonical); e != nil || !bytes.Equal(dump.Bytes(), canonical.Bytes()) {
				t.Fatal("canonical redump", e)
			}
			e = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE TABLE broken(id INT NOT NULL); INSERT INTO broken DEFAULT VALUES;"))
			d := fixture.ControlsReferenceDiagnostic("23502", `null value in column "id" of relation "broken" violates not-null constraint`)
			d.Detail, d.Schema, d.Table, d.Column = "Failing row contains (null).", "public", "broken", "id"
			fixture.AssertDiagnostic(t, e, &d)
			ctx, cancel := context.WithCancel(p.Ctx)
			e = stuntgres.Restore(ctx, p.Native, &fixture.CatalogControlCancelReader{Cancel: cancel})
			cancel()
			if !errors.Is(e, context.Canceled) {
				t.Fatal("cancel restore", e)
			}
			canonical.Reset()
			if e = stuntgres.Dump(p.Ctx, p.Native, &canonical); e != nil || !bytes.Equal(dump.Bytes(), canonical.Bytes()) {
				t.Fatal("failed restore publication", e)
			}
			names, e := retained.Columns()
			if e != nil || len(names) != 2 || names[0] != "id" || names[1] != "v" {
				t.Fatal(names, e)
			}
			meta, e := retained.ColumnTypes()
			if e != nil || meta[0].DatabaseTypeName() != "INT4" || meta[1].DatabaseTypeName() != "INT4" {
				t.Fatal(meta, e)
			}
			if !retained.Next() {
				t.Fatal(retained.Err())
			}
			var id, v int64
			if e = retained.Scan(&id, &v); e != nil || id != 2 || v != 21 {
				t.Fatal(id, v, e)
			}
			if retained.Next() || retained.Err() != nil {
				t.Fatal(retained.Err())
			}
			retained.Close()
			x := fixture.HeldStatement{Executor: b.DB, Statement: held, SQL: heldSQL}
			fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.Select(heldSQL, []string{"v", "v"}, []string{"INT4", "INT4"}, [][]any{}, true))
			fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.Select(heldSQL, []string{"v", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(10), int64(11)}}, false))
			if p.Postgres != nil {
				if _, e = p.Postgres.ExecContext(p.Ctx, dump.String()); e != nil {
					t.Fatalf("actual dump replay: %v\n%s", e, dump.String())
				}
				fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, read)
				fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Exec("INSERT INTO t(k) VALUES(6)", 1))
				fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Select("SELECT id,v,g FROM t WHERE k=6", []string{"id", "v", "g"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(5), int64(8), int64(9)}}))
				if _, e = p.Postgres.ExecContext(p.Ctx, "DROP VIEW source_view; DROP TABLE source,t"); e != nil {
					t.Fatal(e)
				}
			}
		}
		run(fixture.Exec("INSERT INTO t(k) VALUES(6)", 1))
		run(fixture.Select("SELECT id,v,g FROM t WHERE k=6", []string{"id", "v", "g"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(5), int64(8), int64(9)}}))
	})
}
func TestReferenceMergeNothingFeatureSmoke(t *testing.T) {
	mergeNothingRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := fixture.MergeRun(t, p, b, prepared)
		mergeNothingSeed(t, p, b)
		run(mergeNothingMixedStep(prepared))
		run(mergeNothingSurvivors())
		mergeNothingHeldExercise(t, p, b)
		run(insertReturningBag("MERGE INTO t USING (VALUES(1)) x(id) ON t.id=x.id WHEN MATCHED THEN DO NOTHING RETURNING 10/(t.id-t.id)", []string{"?column?"}, []string{"INT4"}, [][]any{}, prepared))
		q := "MERGE INTO t USING (SELECT id FROM (VALUES(8),(9),(10)) x(id) ORDER BY id) x ON FALSE WHEN NOT MATCHED AND x.id=8 THEN DO NOTHING WHEN NOT MATCHED THEN INSERT VALUES(x.id,10/(10-x.id)) RETURNING t.id"
		insertReturningFailure(t, p, b, b.DB, q, prepared, true, fixture.Diagnostic{Code: "22012", Message: "division by zero"})
		read := fixture.Select("SELECT * FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(12)}, {int64(2), int64(21)}, {int64(4), int64(40)}, {int64(7), int64(70)}})
		run(read)
		if !b.PG {
			var dump bytes.Buffer
			if e := stuntgres.Dump(p.Ctx, p.Native, &dump); e != nil {
				t.Fatal(e)
			}
			if e := stuntgres.Reset(p.Ctx, p.Native); e != nil {
				t.Fatal(e)
			}
			if e := stuntgres.Restore(p.Ctx, p.Native, &dump); e != nil {
				t.Fatal(e)
			}
			run(read)
		}
	})
}
