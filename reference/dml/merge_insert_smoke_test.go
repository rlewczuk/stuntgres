package reference_test

import (
	"bytes"
	"database/sql"
	"reflect"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceMergeInsertFeatureSmoke(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := fixture.MergeRun(t, p, b, false)
			run(fixture.Exec("CREATE TABLE t(id INT NOT NULL CONSTRAINT t_pkey UNIQUE,v TEXT,tail TEXT DEFAULT 'tail')", 0))
			run(fixture.Exec("CREATE TABLE source(id INT,v TEXT)", 0))
			run(fixture.Exec("INSERT INTO t VALUES(1,'old','old-tail')", 1))
			run(fixture.Exec("INSERT INTO source VALUES(1,'one'),(2,'two'),(3,'three')", 3))
			q := "MERGE INTO t USING source s ON t.id=s.id WHEN NOT MATCHED BY TARGET AND s.id=2 THEN INSERT(id,v) VALUES(s.id,s.v||'/first') WHEN NOT MATCHED THEN INSERT(id,v) VALUES(s.id,s.v||'/fallback')"
			stmt, err := b.DB.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			run(fixture.Select("SELECT * FROM t", []string{"id", "v", "tail"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(1), "old", "old-tail"}}))
			result, err := stmt.ExecContext(p.Ctx)
			mergeInsertAffected(t, b, result, err, 2)
			want := [][]any{{int64(1), "old", "old-tail"}, {int64(2), "two/first", "tail"}, {int64(3), "three/fallback", "tail"}}
			read := func(rows [][]any) fixture.Step {
				return fixture.Select("SELECT * FROM t ORDER BY id", []string{"id", "v", "tail"}, []string{"INT4", "TEXT", "TEXT"}, rows)
			}
			run(read(want))
			result, err = stmt.ExecContext(p.Ctx)
			mergeInsertAffected(t, b, result, err, 0)
			run(fixture.Exec("INSERT INTO source VALUES(4,'four')", 1))
			result, err = stmt.ExecContext(p.Ctx)
			mergeInsertAffected(t, b, result, err, 1)
			want = append(want, []any{int64(4), "four/fallback", "tail"})
			run(read(want))
			for _, prepared := range []bool{false, true} {
				command := fixture.Select(q, []string{}, []string{}, [][]any{})
				command.Prepared = prepared
				run(command)
			}
			failure := fixture.ImplicitError("MERGE INTO t USING (VALUES(7),(7)) s(id) ON t.id=s.id WHEN NOT MATCHED THEN INSERT(id) VALUES(s.id)", fixture.Diagnostic{Code: "23505", Message: `duplicate key value violates unique constraint "t_pkey"`, Detail: "Key (id)=(7) already exists.", Schema: "public", Table: "t", Constraint: "t_pkey"})
			failure.ErrorStage = "execute"
			for _, prepared := range []bool{false, true} {
				failure.Prepared = prepared
				run(failure)
				run(read(want))
			}
			run(fixture.Exec("CREATE SEQUENCE suppressed", 0))
			run(fixture.Exec("CREATE TABLE defaults(id INT DEFAULT nextval('suppressed'))", 0))
			for _, prepared := range []bool{false, true} {
				for _, source := range []string{"(VALUES(1)) s(id)", "(SELECT 1 AS id WHERE FALSE) s"} {
					s := fixture.Exec("MERGE INTO defaults USING "+source+" ON FALSE WHEN NOT MATCHED AND FALSE THEN INSERT DEFAULT VALUES", 0)
					s.Prepared = prepared
					run(s)
				}
			}
			run(fixture.Select("SELECT nextval('suppressed')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(1)}}))
			tx, err := b.DB.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			privateMerge := fixture.Prepared(fixture.Exec("MERGE INTO t USING (VALUES(5)) s(id) ON t.id=s.id WHEN NOT MATCHED THEN INSERT(id,v) VALUES(s.id,'private')", 1))
			if b.PG {
				privateMerge.Affected = 0
			}
			fixture.InsertSelectRun(t, p.Ctx, tx, b.Recorder, privateMerge)
			private := append(append([][]any{}, want...), []any{int64(5), "private", "tail"})
			fixture.InsertSelectRun(t, p.Ctx, tx, b.Recorder, read(private))
			run(read(want))
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			run(read(want))
			if !b.PG {
				mergeInsertSmokeControls(t, p, b, stmt, read(want), want)
			}
		})
	}
}

func mergeInsertSmokeControls(t *testing.T, p *fixture.Pair, b fixture.Backend, held *sql.Stmt, want fixture.Step, values [][]any) {
	t.Helper()
	retained, err := b.DB.QueryContext(p.Ctx, want.Query)
	if err != nil {
		t.Fatal(err)
	}
	defer retained.Close()
	if names, err := retained.Columns(); err != nil || !reflect.DeepEqual(names, want.Columns) {
		t.Fatalf("retained metadata=%v %v", names, err)
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
	var actual [][]any
	for retained.Next() {
		var id int64
		var v, tail string
		if err := retained.Scan(&id, &v, &tail); err != nil {
			t.Fatal(err)
		}
		actual = append(actual, []any{id, v, tail})
	}
	if err := retained.Err(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actual, values) {
		t.Fatalf("retained MERGE rows=%#v want=%#v", actual, values)
	}
	fixture.InsertSelectRun(t, p.Ctx, p.Memory, p.MemoryRecord, want)
	var second bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &second); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(dump.Bytes(), second.Bytes()) {
		t.Fatal("MERGE dump changed after restore")
	}
	if p.Postgres != nil {
		if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
			t.Fatalf("actual MERGE-produced dump replay: %v\n%s", err, dump.String())
		}
		fixture.InsertSelectRun(t, p.Ctx, p.Postgres, p.PostgresRecord, want)
		if _, err := p.Postgres.ExecContext(p.Ctx, "DROP TABLE t,source,defaults; DROP SEQUENCE suppressed"); err != nil {
			t.Fatal(err)
		}
	}
	err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE TABLE staged(id INT); INSERT INTO staged VALUES(1); CREATE TABLE required(id INT NOT NULL); INSERT INTO required VALUES(NULL);"))
	d := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23502", Message: `null value in column "id" of relation "required" violates not-null constraint`, Detail: "Failing row contains (null).", Schema: "public", Table: "required", Column: "id"}
	fixture.AssertDiagnostic(t, err, &d)
	var preserved bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &preserved); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(dump.Bytes(), preserved.Bytes()) {
		t.Fatal("failed Restore changed complete MERGE catalog")
	}
	result, err := held.ExecContext(p.Ctx)
	fixture.InsertSelectAffected(t, result, err, 0)
	fixture.InsertSelectRun(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO source VALUES(6,'six')", 1))
	result, err = held.ExecContext(p.Ctx)
	fixture.InsertSelectAffected(t, result, err, 1)
	want.Rows = append(append([][]any{}, values...), []any{int64(6), "six/fallback", "tail"})
	fixture.InsertSelectRun(t, p.Ctx, p.Memory, p.MemoryRecord, want)
}
