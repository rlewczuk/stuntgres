package smoke_test

import (
	"database/sql"
	"testing"

	"stuntgres"
)

func TestConditionalAggregateSmoke(t *testing.T) {
	connector, err := stuntgres.NewDriver().OpenConnector(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(connector)
	defer db.Close()
	for _, q := range []string{"CREATE TABLE t(v INTEGER)", "INSERT INTO t VALUES(1),(1),(2)"} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	q := "SELECT CASE count(*) WHEN 3 THEN count(*) ELSE 0 END,COALESCE(count(*),0),NULLIF(count(*),0),GREATEST(count(*),2),LEAST(count(*),2) FROM t"
	names := []string{"case", "coalesce", "nullif", "greatest", "least"}
	types := []string{"INT8", "INT8", "INT8", "INT8", "INT8"}
	want := [][]any{{int64(3), int64(3), int64(3), int64(3), int64(2)}}
	rows, err := db.Query(q)
	aggregateSmokeRows(t, rows, err, names, types, want)
	stmt, err := db.Prepare(q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	for range 2 {
		rows, err = stmt.Query()
		aggregateSmokeRows(t, rows, err, names, types, want)
	}
}
