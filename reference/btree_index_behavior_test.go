//go:build reference

package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
)

func referenceBtreeStreams(t *testing.T, p *referencePair, fn func(*testing.T, *sql.DB, *referenceRecorder)) {
	t.Helper()
	for _, backend := range []struct {
		name     string
		db       *sql.DB
		recorder *referenceRecorder
	}{{"native", p.memory, p.memoryRecord}, {"postgres", p.postgres, p.postgresRecord}} {
		t.Run(backend.name, func(t *testing.T) { fn(t, backend.db, backend.recorder) })
	}
}
func TestReferenceBtreeIndexMaintenance(t *testing.T) {
	p := newReferencePair(t)
	referenceBtreeStreams(t, p, func(t *testing.T, db *sql.DB, recorder *referenceRecorder) {
		for _, step := range []referenceStep{
			referenceExec("CREATE TABLE t(n INT,s TEXT)", 0),
			referenceExec("INSERT INTO t VALUES(2,'b'),(1,'a'),(2,'b'),(NULL,NULL),(3,'c')", 5),
			referenceExec("CREATE INDEX ni ON t(n DESC NULLS LAST,s ASC NULLS FIRST)", 0),
			referencePrepared(referenceExec("CREATE INDEX si ON t USING btree(s)", 0)),
			referenceExec("INSERT INTO t VALUES(2,'b')", 1),
			referencePrepared(referenceExec("INSERT INTO t VALUES($1,$2)", 1, 0, "")),
			referencePrepared(referenceExec("INSERT INTO t VALUES($1,$2)", 1, nil, nil)),
			referenceExec("INSERT INTO t SELECT n,s FROM t WHERE n=3", 1),
			referenceSelect("SELECT n FROM t ORDER BY n ASC NULLS LAST", []string{"n"}, []string{"INT4"}, [][]any{{int64(0)}, {int64(1)}, {int64(2)}, {int64(2)}, {int64(2)}, {int64(3)}, {int64(3)}, {nil}, {nil}}),
			referenceSelect("SELECT n FROM t ORDER BY n DESC NULLS LAST", []string{"n"}, []string{"INT4"}, [][]any{{int64(3)}, {int64(3)}, {int64(2)}, {int64(2)}, {int64(2)}, {int64(1)}, {int64(0)}, {nil}, {nil}}),
			referenceSelect("SELECT n FROM t ORDER BY n ASC NULLS FIRST", []string{"n"}, []string{"INT4"}, [][]any{{nil}, {nil}, {int64(0)}, {int64(1)}, {int64(2)}, {int64(2)}, {int64(2)}, {int64(3)}, {int64(3)}}),
			referenceExec("UPDATE t SET n=10 WHERE n=1", 1),
			referenceExec("DELETE FROM t WHERE n=3", 2),
			referenceSelect("SELECT n FROM t ORDER BY n ASC NULLS FIRST", []string{"n"}, []string{"INT4"}, [][]any{{nil}, {nil}, {int64(0)}, {int64(2)}, {int64(2)}, {int64(2)}, {int64(10)}}),
		} {
			referenceRunStep(t, p.ctx, db, recorder, step)
		}
		for _, tc := range []struct {
			predicate string
			boundary  any
			rows      [][]any
		}{
			{"n<$1", 2, [][]any{{int64(0)}}}, {"n<=$1", 0, [][]any{{int64(0)}}},
			{"n>$1", 2, [][]any{{int64(10)}}}, {"n>=$1", 10, [][]any{{int64(10)}}},
			{"n=$1", 99, nil}, {"n=$1", nil, nil},
			{"n IS NOT NULL", nil, [][]any{{int64(0)}, {int64(2)}, {int64(2)}, {int64(2)}, {int64(10)}}},
		} {
			args := []any{tc.boundary}
			if tc.predicate == "n IS NOT NULL" {
				args = nil
			}
			step := referencePrepared(referenceSelect("SELECT n FROM t WHERE "+tc.predicate, []string{"n"}, []string{"INT4"}, tc.rows, args...))
			step.bag = true
			referenceRunStep(t, p.ctx, db, recorder, step)
		}
	})
}
func TestReferenceBtreeIndexDiagnostics(t *testing.T) {
	p := newReferencePair(t)
	referenceBtreeStreams(t, p, func(t *testing.T, db *sql.DB, recorder *referenceRecorder) {
		for _, step := range []referenceStep{referenceExec("CREATE TABLE t(n INT)", 0), referenceExec("CREATE SEQUENCE seq", 0), referenceExec("CREATE VIEW v AS SELECT n FROM t", 0), referenceExec("CREATE INDEX idx ON t(n)", 0)} {
			referenceRunStep(t, p.ctx, db, recorder, step)
		}
		for _, tc := range []struct{ query, code, message, detail string }{
			{"CREATE INDEX retry ON public.missing(n)", "42P01", `relation "public.missing" does not exist`, ""},
			{"CREATE INDEX retry ON absent.t(n)", "3F000", `schema "absent" does not exist`, ""},
			{"CREATE INDEX idx ON t(missing)", "42703", `column "missing" does not exist`, ""},
			{"CREATE INDEX IF NOT EXISTS idx ON t(missing)", "42703", `column "missing" does not exist`, ""},
			{"CREATE INDEX retry ON seq(n)", "42809", `cannot create index on relation "seq"`, "This operation is not supported for sequences."},
			{"CREATE INDEX retry ON v(n)", "42809", `cannot create index on relation "v"`, "This operation is not supported for views."},
			{"CREATE INDEX retry ON idx(n)", "42809", `cannot open relation "idx"`, "This operation is not supported for indexes."},
			{"CREATE INDEX retry ON t USING absent(n)", "42704", `access method "absent" does not exist`, ""},
			{"CREATE INDEX retry ON t(ctid)", "0A000", "index creation on system columns is not supported", ""},
			{"CREATE INDEX retry ON t(" + strings.TrimSuffix(strings.Repeat("n,", 33), ",") + ")", "54011", "cannot use more than 32 columns in an index", ""},
		} {
			step := referenceImplicitError(tc.query, referenceDiagnostic{Code: tc.code, Message: tc.message, Detail: tc.detail})
			step.errorStage = "execute"
			referenceRunStep(t, p.ctx, db, recorder, step)
			referenceRunStep(t, p.ctx, db, recorder, referencePrepared(step))
			referenceRunStep(t, p.ctx, db, recorder, referenceSelect("SELECT relname FROM pg_catalog.pg_class WHERE relname='retry'", []string{"relname"}, []string{"NAME"}, nil))
		}
		for _, step := range []referenceStep{
			referenceExec("CREATE INDEX retry ON t(n)", 0),
			referenceExec("CREATE INDEX ON t(n)", 0), referenceExec("CREATE INDEX ON t(n)", 0),
			referenceSelect("SELECT relname,relkind,relnatts FROM pg_catalog.pg_class WHERE relname='t_n_idx' OR relname='t_n_idx1' ORDER BY relname", []string{"relname", "relkind", "relnatts"}, []string{"NAME", "CHAR", "INT2"}, [][]any{{"t_n_idx", "i", int64(1)}, {"t_n_idx1", "i", int64(1)}}),
		} {
			referenceRunStep(t, p.ctx, db, recorder, step)
		}
	})
}
func TestReferenceBtreeIndexControls(t *testing.T) {
	p := newReferencePair(t)
	for _, step := range []referenceStep{
		referenceExec("CREATE TABLE t(n INT,s TEXT)", 0),
		referenceExec("INSERT INTO t VALUES(2,'b'),(NULL,NULL),(2,'b')", 3),
		referenceExec("CREATE INDEX idx ON t(n DESC NULLS LAST,s ASC NULLS FIRST)", 0),
		referenceExec("ALTER TABLE t RENAME n TO key", 0), referenceExec("ALTER TABLE t RENAME TO renamed", 0),
	} {
		referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, step)
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dump.String(), `CREATE INDEX "idx" ON "public"."renamed" USING btree ("key" DESC NULLS LAST, "s" ASC NULLS FIRST);`) {
		t.Fatal(dump.String())
	}
	if err := stuntgres.Reset(p.ctx, p.native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(dump.String())); err != nil {
		t.Fatal(err)
	}
	if _, err := p.postgres.ExecContext(p.ctx, dump.String()); err != nil {
		t.Fatal("real PostgreSQL dump replay", err)
	}
	for _, step := range []referenceStep{
		referenceExec("INSERT INTO renamed VALUES(3,'c')", 1),
		referenceSelect("SELECT key,s FROM renamed ORDER BY key DESC NULLS LAST", []string{"key", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(3), "c"}, {int64(2), "b"}, {int64(2), "b"}, {nil, nil}}),
		referenceSelect("SELECT relkind FROM pg_catalog.pg_class WHERE relname='idx'", []string{"relkind"}, []string{"CHAR"}, [][]any{{"i"}}),
	} {
		p.step(t, step)
	}
	if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(dump.String()+"CREATE INDEX broken ON renamed(missing);")); err == nil {
		t.Fatal("bad late index replaced destination")
	}
	p.step(t, referenceSelect("SELECT key FROM renamed ORDER BY key DESC NULLS LAST", []string{"key"}, []string{"INT4"}, [][]any{{int64(3)}, {int64(2)}, {int64(2)}, {nil}}))
}
func TestReferenceBtreeIndexTransactionDistinction(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE t(n INT)", 0))
	referenceBtreeStreams(t, p, func(t *testing.T, db *sql.DB, recorder *referenceRecorder) {
		tx, err := db.BeginTx(p.ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		if db == p.memory {
			step := referencePrepared(referenceImplicitError("CREATE INDEX idx ON t(n)", referenceDiagnostic{Code: "0A000", Message: "statement is unsupported in an explicit transaction"}))
			step.errorStage = "execute"
			referenceRunStep(t, p.ctx, tx, recorder, step)
		} else {
			referenceRunStep(t, p.ctx, tx, recorder, referencePrepared(referenceExec("CREATE INDEX idx ON t(n)", 0)))
			referenceRunStep(t, p.ctx, tx, recorder, referenceSelect("SELECT relname FROM pg_catalog.pg_class WHERE relname='idx'", []string{"relname"}, []string{"NAME"}, [][]any{{"idx"}}))
		}
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		referenceRunStep(t, p.ctx, db, recorder, referenceSelect("SELECT relname FROM pg_catalog.pg_class WHERE relname='idx'", []string{"relname"}, []string{"NAME"}, nil))
		tx, err = db.BeginTx(p.ctx, &sql.TxOptions{ReadOnly: true})
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		step := referencePrepared(referenceImplicitError("CREATE INDEX idx ON missing(n)", referenceDiagnostic{Code: "25006", Message: "cannot execute CREATE INDEX in a read-only transaction"}))
		step.errorStage = "execute"
		referenceRunStep(t, p.ctx, tx, recorder, step)
	})
}
