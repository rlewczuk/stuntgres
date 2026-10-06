//go:build reference

package reference_test

import (
	"database/sql"
	"reflect"
	"strings"
	"testing"
)

func nullCountReferenceSetup(t *testing.T) *referencePair {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE t(id INT,v INT,b BIGINT,s TEXT,f BOOL)", 0))
	p.step(t, referenceExec("INSERT INTO t VALUES(1,1,2147483648,'x',TRUE),(2,NULL,2,'',NULL),(3,NULL,NULL,NULL,NULL),(4,1,2147483648,'x',TRUE)", 4))
	return p
}

func nullCountReferenceSelect(q string, want [][]any, args ...any) referenceStep {
	return referenceSelect(q, []string{"num_nulls", "num_nonnulls"}, []string{"INT4", "INT4"}, want, args...)
}

func TestReferenceNullCountLiterals(t *testing.T) {
	p := nullCountReferenceSetup(t)
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
		p.step(t, step)
		p.step(t, referencePrepared(step))
		step = nullCountReferenceSelect(q+" FROM t ORDER BY id", [][]any{{tc.a, tc.b}, {tc.a, tc.b}, {tc.a, tc.b}, {tc.a, tc.b}})
		p.step(t, step)
		p.step(t, referencePrepared(step))
	}
	for _, step := range []referenceStep{
		nullCountReferenceSelect("SELECT num_nulls(NULL),num_nonnulls(1,NULL,'x')", [][]any{{int64(1), int64(2)}}),
		nullCountReferenceSelect("SELECT num_nulls(num_nulls(NULL),NULL),num_nonnulls(num_nonnulls(NULL),NULL)", [][]any{{int64(1), int64(1)}}),
		referenceExec("SELECT num_nulls(NULL),num_nonnulls(1,NULL,'x')", 1),
	} {
		p.step(t, step)
		p.step(t, referencePrepared(step))
	}
}

func TestReferenceNullCountColumns(t *testing.T) {
	p := nullCountReferenceSetup(t)
	want := [][]any{{int64(0), int64(4)}, {int64(2), int64(2)}, {int64(4), int64(0)}, {int64(0), int64(4)}}
	q := "SELECT num_nulls(v,b,s,f),num_nonnulls(v,b,s,f) FROM t"
	step := nullCountReferenceSelect(q+" ORDER BY id", want)
	p.step(t, step)
	p.step(t, referencePrepared(step))
	step = nullCountReferenceSelect(q, want)
	step.bag = true
	p.step(t, step)
	p.step(t, referencePrepared(step))
	p.step(t, referenceExec("CREATE TABLE empty(v INT)", 0))
	step = nullCountReferenceSelect("SELECT num_nulls(v),num_nonnulls(v) FROM empty", nil)
	p.step(t, step)
	p.step(t, referencePrepared(step))
	p.step(t, referenceExec("CREATE TABLE baseline(id INT,n NUMERIC,r REAL,d DOUBLE PRECISION,tm TIME,ts TIMESTAMP,u UUID)", 0))
	p.step(t, referenceExec("INSERT INTO baseline VALUES(1,1.5,1.5,2.5,'12:34:56','2000-01-02 03:04:05','00000000-0000-0000-0000-000000000001'),(2,NULL,NULL,NULL,NULL,NULL,NULL)", 2))
	step = nullCountReferenceSelect("SELECT num_nulls(n,r,d,tm,ts,u),num_nonnulls(n,r,d,tm,ts,u) FROM baseline ORDER BY id", [][]any{{int64(0), int64(6)}, {int64(6), int64(0)}})
	p.step(t, step)
	p.step(t, referencePrepared(step))
}

func TestReferenceNullCountHeldPrepared(t *testing.T) {
	p := nullCountReferenceSetup(t)
	var observed [][][][]any
	for _, db := range []*sql.DB{p.memory, p.postgres} {
		stmt, err := db.PrepareContext(p.ctx, "SELECT num_nulls(v,b,s,f),num_nonnulls(v,b,s,f) FROM t WHERE id=$1")
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		executions := make([][][]any, 0)
		for _, id := range []int{1, 2, 3, 1, 99} {
			rows, err := stmt.QueryContext(p.ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			names, err := rows.Columns()
			if err != nil || !reflect.DeepEqual(names, []string{"num_nulls", "num_nonnulls"}) {
				t.Fatal(names, err)
			}
			types, err := rows.ColumnTypes()
			if err != nil || len(types) != 2 {
				t.Fatal(types, err)
			}
			for _, typ := range types {
				if typ.DatabaseTypeName() != "INT4" {
					t.Fatal(typ)
				}
			}
			got := make([][]any, 0)
			for rows.Next() {
				var a, b any
				if err := rows.Scan(&a, &b); err != nil {
					t.Fatal(err)
				}
				x, xok := a.(int64)
				y, yok := b.(int64)
				if !xok || !yok || x+y != 4 {
					t.Fatal("nonnull int4/sum", a, b)
				}
				got = append(got, []any{a, b})
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			rows.Close()
			want := [][]any{{int64(0), int64(4)}}
			if id == 2 {
				want = [][]any{{int64(2), int64(2)}}
			}
			if id == 3 {
				want = [][]any{{int64(4), int64(0)}}
			}
			if id == 99 {
				want = [][]any{}
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatal("independent held execution", id, got, want)
			}
			executions = append(executions, got)
		}
		observed = append(observed, executions)
	}
	if !reflect.DeepEqual(observed[0], observed[1]) {
		t.Fatal("held backend parity", observed)
	}
}

func nullCountReferenceError(t *testing.T, p *referencePair, q string, want referenceDiagnostic, args ...any) {
	t.Helper()
	step := referenceImplicitError(q, want, args...)
	step.selectRows = true
	step.errorStage = "execute"
	p.step(t, step)
	step.errorStage = "prepare"
	p.step(t, referencePrepared(step))
}

func TestReferenceNullCountDiagnostics(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE t(v INT)", 0))
	const hint = "No function matches the given name and argument types. You might need to add explicit type casts."
	// Complete logical records are source-derived PG18 expectations; C locations
	// are excluded by the established fixture, not fabricated on native errors.
	for _, tc := range []struct {
		q    string
		want referenceDiagnostic
	}{
		{"SELECT num_nulls()", referenceDiagnostic{Code: "42883", Message: "function num_nulls() does not exist", Position: "8", Hint: hint}},
		{"SELECT num_nonnulls()", referenceDiagnostic{Code: "42883", Message: "function num_nonnulls() does not exist", Position: "8", Hint: hint}},
		{"SELECT /*é*/ num_nulls()", referenceDiagnostic{Code: "42883", Message: "function num_nulls() does not exist", Position: "14", Hint: hint}},
		{"SELECT num_nulls() FROM t", referenceDiagnostic{Code: "42883", Message: "function num_nulls() does not exist", Position: "8", Hint: hint}},
		{"SELECT num_nulls(VARIADIC 1)", referenceDiagnostic{Code: "42804", Message: "VARIADIC argument must be an array", Position: "27"}},
		{"SELECT num_nonnulls(VARIADIC 1)", referenceDiagnostic{Code: "42804", Message: "VARIADIC argument must be an array", Position: "30"}},
		{"SELECT num_nulls(VARIADIC NULL)", referenceDiagnostic{Code: "42804", Message: "VARIADIC argument must be an array", Position: "27"}},
		{"SELECT num_nulls(VARIADIC 'x')", referenceDiagnostic{Code: "42804", Message: "VARIADIC argument must be an array", Position: "27"}},
		{"SELECT num_nulls(1,VARIADIC 2)", referenceDiagnostic{Code: "42883", Message: "function num_nulls(integer, integer) does not exist", Position: "8", Hint: hint}},
		{"SELECT num_nulls($0)", referenceDiagnostic{Code: "42P02", Message: "there is no parameter $0", Position: "18"}},
		{"SELECT num_nulls(1,)", referenceDiagnostic{Code: "42601", Message: `syntax error at or near ")"`, Position: "20"}},
		{"SELECT num_nulls(", referenceDiagnostic{Code: "42601", Message: "syntax error at end of input", Position: "18"}},
		{"SELECT num_nulls(" + strings.TrimSuffix(strings.Repeat("NULL,", 101), ",") + ")", referenceDiagnostic{Code: "54023", Message: "cannot pass more than 100 arguments to a function", Position: "8"}},
		{"SELECT num_nulls(missing) FROM t", referenceDiagnostic{Code: "42703", Message: `column "missing" does not exist`, Position: "18"}},
	} {
		nullCountReferenceError(t, p, tc.q, tc.want)
	}
}

func TestReferenceNullCountArgumentErrorPrecedence(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE t(v INT)", 0))
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
			want     referenceDiagnostic
			args     []any
		}{
			{"$0," + tail, referenceDiagnostic{Code: "42P02", Message: "there is no parameter $0", Position: position}, nil},
			{strings.Repeat("NULL,", 100) + "$0", referenceDiagnostic{Code: "42P02", Message: "there is no parameter $0", Position: lastPosition}, nil},
			{"missing," + tail, referenceDiagnostic{Code: "42703", Message: `column "missing" does not exist`, Position: position}, nil},
			{"num_nulls()," + tail, referenceDiagnostic{Code: "42883", Message: "function num_nulls() does not exist", Position: position, Hint: hint}, nil},
			{"$1," + tail, referenceDiagnostic{Code: "54023", Message: "cannot pass more than 100 arguments to a function", Position: "8"}, []any{1}},
		} {
			nullCountReferenceError(t, p, "SELECT "+name+"("+tc.operands+") FROM t", tc.want, tc.args...)
		}
	}
}

func TestReferenceNullCountParameters(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE t(v INT,s TEXT)", 0))
	for _, name := range []string{"num_nulls", "num_nonnulls"} {
		q := "SELECT " + name + "($1)"
		want := referenceDiagnostic{Code: "42P18", Message: "could not determine data type of parameter $1"}
		for _, arg := range []any{nil, 1, "x"} {
			nullCountReferenceError(t, p, q, want, arg)
		}
		// pq zero-argument Query uses simple protocol. Assert both full records
		// independently rather than normalizing their code/message/position.
		native := referenceImplicitError(q, want)
		native.selectRows = true
		native.errorStage = "execute"
		referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, native)
		pos := "18"
		if name == "num_nonnulls" {
			pos = "21"
		}
		simple := referenceImplicitError(q, referenceDiagnostic{Code: "42P02", Message: "there is no parameter $1", Position: pos})
		simple.selectRows = true
		simple.errorStage = "execute"
		referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, simple)
		native.errorStage = "prepare"
		p.step(t, referencePrepared(native))
	}
	for _, tc := range []struct{ q, pos string }{
		{"SELECT num_nulls($1),$1 FROM t", "18"},
		{"SELECT num_nulls($1) FROM t WHERE v=$1", "18"},
		{"SELECT $1,num_nulls($1) FROM t", "21"},
	} {
		nullCountReferenceError(t, p, tc.q, referenceDiagnostic{Code: "42P08", Message: "could not determine data type of parameter $1", Position: tc.pos}, 1)
	}
}
