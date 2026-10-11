package reference_test

import (
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func nullCountReferenceSetup(t *testing.T) *fixture.Pair {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE t(id INT,v INT,b BIGINT,s TEXT,f BOOL)", 0))
	p.Step(t, fixture.Exec("INSERT INTO t VALUES(1,1,2147483648,'x',TRUE),(2,NULL,2,'',NULL),(3,NULL,NULL,NULL,NULL),(4,1,2147483648,'x',TRUE)", 4))
	return p
}

func nullCountReferenceSelect(q string, want [][]any, args ...any) fixture.Step {
	return fixture.Select(q, []string{"num_nulls", "num_nonnulls"}, []string{"INT4", "INT4"}, want, args...)
}

func TestReferenceNullCountLiterals(t *testing.T) {
	p := nullCountReferenceSetup(t)
	p.Step(t, fixture.Exec("CREATE TABLE first(id INT)", 0))
	p.Step(t, fixture.Exec("INSERT INTO first VALUES(1)", 1))
	nullCountReferenceStep(t, p, nullCountReferenceSelect("SELECT num_nulls(1,NULL,2),num_nonnulls(1,NULL,2) FROM first", [][]any{{int64(1), int64(2)}}), 3)
	for _, tc := range []struct {
		args        string
		a, b, arity int64
	}{
		{"NULL", 1, 0, 1}, {"1,NULL,2", 1, 2, 3}, {"NULL,NULL,NULL", 3, 0, 3},
		{"1,'',FALSE,0,2147483648", 0, 5, 5}, {"NULL,1,NULL,'x',NULL", 3, 2, 5},
		{strings.TrimSuffix(strings.Repeat("NULL,", 100), ","), 100, 0, 100},
		{strings.TrimSuffix(strings.Repeat("1,", 100), ","), 0, 100, 100},
	} {
		if tc.a+tc.b != tc.arity {
			t.Fatal("invalid independent fixture")
		}
		q := "SELECT num_nulls(" + tc.args + "),num_nonnulls(" + tc.args + ")"
		step := nullCountReferenceSelect(q, [][]any{{tc.a, tc.b}})
		nullCountReferenceStep(t, p, step, tc.arity)
		nullCountReferenceStep(t, p, fixture.Prepared(step), tc.arity)
		step = nullCountReferenceSelect(q+" FROM t ORDER BY id", [][]any{{tc.a, tc.b}, {tc.a, tc.b}, {tc.a, tc.b}, {tc.a, tc.b}})
		nullCountReferenceStep(t, p, step, tc.arity)
		nullCountReferenceStep(t, p, fixture.Prepared(step), tc.arity)
	}
	for _, step := range []fixture.Step{
		nullCountReferenceSelect("SELECT num_nulls(NULL),num_nonnulls(1,NULL,'x')", [][]any{{int64(1), int64(2)}}),
		nullCountReferenceSelect("SELECT num_nulls(num_nulls(NULL),NULL),num_nonnulls(num_nonnulls(NULL),NULL)", [][]any{{int64(1), int64(1)}}),
		fixture.Exec("SELECT num_nulls(NULL),num_nonnulls(1,NULL,'x')", 1),
	} {
		arity := int64(2)
		if step.Query == "SELECT num_nulls(NULL),num_nonnulls(1,NULL,'x')" {
			arity = 3
		}
		nullCountReferenceStep(t, p, step, arity)
		nullCountReferenceStep(t, p, fixture.Prepared(step), arity)
	}
}

func TestReferenceNullCountColumns(t *testing.T) {
	p := nullCountReferenceSetup(t)
	want := [][]any{{int64(0), int64(4)}, {int64(2), int64(2)}, {int64(4), int64(0)}, {int64(0), int64(4)}}
	q := "SELECT num_nulls(v,b,s,f),num_nonnulls(v,b,s,f) FROM t"
	step := nullCountReferenceSelect(q+" ORDER BY id", want)
	nullCountReferenceStep(t, p, step, 4)
	nullCountReferenceStep(t, p, fixture.Prepared(step), 4)
	step = nullCountReferenceSelect(q, want)
	step.Bag = true
	nullCountReferenceStep(t, p, step, 4)
	nullCountReferenceStep(t, p, fixture.Prepared(step), 4)
	step = nullCountReferenceSelect("SELECT num_nulls(NULL),num_nonnulls(NULL) FROM t ORDER BY id", [][]any{{int64(1), int64(0)}, {int64(1), int64(0)}, {int64(1), int64(0)}, {int64(1), int64(0)}})
	nullCountReferenceStep(t, p, step, 1)
	nullCountReferenceStep(t, p, fixture.Prepared(step), 1)
	p.Step(t, fixture.Exec("CREATE TABLE empty(v INT)", 0))
	step = nullCountReferenceSelect("SELECT num_nulls(v),num_nonnulls(v) FROM empty", nil)
	nullCountReferenceStep(t, p, step, 1)
	nullCountReferenceStep(t, p, fixture.Prepared(step), 1)
	p.Step(t, fixture.Exec("CREATE TABLE baseline(id INT,n NUMERIC,r REAL,d DOUBLE PRECISION,tm TIME,ts TIMESTAMP,u UUID)", 0))
	p.Step(t, fixture.Exec("INSERT INTO baseline VALUES(1,1.5,1.5,2.5,'12:34:56','2000-01-02 03:04:05','00000000-0000-0000-0000-000000000001'),(2,NULL,NULL,NULL,NULL,NULL,NULL)", 2))
	step = nullCountReferenceSelect("SELECT num_nulls(n,r,d,tm,ts,u),num_nonnulls(n,r,d,tm,ts,u) FROM baseline ORDER BY id", [][]any{{int64(0), int64(6)}, {int64(6), int64(0)}})
	nullCountReferenceStep(t, p, step, 6)
	nullCountReferenceStep(t, p, fixture.Prepared(step), 6)
}

// INSERT VALUES retains its original composed-function exclusion on native;
// no-FROM SELECT admission does not widen this separate consumer.
func TestReferenceNullCountInsertConsumerRoutes(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE t(id INT,v INT,b BIGINT,s TEXT,f BOOL)", 0))
	p.Step(t, fixture.Exec("INSERT INTO t VALUES(1,1,2147483648,'x',TRUE),(2,NULL,2,'',NULL),(3,NULL,NULL,NULL,NULL)", 3))
	const q = "INSERT INTO t VALUES(4,num_nulls(NULL),1,'x',TRUE)"
	nullCountNativeExcluded(t, p, q)
	fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, fixture.Select("SELECT id FROM t WHERE id=4", []string{"id"}, []string{"INT4"}, nil))
	if p.Postgres != nil {
		for _, prepared := range []bool{false, true} {
			step := fixture.Select(q, []string{}, []string{}, nil)
			step.Prepared = prepared
			fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, step)
			fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Select("SELECT id,v,b,s,f FROM t WHERE id=4", []string{"id", "v", "b", "s", "f"}, []string{"INT4", "INT4", "INT8", "TEXT", "BOOL"}, [][]any{{int64(4), int64(1), int64(1), "x", true}}))
			fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Exec("DELETE FROM t WHERE id=4", 1))
		}
	}
}

func nullCountNativeExcluded(t *testing.T, p *fixture.Pair, q string) {
	t.Helper()
	rows, err := p.Memory.QueryContext(p.Ctx, q)
	if rows != nil {
		rows.Close()
		t.Fatal("excluded query exposed cursor", q)
	}
	if err == nil {
		t.Fatal("excluded syntax accepted", q)
	}
	stmt, err := p.Memory.PrepareContext(p.Ctx, q)
	if stmt != nil {
		stmt.Close()
		t.Fatal("excluded prepare exposed handle", q)
	}
	if err == nil {
		t.Fatal("excluded prepare accepted", q)
	}
}

func TestReferenceNullCountHeldPrepared(t *testing.T) {
	p := nullCountReferenceSetup(t)
	for _, backend := range p.Backends() {
		const q = "SELECT num_nulls(v,b,s,f),num_nonnulls(v,b,s,f) FROM t WHERE id=$1"
		stmt, err := backend.DB.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		func() {
			defer stmt.Close()
			held := fixture.HeldStatement{backend.DB, stmt, q}
			for _, id := range []int{1, 2, 3, 1, 99} {
				want := [][]any{{int64(0), int64(4)}}
				if id == 2 {
					want = [][]any{{int64(2), int64(2)}}
				}
				if id == 3 {
					want = [][]any{{int64(4), int64(0)}}
				}
				if id == 99 {
					want = nil
				}
				step := nullCountReferenceSelect(q, want, id)
				rows, err := stmt.QueryContext(p.Ctx, id)
				nullCountReferenceRows(t, rows, err, backend.PG, step, 4)
				fixture.RunStep(t, p.Ctx, held, backend.Recorder, step)
			}
		}()
	}
}

func nullCountReferenceError(t *testing.T, p *fixture.Pair, q string, want fixture.Diagnostic, args ...any) {
	t.Helper()
	step := fixture.ImplicitError(q, want, args...)
	step.SelectRows = true
	step.ErrorStage = "execute"
	p.Step(t, step)
	step.ErrorStage = "prepare"
	p.Step(t, fixture.Prepared(step))
}

func TestReferenceNullCountDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE t(id INT,v INT)", 0))
	const hint = "No function matches the given name and argument types. You might need to add explicit type casts."
	// Complete logical records are source-derived PG18 expectations; C locations
	// are excluded by the established fixture, not fabricated on native errors.
	for _, tc := range []struct {
		q    string
		want fixture.Diagnostic
	}{
		{"SELECT num_nulls()", fixture.Diagnostic{Code: "42883", Message: "function num_nulls() does not exist", Position: "8", Hint: hint}},
		{"SELECT num_nonnulls()", fixture.Diagnostic{Code: "42883", Message: "function num_nonnulls() does not exist", Position: "8", Hint: hint}},
		{"SELECT /*é*/ num_nulls()", fixture.Diagnostic{Code: "42883", Message: "function num_nulls() does not exist", Position: "14", Hint: hint}},
		{"SELECT num_nulls() FROM t", fixture.Diagnostic{Code: "42883", Message: "function num_nulls() does not exist", Position: "8", Hint: hint}},
		{"SELECT num_nulls(VARIADIC 1)", fixture.Diagnostic{Code: "42804", Message: "VARIADIC argument must be an array", Position: "27"}},
		{"SELECT num_nonnulls(VARIADIC 1)", fixture.Diagnostic{Code: "42804", Message: "VARIADIC argument must be an array", Position: "30"}},
		{"SELECT num_nulls(VARIADIC NULL)", fixture.Diagnostic{Code: "42804", Message: "VARIADIC argument must be an array", Position: "27"}},
		{"SELECT num_nulls(VARIADIC 'x')", fixture.Diagnostic{Code: "42804", Message: "VARIADIC argument must be an array", Position: "27"}},
		{"SELECT num_nulls(1,VARIADIC 2)", fixture.Diagnostic{Code: "42883", Message: "function num_nulls(integer, integer) does not exist", Position: "8", Hint: hint}},
		{"SELECT num_nulls($0)", fixture.Diagnostic{Code: "42P02", Message: "there is no parameter $0", Position: "18"}},
		{"SELECT num_nulls(1,)", fixture.Diagnostic{Code: "42601", Message: `syntax error at or near ")"`, Position: "20"}},
		{"SELECT num_nulls(", fixture.Diagnostic{Code: "42601", Message: "syntax error at end of input", Position: "18"}},
		{"SELECT num_nulls(" + strings.TrimSuffix(strings.Repeat("NULL,", 101), ",") + ")", fixture.Diagnostic{Code: "54023", Message: "cannot pass more than 100 arguments to a function", Position: "8"}},
		{"SELECT num_nulls(missing) FROM t", fixture.Diagnostic{Code: "42703", Message: `column "missing" does not exist`, Position: "18"}},
	} {
		nullCountReferenceError(t, p, tc.q, tc.want)
	}
	const q = "SELECT id FROM t WHERE num_nulls(v)"
	nullCountNativeExcluded(t, p, q)
	if p.Postgres != nil {
		step := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42804", Message: "argument of WHERE must be type boolean, not type integer", Position: "24"})
		step.SelectRows, step.ErrorStage = true, "execute"
		fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, step)
		step.ErrorStage = "prepare"
		fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Prepared(step))
	}
}

func TestReferenceNullCountArgumentErrorPrecedence(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE t(v INT)", 0))
	const hint = "No function matches the given name and argument types. You might need to add explicit type casts."
	tail := strings.TrimSuffix(strings.Repeat("NULL,", 100), ",")
	// Independently specified from PG18 child transformation before function
	// resolution; root observes these combined-error records on the pinned oracle.
	for _, name := range []string{"num_nulls", "num_nonnulls"} {
		position, lastPosition := "18", "518"
		if name == "num_nonnulls" {
			position, lastPosition = "21", "521"
		}
		for _, tc := range []struct {
			operands string
			want     fixture.Diagnostic
			args     []any
		}{
			{"$0," + tail, fixture.Diagnostic{Code: "42P02", Message: "there is no parameter $0", Position: position}, nil},
			{strings.Repeat("NULL,", 100) + "$0", fixture.Diagnostic{Code: "42P02", Message: "there is no parameter $0", Position: lastPosition}, nil},
			{"missing," + tail, fixture.Diagnostic{Code: "42703", Message: `column "missing" does not exist`, Position: position}, nil},
			{"num_nulls()," + tail, fixture.Diagnostic{Code: "42883", Message: "function num_nulls() does not exist", Position: position, Hint: hint}, nil},
			{"$1," + tail, fixture.Diagnostic{Code: "54023", Message: "cannot pass more than 100 arguments to a function", Position: "8"}, []any{1}},
		} {
			nullCountReferenceError(t, p, "SELECT "+name+"("+tc.operands+") FROM t", tc.want, tc.args...)
		}
	}
}

func TestReferenceNullCountParameters(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE t(v INT,s TEXT)", 0))
	for _, name := range []string{"num_nulls", "num_nonnulls"} {
		q := "SELECT " + name + "($1)"
		want := fixture.Diagnostic{Code: "42P18", Message: "could not determine data type of parameter $1"}
		for _, arg := range []any{nil, 1, "x"} {
			nullCountReferenceError(t, p, q, want, arg)
		}
		// pq zero-argument Query uses simple protocol. Assert both full records
		// independently rather than normalizing their code/message/position.
		native := fixture.ImplicitError(q, want)
		native.SelectRows = true
		native.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, native)
		pos := "18"
		if name == "num_nonnulls" {
			pos = "21"
		}
		simple := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42P02", Message: "there is no parameter $1", Position: pos})
		simple.SelectRows = true
		simple.ErrorStage = "execute"
		if p.Postgres != nil {
			fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, simple)
		}
		native.ErrorStage = "prepare"
		p.Step(t, fixture.Prepared(native))
	}
	for _, tc := range []struct{ q, pos string }{
		{"SELECT num_nulls($1),$1 FROM t", "18"},
		{"SELECT num_nulls($1) FROM t WHERE v=$1", "18"},
		{"SELECT $1,num_nulls($1) FROM t", "21"},
	} {
		nullCountReferenceError(t, p, tc.q, fixture.Diagnostic{Code: "42P08", Message: "could not determine data type of parameter $1", Position: tc.pos}, 1)
	}
	nullCountReferenceError(t, p, "SELECT $1 FROM t WHERE v=$1", fixture.Diagnostic{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "integer versus text", Position: "8"}, 1)
}
