//go:build reference

package reference_test

import (
	"bytes"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceAlterRenameControls(t *testing.T) {
	p := newReferencePair(t)
	for _, q := range []string{"CREATE TABLE t(a INT CONSTRAINT nn NOT NULL,b TEXT)", "INSERT INTO t VALUES(7,'O''Brien'),(8,NULL)", "COMMENT ON TABLE t IS 'table comment'", "COMMENT ON COLUMN t.a IS 'column comment'", "ALTER TABLE t RENAME a TO renamed", "ALTER TABLE t RENAME TO newtable"} {
		if _, err := p.native.ExecContext(p.ctx, q); err != nil {
			t.Fatal(q, err)
		}
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{`"newtable"`, `"renamed"`, `"nn"`, "table comment", "column comment", "O''Brien"} {
		if !strings.Contains(dump.String(), fragment) {
			t.Fatal(fragment, dump.String())
		}
	}
	if strings.Contains(dump.String(), "ALTER TABLE") {
		t.Fatal(dump.String())
	}
	if err := stuntgres.Reset(p.ctx, p.native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	if _, err := p.postgres.ExecContext(p.ctx, dump.String()); err != nil {
		t.Fatal("canonical PG replay", err, dump.String())
	}
	for _, db := range []referenceExecutor{p.memory, p.postgres} {
		referenceRunStep(t, p.ctx, db, &referenceRecorder{}, referencePrepared(referenceSelect("SELECT * FROM newtable ORDER BY renamed", []string{"renamed", "b"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "O'Brien"}, {int64(8), nil}})))
		referenceRunStep(t, p.ctx, db, &referenceRecorder{}, referenceImplicitError("INSERT INTO newtable VALUES(NULL,'bad')", referenceDiagnostic{Code: "23502", Message: `null value in column "renamed" of relation "newtable" violates not-null constraint`, Detail: "Failing row contains (null, bad).", Schema: "public", Table: "newtable", Column: "renamed"}))
	}
	var again bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &again); err != nil || again.String() != dump.String() {
		t.Fatal(err, again.String())
	}
	var tableComment, columnComment, constraint string
	if err := p.postgres.QueryRowContext(p.ctx, "SELECT obj_description('newtable'::regclass),col_description('newtable'::regclass,1),(SELECT conname FROM pg_constraint WHERE conrelid='newtable'::regclass AND contype='n')").Scan(&tableComment, &columnComment, &constraint); err != nil || tableComment != "table comment" || columnComment != "column comment" || constraint != "nn" {
		t.Fatal(tableComment, columnComment, constraint, err)
	}
}
