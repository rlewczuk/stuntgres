package reference_test

import "testing"

func TestReferenceIdentifierPublicContractRoutes(t *testing.T) {
	p := newReferenceScenario(t)
	for _, s := range []referenceStep{
		referenceExec("CREATE TABLE abort (by INT,between TEXT,values BOOL,integer BIGINT,char TEXT)", 0),
		referenceExec("INSERT INTO public.abort (by,between,values,integer,char) VALUES (1,'one',TRUE,10,'a'),(2,'two',FALSE,20,'b')", 2),
		referencePrepared(referenceExec("INSERT INTO abort (by,between,values,integer,char) VALUES ($1,$2,$3,$4,$5)", 1, 3, "three", true, 30, "c")),
		referenceSelect("SELECT by,between,values,integer,char FROM public.abort WHERE by=2 ORDER BY by DESC", []string{"by", "between", "values", "integer", "char"}, []string{"INT4", "TEXT", "BOOL", "INT8", "TEXT"}, [][]any{{int64(2), "two", false, int64(20), "b"}}),
		referencePrepared(referenceSelect("SELECT by,between,values,integer,char FROM abort WHERE by=$1 ORDER BY by DESC", []string{"by", "between", "values", "integer", "char"}, []string{"INT4", "TEXT", "BOOL", "INT8", "TEXT"}, [][]any{{int64(2), "two", false, int64(20), "b"}}, 2)),
		referenceExec("SELECT by,between,values,integer,char FROM public.abort WHERE by=2 ORDER BY by DESC", 1),
	} {
		p.step(t, s)
	}
}
