package reference_test

import "testing"

func TestReferenceMathScalarTypedNullOverloads(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE scalar_nulls(s SMALLINT,i INTEGER,b BIGINT,r REAL,d DOUBLE PRECISION,n NUMERIC(8,3))", 0))
	p.step(t, referenceExec("INSERT INTO scalar_nulls VALUES(NULL,NULL,NULL,NULL,NULL,NULL)", 1))
	names := []string{"abs", "abs", "abs", "abs", "abs", "abs", "ceil", "ceiling", "floor", "exp", "ceil", "ceiling", "floor", "exp", "div", "factorial"}
	types := []string{"INT2", "INT4", "INT8", "FLOAT4", "FLOAT8", "NUMERIC", "FLOAT8", "FLOAT8", "FLOAT8", "FLOAT8", "NUMERIC", "NUMERIC", "NUMERIC", "NUMERIC", "NUMERIC", "NUMERIC"}
	query := "SELECT abs(s),abs(i),abs(b),abs(r),abs(d),abs(n),ceil(d),ceiling(d),floor(d),exp(d),ceil(n),ceiling(n),floor(n),exp(n),div(n,n),factorial(b) FROM scalar_nulls"
	for _, empty := range []bool{false, true} {
		q := query
		want := [][]any{make([]any, len(names))}
		if empty {
			q += " WHERE false"
			want = nil
		}
		referenceArithmeticMetadata(t, p, referenceSelect(q, names, types, want))
	}
}
