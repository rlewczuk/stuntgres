package reference_test

import (
	"bytes"
	"database/sql"
	"reflect"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceMergeInsertFeatureSmoke(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			run := mergeInsertRun(t, p, b, false)
			run(referenceExec("CREATE TABLE t(id INT NOT NULL CONSTRAINT t_pkey UNIQUE,v TEXT,tail TEXT DEFAULT 'tail')", 0))
			run(referenceExec("CREATE TABLE source(id INT,v TEXT)", 0))
			run(referenceExec("INSERT INTO t VALUES(1,'old','old-tail')", 1))
			run(referenceExec("INSERT INTO source VALUES(1,'one'),(2,'two'),(3,'three')", 3))
			q := "MERGE INTO t USING source s ON t.id=s.id WHEN NOT MATCHED BY TARGET AND s.id=2 THEN INSERT(id,v) VALUES(s.id,s.v||'/first') WHEN NOT MATCHED THEN INSERT(id,v) VALUES(s.id,s.v||'/fallback')"
			stmt, err := b.db.PrepareContext(p.ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			run(referenceSelect("SELECT * FROM t", []string{"id", "v", "tail"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(1), "old", "old-tail"}}))
			result, err := stmt.ExecContext(p.ctx)
			mergeInsertAffected(t, b, result, err, 2)
			want := [][]any{{int64(1), "old", "old-tail"}, {int64(2), "two/first", "tail"}, {int64(3), "three/fallback", "tail"}}
			read := func(rows [][]any) referenceStep {
				return referenceSelect("SELECT * FROM t ORDER BY id", []string{"id", "v", "tail"}, []string{"INT4", "TEXT", "TEXT"}, rows)
			}
			run(read(want))
			result, err = stmt.ExecContext(p.ctx)
			mergeInsertAffected(t, b, result, err, 0)
			run(referenceExec("INSERT INTO source VALUES(4,'four')", 1))
			result, err = stmt.ExecContext(p.ctx)
			mergeInsertAffected(t, b, result, err, 1)
			want = append(want, []any{int64(4), "four/fallback", "tail"})
			run(read(want))
			for _, prepared := range []bool{false, true} {
				command := referenceSelect(q, []string{}, []string{}, [][]any{})
				command.prepared = prepared
				run(command)
			}
			failure := referenceImplicitError("MERGE INTO t USING (VALUES(7),(7)) s(id) ON t.id=s.id WHEN NOT MATCHED THEN INSERT(id) VALUES(s.id)", referenceDiagnostic{Code: "23505", Message: `duplicate key value violates unique constraint "t_pkey"`, Detail: "Key (id)=(7) already exists.", Schema: "public", Table: "t", Constraint: "t_pkey"})
			failure.errorStage = "execute"
			for _, prepared := range []bool{false, true} {
				failure.prepared = prepared
				run(failure)
				run(read(want))
			}
			run(referenceExec("CREATE SEQUENCE suppressed", 0))
			run(referenceExec("CREATE TABLE defaults(id INT DEFAULT nextval('suppressed'))", 0))
			for _, prepared := range []bool{false, true} {
				for _, source := range []string{"(VALUES(1)) s(id)", "(SELECT 1 AS id WHERE FALSE) s"} {
					s := referenceExec("MERGE INTO defaults USING "+source+" ON FALSE WHEN NOT MATCHED AND FALSE THEN INSERT DEFAULT VALUES", 0)
					s.prepared = prepared
					run(s)
				}
			}
			run(referenceSelect("SELECT nextval('suppressed')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(1)}}))
			tx, err := b.db.BeginTx(p.ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			privateMerge := referencePrepared(referenceExec("MERGE INTO t USING (VALUES(5)) s(id) ON t.id=s.id WHEN NOT MATCHED THEN INSERT(id,v) VALUES(s.id,'private')", 1))
			if b.pg {
				privateMerge.affected = 0
			}
			insertSelectRun(t, p.ctx, tx, b.recorder, privateMerge)
			private := append(append([][]any{}, want...), []any{int64(5), "private", "tail"})
			insertSelectRun(t, p.ctx, tx, b.recorder, read(private))
			run(read(want))
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			run(read(want))
			if !b.pg {
				mergeInsertSmokeControls(t, p, b, stmt, read(want), want)
			}
		})
	}
}

func mergeInsertSmokeControls(t *testing.T, p *referencePair, b referenceBackend, held *sql.Stmt, want referenceStep, values [][]any) {
	t.Helper()
	retained, err := b.db.QueryContext(p.ctx, want.query)
	if err != nil {
		t.Fatal(err)
	}
	defer retained.Close()
	if names, err := retained.Columns(); err != nil || !reflect.DeepEqual(names, want.columns) {
		t.Fatalf("retained metadata=%v %v", names, err)
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(p.ctx, p.native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
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
	insertSelectRun(t, p.ctx, p.memory, p.memoryRecord, want)
	var second bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &second); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(dump.Bytes(), second.Bytes()) {
		t.Fatal("MERGE dump changed after restore")
	}
	if p.postgres != nil {
		if _, err := p.postgres.ExecContext(p.ctx, dump.String()); err != nil {
			t.Fatalf("actual MERGE-produced dump replay: %v\n%s", err, dump.String())
		}
		insertSelectRun(t, p.ctx, p.postgres, p.postgresRecord, want)
		if _, err := p.postgres.ExecContext(p.ctx, "DROP TABLE t,source,defaults; DROP SEQUENCE suppressed"); err != nil {
			t.Fatal(err)
		}
	}
	err = stuntgres.Restore(p.ctx, p.native, strings.NewReader("CREATE TABLE staged(id INT); INSERT INTO staged VALUES(1); CREATE TABLE required(id INT NOT NULL); INSERT INTO required VALUES(NULL);"))
	d := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23502", Message: `null value in column "id" of relation "required" violates not-null constraint`, Detail: "Failing row contains (null).", Schema: "public", Table: "required", Column: "id"}
	referenceAssertDiagnostic(t, err, &d)
	var preserved bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &preserved); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(dump.Bytes(), preserved.Bytes()) {
		t.Fatal("failed Restore changed complete MERGE catalog")
	}
	result, err := held.ExecContext(p.ctx)
	insertSelectAffected(t, result, err, 0)
	insertSelectRun(t, p.ctx, b.db, b.recorder, referenceExec("INSERT INTO source VALUES(6,'six')", 1))
	result, err = held.ExecContext(p.ctx)
	insertSelectAffected(t, result, err, 1)
	want.rows = append(append([][]any{}, values...), []any{int64(6), "six/fallback", "tail"})
	insertSelectRun(t, p.ctx, p.memory, p.memoryRecord, want)
}
