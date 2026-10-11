package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceArithmeticRemainingAcceptance(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE t(id INT,i INT,b BIGINT)", 0))
	p.Step(t, fixture.Exec("INSERT INTO t VALUES(1,2147483647,9223372036854775807),(2,NULL,NULL)", 2))
	for _, expr := range []string{"i+1", "i*2", "b+1", "b*2"} {
		message := "integer out of range"
		if expr[0] == 'b' {
			message = "bigint out of range"
		}
		step := fixture.ImplicitError("SELECT "+expr+" FROM t WHERE id=1", fixture.Diagnostic{Code: "22003", Message: message})
		step.SelectRows = true
		step.ErrorStage = "execute"
		p.Step(t, step)
		p.Step(t, fixture.Prepared(step))
	}
	for _, tc := range []struct {
		expr, typ string
		value     any
	}{{"7% -2", "INT4", int64(1)}, {"-7% -2", "INT4", int64(-1)}, {"9007199254740993*2", "INT8", int64(18014398509481986)}, {"18014398509481986/2", "INT8", int64(9007199254740993)}, {"+0x8000000000000000", "NUMERIC", "9223372036854775808"}} {
		step := fixture.Select("SELECT "+tc.expr+" FROM t WHERE id=1", []string{"?column?"}, []string{tc.typ}, [][]any{{tc.value}})
		p.Step(t, step)
		p.Step(t, fixture.Prepared(step))
	}
	p.Step(t, fixture.Exec("CREATE TABLE one(i INT)", 0))
	p.Step(t, fixture.Exec("INSERT INTO one VALUES(1)", 1))
	exact := fixture.Select("SELECT 9007199254740993*2,18014398509481986/2 FROM one", []string{"?column?", "?column?"}, []string{"INT8", "INT8"}, [][]any{{int64(18014398509481986), int64(9007199254740993)}})
	p.Step(t, exact)
	p.Step(t, fixture.Prepared(exact))
	reverse := fixture.Select("SELECT $1+2147483648,$1+7 FROM t WHERE id=1", []string{"?column?", "?column?"}, []string{"INT8", "INT8"}, [][]any{{int64(2147483649), int64(8)}}, 1)
	p.Step(t, reverse)
	p.Step(t, fixture.Prepared(reverse))
	p.Step(t, fixture.Exec("CREATE TABLE d(i INT,b BIGINT)", 0))
	p.Step(t, fixture.Prepared(fixture.Exec("INSERT INTO d VALUES($1+7,$2+2147483648)", 1, 1, 1)))
	p.Step(t, fixture.Select("SELECT i,b FROM d", []string{"i", "b"}, []string{"INT4", "INT8"}, [][]any{{int64(8), int64(2147483649)}}))
	for _, q := range []string{"INSERT INTO d VALUES(1,2147483647+1)", "INSERT INTO d VALUES(2147483647+2147483648,1)"} {
		step := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22003", Message: "integer out of range"})
		step.ErrorStage = "execute"
		p.Step(t, step)
		p.Step(t, fixture.Prepared(step))
	}
	p.Step(t, fixture.Exec("CREATE TABLE f(r REAL,d DOUBLE PRECISION)", 0))
	p.Step(t, fixture.Exec("INSERT INTO f VALUES(7,7)", 1))
	for _, tc := range []struct{ expr, typ string }{{"r%r", "real"}, {"d%d", "double precision"}} {
		q := "SELECT " + tc.expr + " FROM f"
		step := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42883", Message: "operator does not exist: " + tc.typ + " % " + tc.typ, Position: "9", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."})
		step.SelectRows, step.ErrorStage = true, "execute"
		p.Step(t, step)
		step.ErrorStage = "prepare"
		p.Step(t, fixture.Prepared(step))
	}
	p.Step(t, fixture.Exec("CREATE TABLE flags(f BOOL)", 0))
	step := fixture.ImplicitError("INSERT INTO flags VALUES(+0x8000000000000000)", fixture.Diagnostic{Code: "42804", Message: `column "f" is of type boolean but expression is of type numeric`, Position: "26", Hint: "You will need to rewrite or cast the expression."})
	step.ErrorStage = "execute"
	p.Step(t, step)
	step.ErrorStage = "prepare"
	p.Step(t, fixture.Prepared(step))
}

func TestReferenceArithmeticPromotedRadixInput(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE n(v NUMERIC)", 0))
	p.Step(t, fixture.Exec("INSERT INTO n VALUES(+0x8000000000000000)", 1))
	p.Step(t, fixture.Prepared(fixture.Exec("INSERT INTO n VALUES(+0x8000000000000000)", 1)))
	p.Step(t, fixture.Select("SELECT v FROM n ORDER BY v", []string{"v"}, []string{"NUMERIC"}, [][]any{{"9223372036854775808"}, {"9223372036854775808"}}))
	p.Step(t, fixture.Prepared(fixture.Select("SELECT v FROM n ORDER BY v", []string{"v"}, []string{"NUMERIC"}, [][]any{{"9223372036854775808"}, {"9223372036854775808"}})))
	p.Step(t, fixture.Exec("CREATE TABLE d(f BOOL)", 0))
	bad := fixture.ImplicitError("INSERT INTO d VALUES(+0x8000000000000000)", fixture.Diagnostic{Code: "42804", Message: `column "f" is of type boolean but expression is of type numeric`, Hint: "You will need to rewrite or cast the expression.", Position: "22"})
	bad.ErrorStage = "execute"
	referenceArithmeticFailure(t, p, bad)
	bad.ErrorStage = "prepare"
	referenceArithmeticFailure(t, p, fixture.Prepared(bad))
	// Numeric unary plus is identity: overflow belongs to the operand token.
	step := fixture.ImplicitError("SELECT +1e131072 FROM n", fixture.Diagnostic{Code: "22003", Message: "value overflows numeric format", Position: "9"})
	step.SelectRows = true
	step.ErrorStage = "execute"
	p.Step(t, step)
	step.ErrorStage = "prepare"
	p.Step(t, fixture.Prepared(step))
}

func TestReferenceArithmeticSafeEndpoints(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE endpoints(id INT,i INT,b BIGINT)", 0))
	p.Step(t, fixture.Exec("INSERT INTO endpoints VALUES(1,2147483647,9223372036854775807),(2,-2147483648,-9223372036854775808)", 2))
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
		{"i/(b/b*-1)", "id=2", "INT8", 2147483648},
		{"9007199254740993*1", "id=1", "INT8", 9007199254740993},
		{"9007199254740993/1", "id=1", "INT8", 9007199254740993},
	} {
		step := fixture.Select("SELECT "+tc.expression+" FROM endpoints WHERE "+tc.where, []string{"?column?"}, []string{tc.typ}, [][]any{{tc.value}})
		p.Step(t, step)
		p.Step(t, fixture.Prepared(step))
	}
}
