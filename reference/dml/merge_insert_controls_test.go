package reference_test

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceMergeInsertControls(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := fixture.MergeRun(t, p, b, false)
			for _, q := range []string{"CREATE TABLE source(k INT,v INT)", "CREATE VIEW source_view AS SELECT k,v FROM source", "CREATE TABLE t(id INT GENERATED ALWAYS AS IDENTITY CONSTRAINT t_pkey UNIQUE,k INT,v INT DEFAULT 8,g INT GENERATED ALWAYS AS(v+1) STORED)"} {
				run(fixture.Exec(q, 0))
			}
			run(fixture.Exec("INSERT INTO source VALUES(1,7)", 1))
			q := "MERGE INTO t USING source_view s ON t.k=s.k WHEN NOT MATCHED AND s.k=1 THEN INSERT(k,v) VALUES(s.k,s.v) WHEN NOT MATCHED THEN INSERT(k) VALUES(s.k)"
			stmt, err := b.DB.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			r, err := stmt.ExecContext(p.Ctx)
			mergeInsertAffected(t, b, r, err, 1)
			read := fixture.Select("SELECT * FROM t ORDER BY id", []string{"id", "k", "v", "g"}, []string{"INT4", "INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(1), int64(7), int64(8)}})
			run(read)
			if !b.PG {
				retained, err := b.DB.QueryContext(p.Ctx, read.Query)
				if err != nil {
					t.Fatal(err)
				}
				defer retained.Close()
				names, err := retained.Columns()
				if err != nil || !reflect.DeepEqual(names, read.Columns) {
					t.Fatal("retained names", names, err)
				}
				types, err := retained.ColumnTypes()
				if err != nil || len(types) != 4 {
					t.Fatal("retained types", types, err)
				}
				for _, typ := range types {
					if typ.DatabaseTypeName() != "INT4" || typ.ScanType() != reflect.TypeFor[int64]() {
						t.Fatal("retained type", typ)
					}
				}
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
				if !retained.Next() {
					t.Fatal("retained row lost", retained.Err())
				}
				var id, k, v, g int64
				if err := retained.Scan(&id, &k, &v, &g); err != nil || id != 1 || k != 1 || v != 7 || g != 8 {
					t.Fatal("retained row", id, k, v, g, err)
				}
				if retained.Next() || retained.Err() != nil {
					t.Fatal("retained terminal", retained.Err())
				}
				fixture.InsertSelectRun(t, p.Ctx, p.Memory, p.MemoryRecord, read)
				var restored bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &restored); err != nil || !bytes.Equal(dump.Bytes(), restored.Bytes()) {
					t.Fatal("restored symbolic objects/sequence differed", err)
				}
				bad := "CREATE TABLE staged(id INT); INSERT INTO staged VALUES(1); CREATE TABLE required(id INT NOT NULL); INSERT INTO required DEFAULT VALUES;"
				err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad))
				d := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23502", Message: `null value in column "id" of relation "required" violates not-null constraint`, Detail: "Failing row contains (null).", Schema: "public", Table: "required", Column: "id"}
				fixture.AssertDiagnostic(t, err, &d)
				ctx, cancel := context.WithCancel(p.Ctx)
				err = stuntgres.Restore(ctx, p.Native, &fixture.CatalogControlCancelReader{Cancel: cancel})
				cancel()
				if !errors.Is(err, context.Canceled) {
					t.Fatal("staged canceled replacement", err)
				}
				var unchanged bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &unchanged); err != nil || !bytes.Equal(dump.Bytes(), unchanged.Bytes()) {
					t.Fatal("failed/canceled control changed complete state", err)
				}
				if p.Postgres != nil {
					if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
						t.Fatalf("actual symbolic MERGE dump replay: %v\n%s", err, dump.String())
					}
					fixture.InsertSelectRun(t, p.Ctx, p.Postgres, p.PostgresRecord, read)
					result, err := p.Postgres.ExecContext(p.Ctx, "INSERT INTO source VALUES(2,70)")
					fixture.InsertSelectAffected(t, result, err, 1)
					result, err = p.Postgres.ExecContext(p.Ctx, q)
					mergeInsertAffected(t, fixture.Backend{PG: true}, result, err, 1)
					future := read
					future.Rows = append(append([][]any{}, read.Rows...), []any{int64(2), int64(2), int64(8), int64(9)})
					fixture.InsertSelectRun(t, p.Ctx, p.Postgres, p.PostgresRecord, future)
					if _, err := p.Postgres.ExecContext(p.Ctx, "DROP VIEW source_view; DROP TABLE source,t"); err != nil {
						t.Fatal(err)
					}
				}
			}
			r, err = stmt.ExecContext(p.Ctx)
			mergeInsertAffected(t, b, r, err, 0)
			run(fixture.Exec("INSERT INTO source VALUES(2,70)", 1))
			r, err = stmt.ExecContext(p.Ctx)
			mergeInsertAffected(t, b, r, err, 1)
			read.Rows = append(read.Rows, []any{int64(2), int64(2), int64(8), int64(9)})
			run(read)
		})
	}
}

func TestReferenceMergeInsertRetainedControls(t *testing.T) {
	mergeInsertScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := fixture.MergeRun(t, p, b, prepared)
		run(fixture.Exec("CREATE TABLE t(id INT,v TEXT)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1,'old')", 1))
		retained, err := b.DB.QueryContext(p.Ctx, "SELECT * FROM t ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		names, err := retained.Columns()
		if err != nil || !reflect.DeepEqual(names, []string{"id", "v"}) {
			t.Fatal(names, err)
		}
		run(fixture.Exec("MERGE INTO t USING (VALUES(2,'new')) s(id,v) ON t.id=s.id WHEN NOT MATCHED THEN INSERT VALUES(s.id,s.v)", 1))
		if !retained.Next() {
			t.Fatal("old eager result lost", retained.Err())
		}
		var id int64
		var v string
		if err := retained.Scan(&id, &v); err != nil || id != 1 || v != "old" {
			t.Fatal(id, v, err)
		}
		if retained.Next() || retained.Err() != nil {
			t.Fatal("old result saw later insertion", retained.Err())
		}
		run(fixture.Select("SELECT * FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "old"}, {int64(2), "new"}}))
	})
}
