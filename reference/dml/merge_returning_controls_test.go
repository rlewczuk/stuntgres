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

func TestReferenceMergeReturningControls(t *testing.T) {
	mergeReturningRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := func(s fixture.Step) { mergeReturningRun(t, p, b, s) }
		for _, q := range []string{"CREATE TABLE source(k INT)", "CREATE VIEW source_view AS SELECT * FROM source", "CREATE TABLE t(id INT GENERATED ALWAYS AS IDENTITY CONSTRAINT t_key UNIQUE,k INT,v INT DEFAULT 8,g INT GENERATED ALWAYS AS(v+1) STORED)"} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("INSERT INTO t(k,v) VALUES(1,10),(2,20),(3,30)", 3))
		run(fixture.Exec("INSERT INTO source VALUES(1),(2),(4)", 3))
		q := "MERGE INTO t USING source_view s ON t.k=s.k WHEN MATCHED AND s.k=1 THEN DELETE WHEN MATCHED THEN UPDATE SET v=t.v+1 WHEN NOT MATCHED BY SOURCE THEN DELETE WHEN NOT MATCHED THEN INSERT(k) VALUES(s.k) RETURNING merge_action(),s.k,t.id,t.v,t.g,old.v,new.v"
		expected := [][]any{{"DELETE", int64(1), int64(1), int64(10), int64(11), int64(10), nil}, {"UPDATE", int64(2), int64(2), int64(21), int64(22), int64(20), int64(21)}, {"DELETE", nil, int64(3), int64(30), int64(31), int64(30), nil}, {"INSERT", int64(4), int64(4), int64(8), int64(9), nil, int64(8)}}
		run(insertReturningBag(q, []string{"merge_action", "k", "id", "v", "g", "v", "v"}, []string{"TEXT", "INT4", "INT4", "INT4", "INT4", "INT4", "INT4"}, expected, prepared))
		read := fixture.Select("SELECT * FROM t ORDER BY id", []string{"id", "k", "v", "g"}, []string{"INT4", "INT4", "INT4", "INT4"}, [][]any{{int64(2), int64(2), int64(21), int64(22)}, {int64(4), int64(4), int64(8), int64(9)}})
		run(read)
		// Native replacement controls have no PostgreSQL API equivalent. Enabled
		// real mode also checks the actual emitted script through ordinary PG SQL.
		if !b.PG {
			heldSQL := "MERGE INTO t USING (VALUES(2)) s(k) ON t.k=s.k WHEN MATCHED THEN UPDATE SET v=t.v+1 RETURNING old.v,new.v"
			held, e := b.DB.PrepareContext(p.Ctx, heldSQL)
			if e != nil {
				t.Fatal(e)
			}
			defer held.Close()
			retained, e := held.QueryContext(p.Ctx)
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
			restored := fixture.Select(read.Query, read.Columns, read.Types, [][]any{{int64(2), int64(2), int64(22), int64(23)}, {int64(4), int64(4), int64(8), int64(9)}})
			run(restored)
			clone := fixture.CatalogControlPool(t, stuntgres.NewDriver(), t.Name()+"/clone")
			if e = stuntgres.Restore(p.Ctx, clone, bytes.NewReader(dump.Bytes())); e != nil {
				t.Fatal(e)
			}
			fixture.CatalogControlTuples(t, clone, restored.Query, restored.Rows)
			var canonical bytes.Buffer
			if e = stuntgres.Dump(p.Ctx, p.Native, &canonical); e != nil || !bytes.Equal(dump.Bytes(), canonical.Bytes()) {
				t.Fatal("canonical redump", e)
			}
			e = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE TABLE broken(id INT NOT NULL); INSERT INTO broken DEFAULT VALUES;"))
			d := fixture.ControlsReferenceDiagnostic("23502", `null value in column "id" of relation "broken" violates not-null constraint`)
			d.Detail = "Failing row contains (null)."
			d.Schema = "public"
			d.Table = "broken"
			d.Column = "id"
			fixture.AssertDiagnostic(t, e, &d)
			ctx, cancel := context.WithCancel(p.Ctx)
			e = stuntgres.Restore(ctx, p.Native, &fixture.CatalogControlCancelReader{Cancel: cancel})
			cancel()
			if !errors.Is(e, context.Canceled) {
				t.Fatal("cancelled restore", e)
			}
			e = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("MERGE INTO t USING (VALUES(2)) s(k) ON t.k=s.k WHEN MATCHED THEN DELETE RETURNING t.id"))
			fixture.AssertDiagnostic(t, e, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "restore accepts only CREATE/ALTER/DROP SCHEMA, CREATE TABLE/SEQUENCE/VIEW/INDEX/POLICY/RULE, materialized extension dependency ALTER, row-security/existing-index/eligible NOT VALID ALTER TABLE, INSERT, deferred reference DEFAULT, database-local COMMENT, generated sequence ownership/state and SET SESSION AUTHORIZATION"})
			canonical.Reset()
			if e = stuntgres.Dump(p.Ctx, p.Native, &canonical); e != nil || !bytes.Equal(dump.Bytes(), canonical.Bytes()) {
				t.Fatal("failed restore changed publication", e)
			}
			names, e := retained.Columns()
			if e != nil || len(names) != 2 || names[0] != "v" || names[1] != "v" {
				t.Fatal("retained schema", names, e)
			}
			if !retained.Next() {
				t.Fatal("retained output", retained.Err())
			}
			var old, new int64
			if e = retained.Scan(&old, &new); e != nil || old != 21 || new != 22 {
				t.Fatal("retained values", old, new, e)
			}
			if retained.Next() || retained.Err() != nil {
				t.Fatal("retained tail", retained.Err())
			}
			retained.Close()
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: b.DB, Statement: held, SQL: heldSQL}, b.Recorder, fixture.Select(heldSQL, []string{"v", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(22), int64(23)}}))
			if p.Postgres != nil {
				if _, e = p.Postgres.ExecContext(p.Ctx, dump.String()); e != nil {
					t.Fatalf("actual emitted script replay: %v\n%s", e, dump.String())
				}
				fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, restored)
				fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Exec("INSERT INTO t(k) VALUES(5)", 1))
				fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Select("SELECT id,v,g FROM t WHERE k=5", []string{"id", "v", "g"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(5), int64(8), int64(9)}}))
				if _, e = p.Postgres.ExecContext(p.Ctx, "DROP VIEW source_view; DROP TABLE source,t"); e != nil {
					t.Fatal(e)
				}
			}
		}
		run(fixture.Exec("INSERT INTO t(k) VALUES(5)", 1))
		run(fixture.Select("SELECT id,v,g FROM t WHERE k=5", []string{"id", "v", "g"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(5), int64(8), int64(9)}}))
	})
}
func TestReferenceMergeReturningFeatureSmoke(t *testing.T) {
	mergeReturningRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := func(s fixture.Step) { mergeReturningRun(t, p, b, s) }
		mergeReturningSeed(t, p, b)
		run(mergeReturningCoreStep(prepared))
		run(mergeReturningSurvivors())
		run(fixture.Exec("DELETE FROM s", 5))
		run(insertReturningBag("MERGE INTO t USING s ON t.id=s.sid WHEN NOT MATCHED BY SOURCE THEN UPDATE SET v=t.v+1 RETURNING merge_action(),s.sid,t.id,old.v,new.v", []string{"merge_action", "sid", "id", "v", "v"}, []string{"TEXT", "INT4", "INT4", "INT4", "INT4"}, [][]any{{"UPDATE", nil, int64(2), int64(28), int64(29)}, {"UPDATE", nil, int64(4), int64(9), int64(10)}, {"UPDATE", nil, int64(5), int64(50), int64(51)}}, prepared))
		run(fixture.Exec("INSERT INTO t VALUES(7,70)", 1))
		q := "MERGE INTO t USING (VALUES(7)) s(id) ON t.id=s.id WHEN MATCHED AND $1::boolean THEN UPDATE SET v=t.v+1 RETURNING merge_action(),old.v,new.v,$2 AS label"
		held, e := b.DB.PrepareContext(p.Ctx, q)
		if e != nil {
			t.Fatal(e)
		}
		defer held.Close()
		for _, flag := range []any{false, nil, true} {
			rows := [][]any{}
			if flag == true {
				rows = [][]any{{"UPDATE", int64(70), int64(71), "fresh"}}
			}
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: b.DB, Statement: held, SQL: q}, b.Recorder, fixture.Select(q, []string{"merge_action", "v", "v", "label"}, []string{"TEXT", "INT4", "INT4", "TEXT"}, rows, flag, "fresh"))
		}
		run(fixture.Exec("UPDATE t SET v=70 WHERE id=7", 1))
		n := int64(1)
		if b.PG {
			n = 0
		}
		s := fixture.Exec(q, n, true, "ignored")
		s.Prepared = prepared
		run(s)
		insertReturningFailure(t, p, b, b.DB, "MERGE INTO t USING (VALUES(2),(7)) s(id) ON t.id=s.id WHEN MATCHED THEN UPDATE SET v=t.v RETURNING 10/(7-t.id)", prepared, true, fixture.Diagnostic{Code: "22012", Message: "division by zero"})
		read := fixture.Select("SELECT * FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(29)}, {int64(4), int64(10)}, {int64(5), int64(51)}, {int64(7), int64(71)}})
		run(read)
		if !b.PG {
			retained, e := b.DB.QueryContext(p.Ctx, "MERGE INTO t USING (VALUES(7)) s(id) ON t.id=s.id WHEN MATCHED THEN UPDATE SET v=t.v RETURNING t.id,t.v")
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
			if e = stuntgres.Restore(p.Ctx, p.Native, &dump); e != nil {
				t.Fatal(e)
			}
			run(read)
			if !retained.Next() {
				t.Fatal(retained.Err())
			}
			var id, v int64
			if e = retained.Scan(&id, &v); e != nil || id != 7 || v != 71 {
				t.Fatal(id, v, e)
			}
			if retained.Next() || retained.Err() != nil {
				t.Fatal(retained.Err())
			}
		}
	})
}
