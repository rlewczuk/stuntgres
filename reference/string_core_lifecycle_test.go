//go:build reference

package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"stuntgres"
	"testing"
)

func TestReferenceStringCoreLifecycle(t *testing.T) {
	p := newReferencePair(t)
	for _, s := range []referenceStep{
		referenceExec("CREATE TABLE string_core_source(id INT,raw CHAR(8),form TEXT)", 0),
		referenceExec("INSERT INTO string_core_source VALUES(1,' é ','NFC'),(2,' x ','NFC'),(3,NULL,'NFC')", 3),
		referenceExec(`CREATE TABLE string_core_dest(id INT,v TEXT DEFAULT normalize(trim(' é ')),CHECK (v IS NORMALIZED))`, 0),
		referenceExec("INSERT INTO string_core_dest(id) VALUES(0)", 1),
		referencePrepared(referenceExec(`INSERT INTO string_core_dest SELECT id,"normalize"(btrim(raw),form)||'!' FROM string_core_source`, 3)),
		referenceExec(`CREATE VIEW string_core_view AS SELECT id,normalize(btrim(raw))||'!' AS v FROM string_core_source`, 0),
		referenceSelect("SELECT id,v FROM string_core_view ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "é!"}, {int64(2), "x!"}, {int64(3), nil}}),
		referencePrepared(referenceSelect("SELECT s.id,d.v FROM string_core_source s JOIN string_core_dest d ON normalize(btrim(s.raw))||'!'=d.v ORDER BY s.id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "é!"}, {int64(2), "x!"}})),
		referenceSelect("SELECT btrim(raw) AS v,count(*) AS n FROM string_core_source GROUP BY btrim(raw) HAVING btrim(raw) IS NORMALIZED ORDER BY v", []string{"v", "n"}, []string{"TEXT", "INT8"}, [][]any{{"x", int64(1)}}),
		referenceSelect("UPDATE string_core_dest SET v=normalize(v||'a') WHERE id=2 RETURNING id,v", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(2), "x!a"}}),
		referenceSelect("DELETE FROM string_core_dest WHERE btrim(v)='x!a' RETURNING id,v", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(2), "x!a"}}),
		referenceExec("UPDATE string_core_source SET form='bad' WHERE id=2", 1),
		referenceImplicitError(`INSERT INTO string_core_dest SELECT id+10,"normalize"(raw,form) FROM string_core_source`, referenceDiagnostic{Code: "22023", Message: "invalid normalization form: bad"}),
		referenceSelect("SELECT id FROM string_core_dest ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(0)}, {int64(1)}, {int64(3)}}),
		referenceExec("UPDATE string_core_source SET form='NFC' WHERE id=2", 1),
	} {
		p.step(t, s)
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
	p.step(t, referenceSelect("SELECT id,v FROM string_core_view ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "é!"}, {int64(2), "x!"}, {int64(3), nil}}))
	// Native dump is a SQL deliverable: replay it into the real oracle after clearing its schema.
	if _, err := p.postgres.ExecContext(p.ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
		t.Fatal(err)
	}
	if _, err := p.postgres.ExecContext(p.ctx, dump.String()); err != nil {
		t.Fatal("PostgreSQL dump replay", err)
	}
	p.step(t, referencePrepared(referenceSelect("SELECT id,v FROM string_core_view ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "é!"}, {int64(2), "x!"}, {int64(3), nil}})))
	ctx, cancel := context.WithCancel(p.ctx)
	cancel()
	for _, db := range []*sql.DB{p.memory, p.postgres} {
		rows, err := db.QueryContext(ctx, "SELECT normalize('x')")
		if rows != nil {
			rows.Close()
			t.Fatal("cancel exposed rows")
		}
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	}
}
