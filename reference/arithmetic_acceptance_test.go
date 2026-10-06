//go:build reference

package reference_test

import "testing"

func TestReferenceArithmeticRemainingAcceptance(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE t(id INT,i INT,b BIGINT)", 0))
	p.step(t, referenceExec("INSERT INTO t VALUES(1,2147483647,9223372036854775807),(2,NULL,NULL)", 2))
	for _, expr := range []string{"i+1", "i*2", "b+1", "b*2"} {
		message := "integer out of range"
		if expr[0] == 'b' {
			message = "bigint out of range"
		}
		step := referenceImplicitError("SELECT "+expr+" FROM t WHERE id=1", referenceDiagnostic{Code: "22003", Message: message})
		step.selectRows = true
		p.step(t, step)
		p.step(t, referencePrepared(step))
	}
	for _, tc := range []struct {
		expr, typ string
		value     any
	}{{"7% -2", "INT4", int64(1)}, {"-7% -2", "INT4", int64(-1)}, {"9007199254740993*2", "INT8", int64(18014398509481986)}, {"18014398509481986/2", "INT8", int64(9007199254740993)}, {"+0x8000000000000000", "NUMERIC", "9223372036854775808"}} {
		step := referenceSelect("SELECT "+tc.expr+" FROM t WHERE id=1", []string{"?column?"}, []string{tc.typ}, [][]any{{tc.value}})
		p.step(t, step)
		p.step(t, referencePrepared(step))
	}
	reverse := referenceSelect("SELECT $1+2147483648,$1+7 FROM t WHERE id=1", []string{"?column?", "?column?"}, []string{"INT8", "INT8"}, [][]any{{int64(2147483649), int64(8)}}, 1)
	p.step(t, reverse)
	p.step(t, referencePrepared(reverse))
	p.step(t, referenceExec("CREATE TABLE d(i INT,b BIGINT)", 0))
	p.step(t, referencePrepared(referenceExec("INSERT INTO d VALUES($1+7,$2+2147483648)", 1, 1, 1)))
	p.step(t, referenceSelect("SELECT i,b FROM d", []string{"i", "b"}, []string{"INT4", "INT8"}, [][]any{{int64(8), int64(2147483649)}}))
	for _, q := range []string{"INSERT INTO d VALUES(1,2147483647+1)", "INSERT INTO d VALUES(2147483647+2147483648,1)"} {
		step := referenceImplicitError(q, referenceDiagnostic{Code: "22003", Message: "integer out of range"})
		p.step(t, step)
		p.step(t, referencePrepared(step))
	}
	p.step(t, referenceExec("CREATE TABLE f(r REAL,d DOUBLE PRECISION)", 0))
	p.step(t, referenceExec("INSERT INTO f VALUES(7,7)", 1))
	for _, tc := range []struct{ expr, typ string }{{"r%r", "real"}, {"d%d", "double precision"}} {
		q := "SELECT " + tc.expr + " FROM f"
		pg := referenceImplicitError(q, referenceDiagnostic{Code: "42883", Message: "operator does not exist: " + tc.typ + " % " + tc.typ, Position: "9", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."})
		pg.selectRows = true
		native := referenceImplicitError(q, referenceDiagnostic{Code: "0A000", Message: "arithmetic for " + tc.typ + " operands is unsupported", Position: "9"})
		native.selectRows = true
		referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, pg)
		pg.errorStage = "prepare"
		referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, referencePrepared(pg))
		referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, native)
		native.errorStage = "prepare"
		referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referencePrepared(native))
	}
	p.step(t, referenceExec("CREATE TABLE flags(f BOOL)", 0))
	step := referenceImplicitError("INSERT INTO flags VALUES(+0x8000000000000000)", referenceDiagnostic{Code: "42804", Message: `column "f" is of type boolean but expression is of type numeric`, Position: "26", Hint: "You will need to rewrite or cast the expression."})
	p.step(t, step)
	step.errorStage = "prepare"
	p.step(t, referencePrepared(step))
}

func TestReferenceArithmeticPromotedRadixInput(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE n(v NUMERIC)", 0))
	p.step(t, referenceExec("INSERT INTO n VALUES(+0x8000000000000000)", 1))
	p.step(t, referencePrepared(referenceExec("INSERT INTO n VALUES(+0x8000000000000000)", 1)))
	p.step(t, referenceSelect("SELECT v FROM n ORDER BY v", []string{"v"}, []string{"NUMERIC"}, [][]any{{"9223372036854775808"}, {"9223372036854775808"}}))
	// Unary plus preserves the native former signed-input position. PostgreSQL
	// converts the unsigned numeric constant at its own token before the operator.
	step := referenceImplicitError("SELECT +1e131072 FROM n", referenceDiagnostic{Code: "22003", Message: "value overflows numeric format", Position: "8"})
	step.selectRows = true
	pg := *step.diagnostic
	pg.Position = "9"
	step.postgresDiagnostic = &pg
	p.step(t, step)
	step.errorStage = "prepare"
	p.step(t, referencePrepared(step))
}

func TestReferenceArithmeticSafeEndpoints(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE endpoints(id INT,i INT,b BIGINT)", 0))
	p.step(t, referenceExec("INSERT INTO endpoints VALUES(1,2147483647,9223372036854775807),(2,-2147483648,-9223372036854775808)", 2))
	for _, tc := range []struct {
		expression, where, typ string
		value                  int64
	}{
		{"i+0", "id=1", "INT4", 2147483647},
		{"b+0", "id=1", "INT8", 9223372036854775807},
		{"i-0", "id=2", "INT4", -2147483648},
		{"b-0", "id=2", "INT8", -9223372036854775808},
		{"i*1", "id=2", "INT4", -2147483648},
		{"b*1", "id=2", "INT8", -9223372036854775808},
		{"i/1", "id=1", "INT4", 2147483647},
		{"b/1", "id=1", "INT8", 9223372036854775807},
	} {
		step := referenceSelect("SELECT "+tc.expression+" FROM endpoints WHERE "+tc.where, []string{"?column?"}, []string{tc.typ}, [][]any{{tc.value}})
		p.step(t, step)
		p.step(t, referencePrepared(step))
	}
}
