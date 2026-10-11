package reference_test

import (
	"bytes"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAlterNullabilityControls(t *testing.T) {
	p := fixture.NewScenario(t)
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, s) }
	run(fixture.Exec("CREATE TABLE t(a INT,b TEXT DEFAULT 'default')", 0))
	run(fixture.Exec("INSERT INTO t VALUES(7,'seed')", 1))
	run(fixture.Exec("COMMENT ON COLUMN t.a IS 'keep'", 0))
	dump := func() string {
		t.Helper()
		var data bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &data); err != nil {
			t.Fatal(err)
		}
		return data.String()
	}
	original := dump()
	held, err := p.Native.QueryContext(p.Ctx, "SELECT * FROM t")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	tx, err := p.Memory.BeginTx(p.Ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	fixture.RunStep(t, p.Ctx, tx, p.MemoryRecord, fixture.Exec(`ALTER TABLE t ADD CONSTRAINT "n""é" NOT NULL a, ALTER b SET NOT NULL`, 0))
	fixture.RunStep(t, p.Ctx, tx, p.MemoryRecord, fixture.Exec("INSERT INTO t VALUES(8,'private')", 1))
	if got := dump(); got != original {
		t.Fatal("private descriptor/rows leaked", got)
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if got := dump(); got != original {
		t.Fatal("rollback changed canonical stream", got)
	}
	tx, err = p.Memory.BeginTx(p.Ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	fixture.RunStep(t, p.Ctx, tx, p.MemoryRecord, fixture.Exec(`ALTER TABLE t ADD CONSTRAINT "n""é" NOT NULL a, ALTER b SET NOT NULL`, 0))
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	committed := dump()
	if committed == original {
		t.Fatal("committed metadata absent")
	}
	if err = stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(committed)); err != nil {
		t.Fatal(err)
	}
	if got := dump(); got != committed {
		t.Fatal("logical roundtrip changed", got)
	}
	columns, err := held.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	fixture.TableDDLColumnTypes(t, false, columns, []string{"a", "b"}, []string{"INT4", "TEXT"}, []bool{true, true})
	if !held.Next() {
		t.Fatal("missing retained eager row", held.Err())
	}
	var a, b any
	if err = held.Scan(&a, &b); err != nil {
		t.Fatal(err)
	}
	fixture.AssertRawValue(t, false, "INT4", a)
	fixture.AssertRawValue(t, false, "TEXT", b)
	if a != int64(7) || b != "seed" {
		t.Fatal(a, b)
	}
	if held.Next() || held.Err() != nil {
		t.Fatal("unexpected retained eager cardinality", held.Err())
	}
	if err = held.Close(); err != nil {
		t.Fatal(err)
	}
	if p.Postgres != nil {
		// The unchanged native stream is consumed by the real pinned PostgreSQL server.
		if _, err = p.Postgres.ExecContext(p.Ctx, committed); err != nil {
			t.Fatal("PostgreSQL native-stream consumer", err)
		}
		fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Select("SELECT * FROM t", []string{"a", "b"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "seed"}}))
		fixture.TableDDLMetadata(t, p.Ctx, p.Postgres, true, false, "SELECT * FROM t", []string{"a", "b"}, []string{"INT4", "TEXT"}, []bool{false, false}, 1)
		var comment string
		if err = p.Postgres.QueryRowContext(p.Ctx, "SELECT col_description('t'::regclass,1)").Scan(&comment); err != nil || comment != "keep" {
			t.Fatal(comment, err)
		}
	}
	for _, backend := range p.Backends() {
		r := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
		r(fixture.Prepared(fixture.Exec(`ALTER TABLE t DROP CONSTRAINT "n""é", DROP CONSTRAINT t_b_not_null`, 0)))
		r(fixture.Exec("INSERT INTO t VALUES(NULL,NULL)", 1))
		r(fixture.Exec("INSERT INTO t(a) VALUES(9)", 1))
		r(fixture.Select("SELECT b FROM t WHERE a=9", []string{"b"}, []string{"TEXT"}, [][]any{{"default"}}))
	}
	sentinel := dump()
	err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE TABLE replacement(a INT NOT NULL);INSERT INTO replacement VALUES(NULL)"))
	want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23502", Message: `null value in column "a" of relation "replacement" violates not-null constraint`, Detail: "Failing row contains (null).", Schema: "public", Table: "replacement", Column: "a"}
	fixture.AssertDiagnostic(t, err, &want)
	if got := dump(); got != sentinel {
		t.Fatal("failed Restore changed sentinel")
	}
}

func TestReferenceAlterNullabilityControlEpoch(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE t(a INT,b TEXT)", 0))
			run(fixture.Exec("INSERT INTO t VALUES(7,'seed'),(NULL,NULL)", 2))
			tx, err := b.DB.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec("DELETE FROM t WHERE a IS NULL", 1))
			fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec("ALTER TABLE t ALTER a SET NOT NULL", 0))
			if !b.PG {
				if err = stuntgres.Reset(p.Ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				fixture.TransactionOrdinary(t, tx.Commit(), "stuntgres: catalog replaced before publication")
			} else {
				// PostgreSQL has no host catalog replacement API; assert the ordinary SQL publication.
				if err = tx.Commit(); err != nil {
					t.Fatal(err)
				}
				run(fixture.Select("SELECT * FROM t", []string{"a", "b"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "seed"}}))
				run(fixture.ImplicitError("INSERT INTO t VALUES(NULL,'bad')", fixture.Diagnostic{Code: "23502", Message: `null value in column "a" of relation "t" violates not-null constraint`, Detail: "Failing row contains (null, bad).", Schema: "public", Table: "t", Column: "a"}))
			}
		})
	}
}
