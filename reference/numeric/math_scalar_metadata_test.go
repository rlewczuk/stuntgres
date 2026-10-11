package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceMathScalarComputedMetadata(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE m(s SMALLINT,i INTEGER,b BIGINT,r REAL,d DOUBLE PRECISION,n NUMERIC(8,3))", 0))
	p.Step(t, fixture.Exec("INSERT INTO m VALUES(-17,-17,-17,-17.5,-17.5,-17.400)", 1))
	referenceArithmeticMetadata(t, p, fixture.Select("SELECT abs(s),abs(i),abs(b),abs(r),abs(d),abs(n),ceil(n),ceiling(n),floor(n),div(n,4),exp(0.0),factorial(5) FROM m",
		[]string{"abs", "abs", "abs", "abs", "abs", "abs", "ceil", "ceiling", "floor", "div", "exp", "factorial"},
		[]string{"INT2", "INT4", "INT8", "FLOAT4", "FLOAT8", "NUMERIC", "NUMERIC", "NUMERIC", "NUMERIC", "NUMERIC", "NUMERIC", "NUMERIC"},
		[][]any{{int64(17), int64(17), int64(17), float64(17.5), float64(17.5), "17.400", "-17", "-17", "-18", "-4", "1.0000000000000000", "120"}}))
	names := []string{"abs", "ceil", "ceiling", "floor", "exp", "div", "factorial"}
	referenceArithmeticMetadata(t, p, fixture.Select("SELECT abs(NULL),ceil(NULL),ceiling(NULL),floor(NULL),exp(NULL),div(NULL,1),factorial(NULL)", names, []string{"FLOAT8", "FLOAT8", "FLOAT8", "FLOAT8", "FLOAT8", "NUMERIC", "NUMERIC"}, [][]any{{nil, nil, nil, nil, nil, nil, nil}}))
	referenceArithmeticMetadata(t, p, fixture.Select("SELECT abs(s),ceil(r),ceiling(d),floor(n),exp(n),div(n,1),factorial(b) FROM m WHERE false", names, []string{"INT2", "FLOAT8", "FLOAT8", "NUMERIC", "NUMERIC", "NUMERIC", "NUMERIC"}, nil))
}
