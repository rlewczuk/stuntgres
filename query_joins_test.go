package stuntgres_test

import (
	"database/sql"
	"testing"

	"stuntgres"
)

func queryJoinsPublicFixture(t *testing.T) *sql.DB {
	t.Helper()
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	for _, q := range []string{
		"CREATE TABLE a(l TEXT,k INT,q INT)",
		"CREATE TABLE b(r TEXT,q INT,k INT)",
		"CREATE TABLE c(k INT,c TEXT)",
		"CREATE TABLE empty(r TEXT,q INT,k INT)",
		"CREATE TABLE wide(k BIGINT)",
		"INSERT INTO a VALUES('a',1,9),('a',1,9),('n',NULL,9),('z',2,8)",
		"INSERT INTO b VALUES('x',9,1),('x',9,1),('y',8,3),('n',9,NULL)",
		"INSERT INTO c VALUES(1,'c'),(1,'c')",
		"INSERT INTO wide VALUES(1)",
	} {
		execSQL(t, db, q)
	}
	return db
}
