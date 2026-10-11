package reference_test

import (
	"bytes"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAlterRenameControls(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, q := range []string{"CREATE TABLE t(a INT CONSTRAINT nn NOT NULL,b TEXT)", "INSERT INTO t VALUES(7,'O''Brien'),(8,NULL)", "COMMENT ON TABLE t IS 'table comment'", "COMMENT ON COLUMN t.a IS 'column comment'", "ALTER TABLE t RENAME a TO renamed", "ALTER TABLE t RENAME TO newtable"} {
		count := int64(0)
		if strings.HasPrefix(q, "INSERT") {
			count = 2
		}
		fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, fixture.Exec(q, count))
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
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
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	if p.Postgres != nil {
		if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
			t.Fatal("canonical PG replay", err, dump.String())
		}
	}
	for _, backend := range p.Backends() {
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Prepared(fixture.Select("SELECT * FROM newtable ORDER BY renamed", []string{"renamed", "b"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "O'Brien"}, {int64(8), nil}})))
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.ImplicitError("INSERT INTO newtable VALUES(NULL,'bad')", fixture.Diagnostic{Code: "23502", Message: `null value in column "renamed" of relation "newtable" violates not-null constraint`, Detail: "Failing row contains (null, bad).", Schema: "public", Table: "newtable", Column: "renamed"}))
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Select("SELECT * FROM newtable ORDER BY renamed", []string{"renamed", "b"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "O'Brien"}, {int64(8), nil}}))
	}
	var again bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &again); err != nil || again.String() != dump.String() {
		t.Fatal(err, again.String())
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("ALTER TABLE newtable RENAME TO bad;")); err == nil {
		t.Fatal("Restore accepted ALTER history")
	}
	if referenceDDLNativeDump(t, p) != dump.String() {
		t.Fatal("failed Restore changed renamed catalog")
	}
	if p.Postgres == nil {
		return
	}
	var tableComment, columnComment, constraint string
	if err := p.Postgres.QueryRowContext(p.Ctx, "SELECT obj_description('newtable'::regclass),col_description('newtable'::regclass,1),(SELECT conname FROM pg_constraint WHERE conrelid='newtable'::regclass AND contype='n')").Scan(&tableComment, &columnComment, &constraint); err != nil || tableComment != "table comment" || columnComment != "column comment" || constraint != "nn" {
		t.Fatal(tableComment, columnComment, constraint, err)
	}
}
