package stuntgres_test

import (
	"stuntgres"
	"testing"
)

// TestOIDUnsignedValues is the first public behavioral regression for the OID
// scalar. It uses only the existing database/sql driver API and SQL syntax.
func TestOIDUnsignedValues(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, `CREATE TABLE oid_values(id INT,v OID)`)
	execSQL(t, db, `INSERT INTO oid_values VALUES(1,'0'),(2,'1'),(3,'2147483647'),(4,'2147483648'),(5,'4294967295'),(6,NULL)`)
	wantTuples(t, db, `SELECT id,v FROM oid_values ORDER BY id`, [][]any{
		{int64(1), int64(0)},
		{int64(2), int64(1)},
		{int64(3), int64(2147483647)},
		{int64(4), int64(2147483648)},
		{int64(5), int64(4294967295)},
		{int64(6), nil},
	})
	wantTuples(t, db, `SELECT v FROM oid_values ORDER BY v`, [][]any{
		{int64(0)}, {int64(1)}, {int64(2147483647)},
		{int64(2147483648)}, {int64(4294967295)}, {nil},
	})
	wantTuples(t, db, `SELECT v FROM oid_values ORDER BY v DESC`, [][]any{
		{nil}, {int64(4294967295)}, {int64(2147483648)},
		{int64(2147483647)}, {int64(1)}, {int64(0)},
	})
}
