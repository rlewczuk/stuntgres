package reference_test

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceBitwiseDefaultsAndReplay(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE d(id INTEGER,s SMALLINT DEFAULT (1::smallint << 15),i INTEGER DEFAULT (~0),b BIGINT DEFAULT (1::bigint << 63),n SMALLINT DEFAULT (~NULL::smallint),w BIGINT DEFAULT (32767::smallint << 1::smallint))", 0))
	p.Step(t, fixture.Exec("INSERT INTO d(id) VALUES(1)", 1))
	// Isolate the original direct DEFAULT route before the reference prepared route.
	p.Step(t, fixture.Exec("INSERT INTO d VALUES(2,DEFAULT,DEFAULT,DEFAULT,DEFAULT,DEFAULT)", 1))
	directDefaults := fixture.Select("SELECT s,i,b,n,w FROM d WHERE id=2", []string{"s", "i", "b", "n", "w"}, []string{"INT2", "INT4", "INT8", "INT2", "INT8"}, [][]any{{int64(-32768), int64(-1), int64(-9223372036854775808), nil, int64(-2)}})
	p.Step(t, directDefaults)
	p.Step(t, fixture.Prepared(directDefaults))
	p.Step(t, fixture.Exec("DELETE FROM d WHERE id=2", 1))
	p.Step(t, fixture.Prepared(fixture.Exec("INSERT INTO d VALUES(2,DEFAULT,DEFAULT,DEFAULT,DEFAULT,DEFAULT)", 1)))
	p.Step(t, fixture.Exec("INSERT INTO d VALUES(3,NULL,NULL,NULL,NULL,NULL)", 1))
	want := [][]any{{int64(-32768), int64(-1), int64(-9223372036854775808), nil, int64(-2)}, {int64(-32768), int64(-1), int64(-9223372036854775808), nil, int64(-2)}, {nil, nil, nil, nil, nil}}
	step := fixture.Select("SELECT s,i,b,n,w FROM d ORDER BY id", []string{"s", "i", "b", "n", "w"}, []string{"INT2", "INT4", "INT8", "INT2", "INT8"}, want)
	p.Step(t, step)
	p.Step(t, fixture.Prepared(step))
	p.Step(t, fixture.Exec("CREATE VIEW masks AS SELECT id,~s AS ns,s << 32 AS ss,s & b AS mixed FROM d", 0))
	view := fixture.Select("SELECT ns,ss,mixed FROM masks ORDER BY id", []string{"ns", "ss", "mixed"}, []string{"INT2", "INT2", "INT8"}, [][]any{{int64(32767), int64(-32768), int64(-9223372036854775808)}, {int64(32767), int64(-32768), int64(-9223372036854775808)}, {nil, nil, nil}})
	p.Step(t, fixture.Prepared(view))
	held, err := p.Memory.QueryContext(context.WithValue(p.Ctx, fixture.RecorderKey{}, p.MemoryRecord), "SELECT ns,ss,mixed FROM masks WHERE id=1")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	if t.Failed() {
		return // Do not replay an incomplete native fixture over the PostgreSQL oracle.
	}
	if p.Postgres != nil {
		if _, err := p.Postgres.ExecContext(p.Ctx, "DROP VIEW masks"); err != nil {
			t.Fatal(err)
		}
	}
	fixture.ArithmeticRoundtrip(t, p, "d")
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
	referenceArithmeticRecordedClosure(t, p.MemoryRecord, "SELECT ns,ss,mixed FROM masks WHERE id=1", 3, 1)
	p.Step(t, step)
	p.Step(t, fixture.Prepared(view))
	p.Step(t, fixture.Exec("INSERT INTO d(id) VALUES(4)", 1))
	p.Step(t, fixture.Select("SELECT s,i,b,n,w FROM d WHERE id=4", []string{"s", "i", "b", "n", "w"}, []string{"INT2", "INT4", "INT8", "INT2", "INT8"}, [][]any{want[0]}))
	masks := fixture.Select("SELECT ns,ss,mixed FROM masks WHERE id=4", []string{"ns", "ss", "mixed"}, []string{"INT2", "INT2", "INT8"}, [][]any{{int64(32767), int64(-32768), int64(-9223372036854775808)}})
	p.Step(t, masks)
	p.Step(t, fixture.Prepared(masks))
	const bad = "CREATE TABLE failed(i INTEGER); INSERT INTO failed VALUES(1 & 1); INSERT INTO failed VALUES(~true)"
	failure := fixture.ImplicitError(bad, fixture.Diagnostic{Code: "42883", Message: "operator does not exist: ~ boolean", Position: strconv.Itoa(strings.Index(bad, "~") + 1), Hint: "No operator matches the given name and argument type. You might need to add an explicit type cast."})
	fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad)), failure.Diagnostic)
	remaining := fixture.Select("SELECT s FROM d WHERE id=4", []string{"s"}, []string{"INT2"}, [][]any{{int64(-32768)}})
	p.Step(t, remaining)
	p.Step(t, fixture.Prepared(remaining))
	canceled, cancel := context.WithCancel(p.Ctx)
	cancel()
	if err := stuntgres.Restore(canceled, p.Native, strings.NewReader(bad)); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestReferenceBitwiseWriteAtomicityAndParameterErrors(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE source(id INTEGER,x TEXT)", 0))
	p.Step(t, fixture.Exec("INSERT INTO source VALUES(1,'1'),(2,'bad')", 2))
	p.Step(t, fixture.Exec("CREATE TABLE dest(id INTEGER,s SMALLINT)", 0))
	p.Step(t, fixture.Exec("INSERT INTO dest VALUES(99,99)", 1))
	for _, prepared := range []bool{false, true} {
		s := fixture.ImplicitError("INSERT INTO dest SELECT id,CAST(x AS INTEGER) & 1 FROM source ORDER BY id", fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`})
		s.Prepared = prepared
		s.ErrorStage = "execute"
		p.Step(t, s)
		p.Step(t, fixture.Select("SELECT id,s FROM dest", []string{"id", "s"}, []string{"INT4", "INT2"}, [][]any{{int64(99), int64(99)}}))
		p.Step(t, fixture.Prepared(fixture.Select("SELECT id,s FROM dest", []string{"id", "s"}, []string{"INT4", "INT2"}, [][]any{{int64(99), int64(99)}})))
	}
	p.Step(t, fixture.Exec("INSERT INTO dest VALUES(1,1),(2,2)", 2))
	for _, prepared := range []bool{false, true} {
		s := fixture.ImplicitError("UPDATE dest SET s=CASE WHEN id=2 THEN 1::bigint << 16 ELSE s::bigint & 1 END", fixture.Diagnostic{Code: "22003", Message: "smallint out of range"})
		s.Prepared = prepared
		s.ErrorStage = "execute"
		p.Step(t, s)
		p.Step(t, fixture.Select("SELECT id,s FROM dest ORDER BY id", []string{"id", "s"}, []string{"INT4", "INT2"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(2)}, {int64(99), int64(99)}}))
		p.Step(t, fixture.Prepared(fixture.Select("SELECT id,s FROM dest ORDER BY id", []string{"id", "s"}, []string{"INT4", "INT2"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(2)}, {int64(99), int64(99)}})))
	}
	for _, tc := range []struct{ arg, message, code string }{{"bad", `invalid input syntax for type smallint: "bad"`, "22P02"}, {"32768", `value "32768" is out of range for type smallint`, "22003"}, {"", `invalid input syntax for type smallint: ""`, "22P02"}} {
		for _, prepared := range []bool{false, true} {
			s := fixture.ImplicitError("SELECT s & $1 FROM dest WHERE id=1", fixture.Diagnostic{Code: tc.code, Message: tc.message}, tc.arg)
			s.SelectRows = true
			s.Prepared = prepared
			s.ErrorStage = "execute"
			pg := *s.Diagnostic
			pg.Context = "unnamed portal parameter $1 = '...'"
			if tc.arg == "" {
				pg.Context = "unnamed portal parameter $1 = ''"
			}
			s.Diagnostic = &pg
			p.Step(t, s)
		}
	}
	canceled, cancel := context.WithCancel(p.Ctx)
	cancel()
	for _, backend := range p.Backends() {
		result, err := backend.DB.ExecContext(canceled, "INSERT INTO dest VALUES(4,1::smallint<<2)")
		if result != nil || !errors.Is(err, context.Canceled) {
			t.Fatal(result, err)
		}
	}
	p.Step(t, fixture.Exec("INSERT INTO dest VALUES(4,1::smallint<<2)", 1))
}
