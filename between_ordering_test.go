package stuntgres_test

import "testing"

func TestBetweenMixedProvidersAndExceptionalOrdering(t *testing.T) {
	_, db := betweenSetup(t)
	execSQL(t, db, "CREATE TABLE providers(a NAME,l TEXT,u VARCHAR(3),c CHAR(3),n NUMERIC,f REAL,g DOUBLE PRECISION)")
	execSQL(t, db, "INSERT INTO providers VALUES('b','a','c','b  ','NaN','NaN','Infinity')")
	compareOpsDirectPrepared(t, db, "SELECT a BETWEEN l AND 'c',c BETWEEN l AND u,n BETWEEN 'Infinity' AND 'NaN',n BETWEEN 0 AND 'Infinity',f BETWEEN g AND 'NaN',f BETWEEN 0 AND g FROM providers", []string{"BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true, false, true, false}})
	execSQL(t, db, "CREATE TABLE crosswidth(a SMALLINT,l INTEGER,u BIGINT,n NUMERIC)")
	execSQL(t, db, "INSERT INTO crosswidth VALUES(-32768,-32768,2147483648,9007199254740993)")
	compareOpsDirectPrepared(t, db, "SELECT a BETWEEN l AND u,a BETWEEN SYMMETRIC u AND l,n BETWEEN 9007199254740992 AND 9007199254740993 FROM crosswidth", []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true}})
}
