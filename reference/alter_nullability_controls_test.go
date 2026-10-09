package reference_test

import (
	"bytes"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceAlterNullabilityControls(t *testing.T) {
	p := newReferenceScenario(t)
	run := func(s referenceStep) { referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, s) }
	run(referenceExec("CREATE TABLE t(a INT,b TEXT DEFAULT 'default')", 0))
	run(referenceExec("INSERT INTO t VALUES(7,'seed')", 1))
	run(referenceExec("COMMENT ON COLUMN t.a IS 'keep'", 0))
	dump := func() string {
		t.Helper()
		var data bytes.Buffer
		if err := stuntgres.Dump(p.ctx, p.native, &data); err != nil {
			t.Fatal(err)
		}
		return data.String()
	}
	original := dump()
	held, err := p.native.QueryContext(p.ctx, "SELECT * FROM t")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	tx, err := p.memory.BeginTx(p.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	referenceRunStep(t, p.ctx, tx, p.memoryRecord, referenceExec(`ALTER TABLE t ADD CONSTRAINT "n""é" NOT NULL a, ALTER b SET NOT NULL`, 0))
	referenceRunStep(t, p.ctx, tx, p.memoryRecord, referenceExec("INSERT INTO t VALUES(8,'private')", 1))
	if got := dump(); got != original {
		t.Fatal("private descriptor/rows leaked", got)
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if got := dump(); got != original {
		t.Fatal("rollback changed canonical stream", got)
	}
	tx, err = p.memory.BeginTx(p.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	referenceRunStep(t, p.ctx, tx, p.memoryRecord, referenceExec(`ALTER TABLE t ADD CONSTRAINT "n""é" NOT NULL a, ALTER b SET NOT NULL`, 0))
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	committed := dump()
	if committed == original {
		t.Fatal("committed metadata absent")
	}
	if err = stuntgres.Reset(p.ctx, p.native); err != nil {
		t.Fatal(err)
	}
	if err = stuntgres.Restore(p.ctx, p.native, strings.NewReader(committed)); err != nil {
		t.Fatal(err)
	}
	if got := dump(); got != committed {
		t.Fatal("logical roundtrip changed", got)
	}
	columns, err := held.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	referenceTableDDLColumnTypes(t, false, columns, []string{"a", "b"}, []string{"INT4", "TEXT"}, []bool{true, true})
	if !held.Next() {
		t.Fatal("missing retained eager row", held.Err())
	}
	var a, b any
	if err = held.Scan(&a, &b); err != nil {
		t.Fatal(err)
	}
	referenceAssertRawValue(t, false, "INT4", a)
	referenceAssertRawValue(t, false, "TEXT", b)
	if a != int64(7) || b != "seed" {
		t.Fatal(a, b)
	}
	if held.Next() || held.Err() != nil {
		t.Fatal("unexpected retained eager cardinality", held.Err())
	}
	if err = held.Close(); err != nil {
		t.Fatal(err)
	}
	if p.postgres != nil {
		// The unchanged native stream is consumed by the real pinned PostgreSQL server.
		if _, err = p.postgres.ExecContext(p.ctx, committed); err != nil {
			t.Fatal("PostgreSQL native-stream consumer", err)
		}
		referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, referenceSelect("SELECT * FROM t", []string{"a", "b"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "seed"}}))
		referenceTableDDLMetadata(t, p.ctx, p.postgres, true, false, "SELECT * FROM t", []string{"a", "b"}, []string{"INT4", "TEXT"}, []bool{false, false}, 1)
		var comment string
		if err = p.postgres.QueryRowContext(p.ctx, "SELECT col_description('t'::regclass,1)").Scan(&comment); err != nil || comment != "keep" {
			t.Fatal(comment, err)
		}
	}
	for _, backend := range p.backends() {
		r := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
		r(referencePrepared(referenceExec(`ALTER TABLE t DROP CONSTRAINT "n""é", DROP CONSTRAINT t_b_not_null`, 0)))
		r(referenceExec("INSERT INTO t VALUES(NULL,NULL)", 1))
		r(referenceExec("INSERT INTO t(a) VALUES(9)", 1))
		r(referenceSelect("SELECT b FROM t WHERE a=9", []string{"b"}, []string{"TEXT"}, [][]any{{"default"}}))
	}
	sentinel := dump()
	err = stuntgres.Restore(p.ctx, p.native, strings.NewReader("CREATE TABLE replacement(a INT NOT NULL);INSERT INTO replacement VALUES(NULL)"))
	want := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23502", Message: `null value in column "a" of relation "replacement" violates not-null constraint`, Detail: "Failing row contains (null).", Schema: "public", Table: "replacement", Column: "a"}
	referenceAssertDiagnostic(t, err, &want)
	if got := dump(); got != sentinel {
		t.Fatal("failed Restore changed sentinel")
	}
}

func TestReferenceAlterNullabilityControlEpoch(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
			run(referenceExec("CREATE TABLE t(a INT,b TEXT)", 0))
			run(referenceExec("INSERT INTO t VALUES(7,'seed'),(NULL,NULL)", 2))
			tx, err := b.db.BeginTx(p.ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			referenceRunStep(t, p.ctx, tx, b.recorder, referenceExec("DELETE FROM t WHERE a IS NULL", 1))
			referenceRunStep(t, p.ctx, tx, b.recorder, referenceExec("ALTER TABLE t ALTER a SET NOT NULL", 0))
			if !b.pg {
				if err = stuntgres.Reset(p.ctx, p.native); err != nil {
					t.Fatal(err)
				}
				transactionOrdinary(t, tx.Commit(), "stuntgres: catalog replaced before publication")
			} else {
				// PostgreSQL has no host catalog replacement API; assert the ordinary SQL publication.
				if err = tx.Commit(); err != nil {
					t.Fatal(err)
				}
				run(referenceSelect("SELECT * FROM t", []string{"a", "b"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "seed"}}))
				run(referenceImplicitError("INSERT INTO t VALUES(NULL,'bad')", referenceDiagnostic{Code: "23502", Message: `null value in column "a" of relation "t" violates not-null constraint`, Detail: "Failing row contains (null, bad).", Schema: "public", Table: "t", Column: "a"}))
			}
		})
	}
}
