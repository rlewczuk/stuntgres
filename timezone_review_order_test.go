package stuntgres_test

import (
	"testing"

	"stuntgres"
)

func TestTimezoneReviewOrderComposition(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "SET TIME ZONE 'UTC'")
	execSQL(t, db, "CREATE TABLE tz_order(id INT,c TIMESTAMP)")
	execSQL(t, db, "INSERT INTO tz_order VALUES(1,'2004-07-01 12:00'),(2,'2004-07-02 12:00'),(3,'2004-07-01 12:00'),(4,NULL)")
	for _, tc := range []struct {
		query, label, typ string
		want              [][]any
	}{
		{`SELECT id,(c::timestamp AT TIME ZONE 'UTC'::text AT TIME ZONE 'America/New_York')::text converted FROM tz_order ORDER BY c AT TIME ZONE 'UTC' ASC NULLS LAST,id DESC`, "converted", "TEXT", [][]any{{int64(3), "2004-07-01 08:00:00"}, {int64(1), "2004-07-01 08:00:00"}, {int64(2), "2004-07-02 08:00:00"}, {int64(4), nil}}},
		{`SELECT id,c AT TIME ZONE 'UTC' = "timestamptz" '2004-07-01 12:00+00' AS equal_value FROM tz_order ORDER BY equal_value DESC NULLS LAST,id DESC`, "equal_value", "BOOL", [][]any{{int64(3), true}, {int64(1), true}, {int64(2), false}, {int64(4), nil}}},
		{`SELECT id,(c AT LOCAL)::text AS local_value FROM tz_order ORDER BY c AT LOCAL DESC NULLS FIRST,id ASC`, "local_value", "TEXT", [][]any{{int64(4), nil}, {int64(2), "2004-07-02 12:00:00+00"}, {int64(1), "2004-07-01 12:00:00+00"}, {int64(3), "2004-07-01 12:00:00+00"}}},
		{`SELECT id,(pg_catalog."timestamptz"(3) '2004-07-01 12:00:00.9995+00' AT TIME ZONE 'UTC')::text AS converted FROM tz_order ORDER BY converted,id DESC`, "converted", "TEXT", [][]any{{int64(4), "2004-07-01 12:00:01"}, {int64(3), "2004-07-01 12:00:01"}, {int64(2), "2004-07-01 12:00:01"}, {int64(1), "2004-07-01 12:00:01"}}},
		{`SELECT id,(pg_catalog."timetz" '05:34:17-05' AT TIME ZONE 'UTC')::text converted FROM tz_order ORDER BY converted,id`, "converted", "TEXT", [][]any{{int64(1), "10:34:17+00"}, {int64(2), "10:34:17+00"}, {int64(3), "10:34:17+00"}, {int64(4), "10:34:17+00"}}},
	} {
		t.Run(tc.query, func(t *testing.T) {
			rows, err := db.Query(tc.query)
			queryCoreRows(t, rows, err, []string{"id", tc.label}, []string{"INT4", tc.typ}, tc.want, false)
			stmt, err := db.Prepare(tc.query)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			rows, err = stmt.Query()
			queryCoreRows(t, rows, err, []string{"id", tc.label}, []string{"INT4", tc.typ}, tc.want, false)
		})
	}
}
