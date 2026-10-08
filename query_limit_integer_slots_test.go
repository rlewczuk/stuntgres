package stuntgres_test

import "testing"

func TestQueryLimitIntegerSlots(t *testing.T) {
	db := queryLimitFixture(t)
	execSQL(t, db, "CREATE TABLE widths(i2 SMALLINT,i4 INTEGER,i8 BIGINT,o OID)")
	execSQL(t, db, "INSERT INTO widths VALUES(2,2,2,2)")
	for _, column := range []string{"i2", "i4", "i8", "o"} {
		t.Run(column, func(t *testing.T) {
			q := "SELECT t.id FROM t,widths WHERE widths." + column + "=$1 ORDER BY t.id LIMIT $1"
			rows, err := db.Query(q, int64(2))
			queryCoreRows(t, rows, err, []string{"id"}, []string{"INT4"}, queryLimitIDs(1, 2), false)
			stmt, err := db.Prepare(q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			rows, err = stmt.Query(int64(2))
			queryCoreRows(t, rows, err, []string{"id"}, []string{"INT4"}, queryLimitIDs(1, 2), false)
		})
	}
}
