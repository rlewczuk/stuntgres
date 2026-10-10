package reference_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceSimilarControls(t *testing.T) {
	p := newReferenceScenario(t)
	for _, s := range []referenceStep{
		referenceExec(`CREATE TABLE t(id INTEGER,v TEXT,p TEXT,e TEXT,CONSTRAINT valid CHECK(v SIMILAR TO 'a%'))`, 0),
		referenceExec(`INSERT INTO t VALUES(1,'abc','a#"%#"','#'),(2,NULL,'a%','#')`, 2),
		referenceExec(`CREATE VIEW matched AS SELECT id,v SIMILAR TO 'a%' AS m,v NOT SIMILAR TO p ESCAPE e AS n,substring(v SIMILAR p ESCAPE e) AS piece,substring(v,p,e) AS ordinary FROM t`, 0),
	} {
		p.step(t, s)
	}
	want := referenceSelect(`SELECT m,n,piece,ordinary FROM matched ORDER BY id`, []string{"m", "n", "piece", "ordinary"}, []string{"BOOL", "BOOL", "TEXT", "TEXT"}, [][]any{{true, false, "bc", "bc"}, {nil, nil, nil, nil}})
	want.checkMetadata = similarReferenceDeclaredMetadata(false)
	paired := func(step referenceStep) {
		for _, b := range p.backends() {
			step.checkMetadata = similarReferenceDeclaredMetadata(b.pg)
			referenceRunStep(t, p.ctx, b.db, b.recorder, step)
		}
	}
	paired(want)
	paired(referencePrepared(want))
	var held []referenceHeldStatement
	for _, b := range p.backends() {
		stmt, err := b.db.PrepareContext(p.ctx, want.query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held = append(held, referenceHeldStatement{b.db, stmt, want.query})
	}
	p.step(t, referenceExec(`ALTER TABLE t RENAME COLUMN v TO value`, 0))
	for i, b := range p.backends() {
		step := want
		step.checkMetadata = similarReferenceDeclaredMetadata(b.pg)
		referenceRunStep(t, p.ctx, held[i], b.recorder, step)
	}
	old, err := p.native.QueryContext(p.ctx, want.query)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	if t.Failed() {
		t.FailNow()
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dump.String(), "SIMILAR") || strings.Contains(dump.String(), "similar_to_escape") {
		t.Fatal("Dump lost symbolic SQL-regex provenance", dump.String())
	}
	if err := stuntgres.Reset(p.ctx, p.native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	stringCoreRetainedRows(t, old, nil, want.columns, want.types, want.rows, false)
	paired(want)
	var redump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &redump); err != nil || !bytes.Equal(dump.Bytes(), redump.Bytes()) {
		t.Fatal("unstable redump", err)
	}
	if p.postgres != nil {
		if _, err := p.postgres.ExecContext(p.ctx, `DROP VIEW matched; DROP TABLE t`); err != nil {
			t.Fatal(err)
		}
		if _, err := p.postgres.ExecContext(p.ctx, dump.String()); err != nil {
			t.Fatal(err)
		}
		step := want
		step.checkMetadata = similarReferenceDeclaredMetadata(true)
		referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, step)
	}
	for _, suffix := range []string{`INSERT INTO t VALUES(3,'bad','%','#');`, `CREATE TABLE replacement(v TEXT); INSERT INTO replacement VALUES('created'); SELECT 'a' SIMILAR TO '[';`} {
		if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(dump.String()+suffix)); err == nil {
			t.Fatal("invalid replay published")
		}
		referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, want)
	}
	canceled, cancel := context.WithCancel(p.ctx)
	cancel()
	if err := stuntgres.Restore(canceled, p.native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	var after bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &after); err != nil || !bytes.Equal(redump.Bytes(), after.Bytes()) {
		t.Fatal("failed/canceled replay changed catalog", err)
	}
	for i, b := range p.backends() {
		step := want
		step.checkMetadata = similarReferenceDeclaredMetadata(b.pg)
		referenceRunStep(t, p.ctx, held[i], b.recorder, step)
	}
}

func TestReferenceSimilarStoredAtomicity(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
		run(referenceExec(`CREATE TABLE guarded(v TEXT CONSTRAINT valid CHECK(v SIMILAR TO 'a%'))`, 0))
		run(referenceExec(`INSERT INTO guarded VALUES('a'),(NULL)`, 2))
		want := referenceSelect(`SELECT v FROM guarded ORDER BY v`, []string{"v"}, []string{"TEXT"}, [][]any{{"a"}, {nil}})
		for _, q := range []string{`INSERT INTO guarded VALUES('ab'),('b')`, `UPDATE guarded SET v='b'`} {
			for _, prepared := range []bool{false, true} {
				s := referenceImplicitError(q, referenceCheckViolation("guarded", "valid", "b"))
				s.errorStage = "execute"
				s.prepared = prepared
				run(s)
				run(want)
			}
		}
		run(referenceExec(`CREATE TABLE generated_t(id INTEGER,v TEXT,p TEXT,e TEXT,m BOOLEAN GENERATED ALWAYS AS (v SIMILAR TO p ESCAPE e) STORED)`, 0))
		run(referenceExec(`INSERT INTO generated_t(id,v,p,e) VALUES(1,'a','a','#'),(2,NULL,'a','#')`, 2))
		selected := referenceSelect(`SELECT id,m FROM generated_t ORDER BY id`, []string{"id", "m"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}, {int64(2), nil}})
		similarReferenceRoutes(t, p, b, selected)
		for _, prepared := range []bool{false, true} {
			s := similarReferenceRegexError(`INSERT INTO generated_t(id,v,p,e) VALUES(3,'a','a','#'),(4,'a','[','#')`, "brackets [] not balanced", "")
			s.prepared = prepared
			s.errorStage = "execute"
			run(s)
			run(selected)
		}
		run(referenceExec(`CREATE INDEX similar_idx ON generated_t ((v SIMILAR TO p ESCAPE e))`, 0))
		run(referenceExec(`ALTER TABLE generated_t RENAME COLUMN v TO value`, 0))
		run(selected)
		run(referenceExec(`CREATE TABLE defaults_t(m BOOLEAN DEFAULT ('a' SIMILAR TO 'a'))`, 0))
		run(referenceExec(`INSERT INTO defaults_t DEFAULT VALUES`, 1))
		similarReferenceRoutes(t, p, b, referenceSelect(`SELECT m FROM defaults_t`, []string{"m"}, []string{"BOOL"}, [][]any{{true}}))
	}
}
