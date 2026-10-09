package reference_test

import "testing"

func TestReferenceBetweenMixedProvidersAndExceptionalOrdering(t *testing.T) {
	p := newReferenceScenario(t)
	scalarStep(t, p, referenceExec("CREATE TABLE providers(a NAME,l TEXT,u VARCHAR(3),c CHAR(3),n NUMERIC,f REAL,g DOUBLE PRECISION)", 0))
	scalarStep(t, p, referenceExec("INSERT INTO providers VALUES('b','a','c','b  ','NaN','NaN','Infinity')", 1))
	betweenReferenceBoth(t, p, referenceSelect("SELECT a BETWEEN l AND 'c',c BETWEEN l AND u,n BETWEEN 'Infinity' AND 'NaN',n BETWEEN 0 AND 'Infinity',f BETWEEN g AND 'NaN',f BETWEEN 0 AND g FROM providers", []string{"?column?", "?column?", "?column?", "?column?", "?column?", "?column?"}, []string{"BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true, false, true, false}}))
	scalarStep(t, p, referenceExec("CREATE TABLE crosswidth(a SMALLINT,l INTEGER,u BIGINT,n NUMERIC)", 0))
	scalarStep(t, p, referenceExec("INSERT INTO crosswidth VALUES(-32768,-32768,2147483648,9007199254740993)", 1))
	betweenReferenceBoth(t, p, referenceSelect("SELECT a BETWEEN l AND u,a BETWEEN SYMMETRIC u AND l,n BETWEEN 9007199254740992 AND 9007199254740993 FROM crosswidth", []string{"?column?", "?column?", "?column?"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true}}))
}
