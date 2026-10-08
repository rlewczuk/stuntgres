//go:build reference

package reference_test

import (
	"bytes"
	"math"
	"strings"
	"testing"

	"stuntgres"
)

func TestReferenceMathScalarSpecialsAndLimits(t *testing.T) {
	p := newReferencePair(t)
	for _, name := range []string{"abs", "ceil", "ceiling", "floor", "exp"} {
		for _, special := range []string{"NaN", "Infinity", "-Infinity"} {
			want := special
			if name == "abs" && special == "-Infinity" {
				want = "Infinity"
			}
			if name == "exp" && special == "-Infinity" {
				want = "0"
			}
			s := referenceSelect("SELECT "+name+"(COALESCE('"+special+"',1.0))", []string{name}, []string{"NUMERIC"}, [][]any{{want}})
			p.step(t, s)
			p.step(t, referencePrepared(s))
		}
	}
	for _, a := range []string{"NaN", "Infinity", "-Infinity", "0", "2", "-2"} {
		for _, b := range []string{"NaN", "Infinity", "-Infinity", "0", "2", "-2"} {
			q := "SELECT div(COALESCE('" + a + "',1.0),COALESCE('" + b + "',1.0))"
			if b == "0" && a != "NaN" {
				s := referenceImplicitError(q, referenceDiagnostic{Code: "22012", Message: "division by zero"})
				s.errorStage = "execute"
				p.step(t, s)
				p.step(t, referencePrepared(s))
				continue
			}
			want := "0"
			if a == "NaN" || b == "NaN" || strings.Contains(a, "Infinity") && strings.Contains(b, "Infinity") {
				want = "NaN"
			} else if strings.Contains(a, "Infinity") {
				want = "Infinity"
				if strings.HasPrefix(a, "-") != strings.HasPrefix(b, "-") {
					want = "-Infinity"
				}
			} else if !strings.Contains(b, "Infinity") && a != "0" {
				want = "1"
				if strings.HasPrefix(a, "-") != strings.HasPrefix(b, "-") {
					want = "-1"
				}
			}
			s := referenceSelect(q, []string{"div"}, []string{"NUMERIC"}, [][]any{{want}})
			p.step(t, s)
			p.step(t, referencePrepared(s))
		}
	}
	for _, tc := range []struct{ name, input string }{{"ceil", strings.Repeat("9", 131072) + ".1"}, {"ceiling", strings.Repeat("9", 131072) + ".1"}, {"floor", "-" + strings.Repeat("9", 131072) + ".1"}} {
		s := referenceImplicitError("SELECT "+tc.name+"("+tc.input+")", referenceDiagnostic{Code: "22003", Message: "value overflows numeric format"})
		s.errorStage = "execute"
		p.step(t, s)
		p.step(t, referencePrepared(s))
	}
	for _, tc := range []struct{ name, input, want string }{{"abs", "-1e131071", "1" + strings.Repeat("0", 131071)}, {"floor", strings.Repeat("9", 131072) + ".1", strings.Repeat("9", 131072)}, {"div", "1e131071,1", "1" + strings.Repeat("0", 131071)}} {
		s := referenceSelect("SELECT "+tc.name+"("+tc.input+")", []string{tc.name}, []string{"NUMERIC"}, [][]any{{tc.want}})
		p.step(t, s)
	}
}

func TestReferenceMathScalarEvaluationPhase(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE m(n NUMERIC,i INTEGER)", 0))
	s := referenceImplicitError("SELECT div(1,0) FROM m", referenceDiagnostic{Code: "22012", Message: "division by zero"})
	s.selectRows = true
	s.errorStage = "execute"
	p.step(t, s)
	p.step(t, referencePrepared(s))
	p.step(t, referenceExec("INSERT INTO m VALUES(NULL,9)", 1))
	for _, q := range []string{"SELECT div(NULL,div(i,0)) FROM m", "SELECT div(n,0) FROM m"} {
		s := referenceSelect(q, []string{"div"}, []string{"NUMERIC"}, [][]any{{nil}})
		p.step(t, s)
		p.step(t, referencePrepared(s))
	}
	s = referenceImplicitError("SELECT div(n,div(i,0)) FROM m", referenceDiagnostic{Code: "22012", Message: "division by zero"})
	s.selectRows = true
	s.errorStage = "execute"
	p.step(t, s)
	p.step(t, referencePrepared(s))
}

func TestReferenceMathScalarNativeControls(t *testing.T) {
	p := newReferencePair(t)
	for _, q := range []string{"CREATE TABLE source(n NUMERIC,d DOUBLE PRECISION)", "INSERT INTO source VALUES(-17.400,'-0.5'),(NULL,NULL)", "CREATE TABLE stored(n NUMERIC,d DOUBLE PRECISION)", "INSERT INTO stored SELECT abs(n),ceil(d) FROM source", "CREATE VIEW scalar_view AS SELECT ceil(n) AS c,ceiling(n) AS cc,floor(n) AS f,div(n,4) AS q,exp(0.0) AS e,factorial(5) AS fac FROM source"} {
		if _, err := p.native.ExecContext(p.ctx, q); err != nil {
			t.Fatal(q, err)
		}
	}
	held, err := p.native.QueryContext(p.ctx, "SELECT n,d FROM stored")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
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
	var n string
	var d float64
	if err := p.native.QueryRowContext(p.ctx, "SELECT n,d FROM stored").Scan(&n, &d); err != nil || n != "17.400" || d != 0 || !math.Signbit(d) {
		t.Fatal(n, d, err)
	}
	var c, cc, f, q, e, fac string
	if err := p.native.QueryRowContext(p.ctx, "SELECT c,cc,f,q,e,fac FROM scalar_view").Scan(&c, &cc, &f, &q, &e, &fac); err != nil || c != "-17" || cc != "-17" || f != "-18" || q != "-4" || e != "1.0000000000000000" || fac != "120" {
		t.Fatal(c, cc, f, q, e, fac, err)
	}
	if !held.Next() {
		t.Fatal(held.Err())
	}
	if err := held.Scan(&n, &d); err != nil || n != "17.400" || d != 0 || !math.Signbit(d) {
		t.Fatal(n, d, err)
	}
	ts, err := held.ColumnTypes()
	if err != nil || ts[0].DatabaseTypeName() != "NUMERIC" || ts[1].DatabaseTypeName() != "FLOAT8" {
		t.Fatal(ts, err)
	}
	var redump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &redump); err != nil || redump.String() != dump.String() {
		t.Fatal("deterministic replay", err)
	}
}
