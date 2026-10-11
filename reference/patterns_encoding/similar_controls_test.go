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

func TestReferenceSimilarControls(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, s := range []fixture.Step{
		fixture.Exec(`CREATE TABLE t(id INTEGER,v TEXT,p TEXT,e TEXT,CONSTRAINT valid CHECK(v SIMILAR TO 'a%'))`, 0),
		fixture.Exec(`INSERT INTO t VALUES(1,'abc','a#"%#"','#'),(2,NULL,'a%','#')`, 2),
		fixture.Exec(`CREATE VIEW matched AS SELECT id,v SIMILAR TO 'a%' AS m,v NOT SIMILAR TO p ESCAPE e AS n,substring(v SIMILAR p ESCAPE e) AS piece,substring(v,p,e) AS ordinary FROM t`, 0),
	} {
		p.Step(t, s)
	}
	want := fixture.Select(`SELECT m,n,piece,ordinary FROM matched ORDER BY id`, []string{"m", "n", "piece", "ordinary"}, []string{"BOOL", "BOOL", "TEXT", "TEXT"}, [][]any{{true, false, "bc", "bc"}, {nil, nil, nil, nil}})
	want.CheckMetadata = similarReferenceDeclaredMetadata(false)
	paired := func(step fixture.Step) {
		for _, b := range p.Backends() {
			step.CheckMetadata = similarReferenceDeclaredMetadata(b.PG)
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, step)
		}
	}
	paired(want)
	paired(fixture.Prepared(want))
	var held []fixture.HeldStatement
	for _, b := range p.Backends() {
		stmt, err := b.DB.PrepareContext(p.Ctx, want.Query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held = append(held, fixture.HeldStatement{b.DB, stmt, want.Query})
	}
	p.Step(t, fixture.Exec(`ALTER TABLE t RENAME COLUMN v TO value`, 0))
	for i, b := range p.Backends() {
		step := want
		step.CheckMetadata = similarReferenceDeclaredMetadata(b.PG)
		fixture.RunStep(t, p.Ctx, held[i], b.Recorder, step)
	}
	old, err := p.Native.QueryContext(p.Ctx, want.Query)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	if t.Failed() {
		t.FailNow()
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dump.String(), "SIMILAR") || strings.Contains(dump.String(), "similar_to_escape") {
		t.Fatal("Dump lost symbolic SQL-regex provenance", dump.String())
	}
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	fixture.StringCoreRetainedRows(t, old, nil, want.Columns, want.Types, want.Rows, false)
	paired(want)
	var redump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil || !bytes.Equal(dump.Bytes(), redump.Bytes()) {
		t.Fatal("unstable redump", err)
	}
	if p.Postgres != nil {
		if _, err := p.Postgres.ExecContext(p.Ctx, `DROP VIEW matched; DROP TABLE t`); err != nil {
			t.Fatal(err)
		}
		if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
			t.Fatal(err)
		}
		step := want
		step.CheckMetadata = similarReferenceDeclaredMetadata(true)
		fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, step)
	}
	for _, suffix := range []string{`INSERT INTO t VALUES(3,'bad','%','#');`, `CREATE TABLE replacement(v TEXT); INSERT INTO replacement VALUES('created'); SELECT 'a' SIMILAR TO '[';`} {
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String()+suffix)); err == nil {
			t.Fatal("invalid replay published")
		}
		fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, want)
	}
	canceled, cancel := context.WithCancel(p.Ctx)
	cancel()
	if err := stuntgres.Restore(canceled, p.Native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	var after bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil || !bytes.Equal(redump.Bytes(), after.Bytes()) {
		t.Fatal("failed/canceled replay changed catalog", err)
	}
	for i, b := range p.Backends() {
		step := want
		step.CheckMetadata = similarReferenceDeclaredMetadata(b.PG)
		fixture.RunStep(t, p.Ctx, held[i], b.Recorder, step)
	}
}

func TestReferenceSimilarStoredAtomicity(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Exec(`CREATE TABLE guarded(v TEXT CONSTRAINT valid CHECK(v SIMILAR TO 'a%'))`, 0))
		run(fixture.Exec(`INSERT INTO guarded VALUES('a'),(NULL)`, 2))
		want := fixture.Select(`SELECT v FROM guarded ORDER BY v`, []string{"v"}, []string{"TEXT"}, [][]any{{"a"}, {nil}})
		for _, q := range []string{`INSERT INTO guarded VALUES('ab'),('b')`, `UPDATE guarded SET v='b'`} {
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError(q, fixture.CheckViolation("guarded", "valid", "b"))
				s.ErrorStage = "execute"
				s.Prepared = prepared
				run(s)
				run(want)
			}
		}
		run(fixture.Exec(`CREATE TABLE generated_t(id INTEGER,v TEXT,p TEXT,e TEXT,m BOOLEAN GENERATED ALWAYS AS (v SIMILAR TO p ESCAPE e) STORED)`, 0))
		run(fixture.Exec(`INSERT INTO generated_t(id,v,p,e) VALUES(1,'a','a','#'),(2,NULL,'a','#')`, 2))
		selected := fixture.Select(`SELECT id,m FROM generated_t ORDER BY id`, []string{"id", "m"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}, {int64(2), nil}})
		similarReferenceRoutes(t, p, b, selected)
		for _, prepared := range []bool{false, true} {
			s := similarReferenceRegexError(`INSERT INTO generated_t(id,v,p,e) VALUES(3,'a','a','#'),(4,'a','[','#')`, "brackets [] not balanced", "")
			s.Prepared = prepared
			s.ErrorStage = "execute"
			run(s)
			run(selected)
		}
		run(fixture.Exec(`CREATE INDEX similar_idx ON generated_t ((v SIMILAR TO p ESCAPE e))`, 0))
		run(fixture.Exec(`ALTER TABLE generated_t RENAME COLUMN v TO value`, 0))
		run(selected)
		run(fixture.Exec(`CREATE TABLE defaults_t(m BOOLEAN DEFAULT ('a' SIMILAR TO 'a'))`, 0))
		run(fixture.Exec(`INSERT INTO defaults_t DEFAULT VALUES`, 1))
		similarReferenceRoutes(t, p, b, fixture.Select(`SELECT m FROM defaults_t`, []string{"m"}, []string{"BOOL"}, [][]any{{true}}))
	}
}
