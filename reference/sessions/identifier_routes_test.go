package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceIdentifierPublicContractRoutes(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, s := range []fixture.Step{
		fixture.Exec("CREATE TABLE abort (by INT,between TEXT,values BOOL,integer BIGINT,char TEXT)", 0),
		fixture.Exec("INSERT INTO public.abort (by,between,values,integer,char) VALUES (1,'one',TRUE,10,'a'),(2,'two',FALSE,20,'b')", 2),
		fixture.Prepared(fixture.Exec("INSERT INTO abort (by,between,values,integer,char) VALUES ($1,$2,$3,$4,$5)", 1, 3, "three", true, 30, "c")),
		fixture.Select("SELECT by,between,values,integer,char FROM public.abort WHERE by=2 ORDER BY by DESC", []string{"by", "between", "values", "integer", "char"}, []string{"INT4", "TEXT", "BOOL", "INT8", "TEXT"}, [][]any{{int64(2), "two", false, int64(20), "b"}}),
		fixture.Prepared(fixture.Select("SELECT by,between,values,integer,char FROM abort WHERE by=$1 ORDER BY by DESC", []string{"by", "between", "values", "integer", "char"}, []string{"INT4", "TEXT", "BOOL", "INT8", "TEXT"}, [][]any{{int64(2), "two", false, int64(20), "b"}}, 2)),
		fixture.Exec("SELECT by,between,values,integer,char FROM public.abort WHERE by=2 ORDER BY by DESC", 1),
	} {
		p.Step(t, s)
	}
}
