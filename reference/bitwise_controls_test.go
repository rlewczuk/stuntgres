package reference_test

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceBitwiseDefaultsAndReplay(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE d(id INTEGER,s SMALLINT DEFAULT (1::smallint << 15),i INTEGER DEFAULT (~0),b BIGINT DEFAULT (1::bigint << 63),n SMALLINT DEFAULT (~NULL::smallint),w BIGINT DEFAULT (32767::smallint << 1::smallint))", 0))
	p.step(t, referenceExec("INSERT INTO d(id) VALUES(1)", 1))
	// Isolate the original direct DEFAULT route before the reference prepared route.
	p.step(t, referenceExec("INSERT INTO d VALUES(2,DEFAULT,DEFAULT,DEFAULT,DEFAULT,DEFAULT)", 1))
	directDefaults := referenceSelect("SELECT s,i,b,n,w FROM d WHERE id=2", []string{"s", "i", "b", "n", "w"}, []string{"INT2", "INT4", "INT8", "INT2", "INT8"}, [][]any{{int64(-32768), int64(-1), int64(-9223372036854775808), nil, int64(-2)}})
	p.step(t, directDefaults)
	p.step(t, referencePrepared(directDefaults))
	p.step(t, referenceExec("DELETE FROM d WHERE id=2", 1))
	p.step(t, referencePrepared(referenceExec("INSERT INTO d VALUES(2,DEFAULT,DEFAULT,DEFAULT,DEFAULT,DEFAULT)", 1)))
	p.step(t, referenceExec("INSERT INTO d VALUES(3,NULL,NULL,NULL,NULL,NULL)", 1))
	want := [][]any{{int64(-32768), int64(-1), int64(-9223372036854775808), nil, int64(-2)}, {int64(-32768), int64(-1), int64(-9223372036854775808), nil, int64(-2)}, {nil, nil, nil, nil, nil}}
	step := referenceSelect("SELECT s,i,b,n,w FROM d ORDER BY id", []string{"s", "i", "b", "n", "w"}, []string{"INT2", "INT4", "INT8", "INT2", "INT8"}, want)
	p.step(t, step)
	p.step(t, referencePrepared(step))
	p.step(t, referenceExec("CREATE VIEW masks AS SELECT id,~s AS ns,s << 32 AS ss,s & b AS mixed FROM d", 0))
	view := referenceSelect("SELECT ns,ss,mixed FROM masks ORDER BY id", []string{"ns", "ss", "mixed"}, []string{"INT2", "INT2", "INT8"}, [][]any{{int64(32767), int64(-32768), int64(-9223372036854775808)}, {int64(32767), int64(-32768), int64(-9223372036854775808)}, {nil, nil, nil}})
	p.step(t, referencePrepared(view))
	held, err := p.memory.QueryContext(context.WithValue(p.ctx, referenceRecorderKey{}, p.memoryRecord), "SELECT ns,ss,mixed FROM masks WHERE id=1")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	if t.Failed() {
		return // Do not replay an incomplete native fixture over the PostgreSQL oracle.
	}
	if p.postgres != nil {
		if _, err := p.postgres.ExecContext(p.ctx, "DROP VIEW masks"); err != nil {
			t.Fatal(err)
		}
	}
	referenceArithmeticRoundtrip(t, p, "d")
	meta, err := held.ColumnTypes()
	if err != nil || len(meta) != 3 {
		t.Fatal(meta, err)
	}
	for i, typ := range []string{"INT2", "INT2", "INT8"} {
		if meta[i].DatabaseTypeName() != typ {
			t.Fatal(meta[i])
		}
	}
	if !held.Next() {
		t.Fatal(held.Err())
	}
	var ns, ss, mixed int64
	if err := held.Scan(&ns, &ss, &mixed); err != nil || ns != 32767 || ss != -32768 || mixed != -9223372036854775808 {
		t.Fatal(ns, ss, mixed, err)
	}
	if held.Next() || held.Err() != nil {
		t.Fatal("held masks end", held.Err())
	}
	if err := held.Close(); err != nil {
		t.Fatal(err)
	}
	referenceArithmeticRecordedClosure(t, p.memoryRecord, "SELECT ns,ss,mixed FROM masks WHERE id=1", 3, 1)
	p.step(t, step)
	p.step(t, referencePrepared(view))
	p.step(t, referenceExec("INSERT INTO d(id) VALUES(4)", 1))
	p.step(t, referenceSelect("SELECT s,i,b,n,w FROM d WHERE id=4", []string{"s", "i", "b", "n", "w"}, []string{"INT2", "INT4", "INT8", "INT2", "INT8"}, [][]any{want[0]}))
	masks := referenceSelect("SELECT ns,ss,mixed FROM masks WHERE id=4", []string{"ns", "ss", "mixed"}, []string{"INT2", "INT2", "INT8"}, [][]any{{int64(32767), int64(-32768), int64(-9223372036854775808)}})
	p.step(t, masks)
	p.step(t, referencePrepared(masks))
	const bad = "CREATE TABLE failed(i INTEGER); INSERT INTO failed VALUES(1 & 1); INSERT INTO failed VALUES(~true)"
	failure := referenceImplicitError(bad, referenceDiagnostic{Code: "42883", Message: "operator does not exist: ~ boolean", Position: strconv.Itoa(strings.Index(bad, "~") + 1), Hint: "No operator matches the given name and argument type. You might need to add an explicit type cast."})
	referenceAssertDiagnostic(t, stuntgres.Restore(p.ctx, p.native, strings.NewReader(bad)), failure.diagnostic)
	remaining := referenceSelect("SELECT s FROM d WHERE id=4", []string{"s"}, []string{"INT2"}, [][]any{{int64(-32768)}})
	p.step(t, remaining)
	p.step(t, referencePrepared(remaining))
	canceled, cancel := context.WithCancel(p.ctx)
	cancel()
	if err := stuntgres.Restore(canceled, p.native, strings.NewReader(bad)); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestReferenceBitwiseWriteAtomicityAndParameterErrors(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE source(id INTEGER,x TEXT)", 0))
	p.step(t, referenceExec("INSERT INTO source VALUES(1,'1'),(2,'bad')", 2))
	p.step(t, referenceExec("CREATE TABLE dest(id INTEGER,s SMALLINT)", 0))
	p.step(t, referenceExec("INSERT INTO dest VALUES(99,99)", 1))
	for _, prepared := range []bool{false, true} {
		s := referenceImplicitError("INSERT INTO dest SELECT id,CAST(x AS INTEGER) & 1 FROM source ORDER BY id", referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`})
		s.prepared = prepared
		s.errorStage = "execute"
		p.step(t, s)
		p.step(t, referenceSelect("SELECT id,s FROM dest", []string{"id", "s"}, []string{"INT4", "INT2"}, [][]any{{int64(99), int64(99)}}))
		p.step(t, referencePrepared(referenceSelect("SELECT id,s FROM dest", []string{"id", "s"}, []string{"INT4", "INT2"}, [][]any{{int64(99), int64(99)}})))
	}
	p.step(t, referenceExec("INSERT INTO dest VALUES(1,1),(2,2)", 2))
	for _, prepared := range []bool{false, true} {
		s := referenceImplicitError("UPDATE dest SET s=CASE WHEN id=2 THEN 1::bigint << 16 ELSE s::bigint & 1 END", referenceDiagnostic{Code: "22003", Message: "smallint out of range"})
		s.prepared = prepared
		s.errorStage = "execute"
		p.step(t, s)
		p.step(t, referenceSelect("SELECT id,s FROM dest ORDER BY id", []string{"id", "s"}, []string{"INT4", "INT2"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(2)}, {int64(99), int64(99)}}))
		p.step(t, referencePrepared(referenceSelect("SELECT id,s FROM dest ORDER BY id", []string{"id", "s"}, []string{"INT4", "INT2"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(2)}, {int64(99), int64(99)}})))
	}
	for _, tc := range []struct{ arg, message, code string }{{"bad", `invalid input syntax for type smallint: "bad"`, "22P02"}, {"32768", `value "32768" is out of range for type smallint`, "22003"}, {"", `invalid input syntax for type smallint: ""`, "22P02"}} {
		for _, prepared := range []bool{false, true} {
			s := referenceImplicitError("SELECT s & $1 FROM dest WHERE id=1", referenceDiagnostic{Code: tc.code, Message: tc.message}, tc.arg)
			s.selectRows = true
			s.prepared = prepared
			s.errorStage = "execute"
			pg := *s.diagnostic
			pg.Context = "unnamed portal parameter $1 = '...'"
			if tc.arg == "" {
				pg.Context = "unnamed portal parameter $1 = ''"
			}
			s.diagnostic = &pg
			p.step(t, s)
		}
	}
	canceled, cancel := context.WithCancel(p.ctx)
	cancel()
	for _, backend := range p.backends() {
		result, err := backend.db.ExecContext(canceled, "INSERT INTO dest VALUES(4,1::smallint<<2)")
		if result != nil || !errors.Is(err, context.Canceled) {
			t.Fatal(result, err)
		}
	}
	p.step(t, referenceExec("INSERT INTO dest VALUES(4,1::smallint<<2)", 1))
}
