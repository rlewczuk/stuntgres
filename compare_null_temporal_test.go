package stuntgres_test

import (
	"context"
	"testing"
)

func TestCompareNullTemporalSymmetryAndHeldFreeze(t *testing.T) {
	_, db := compareNullSetup(t)
	for _, expr := range []string{
		"'12:34:56' IS DISTINCT FROM TIME '12:34:56'",
		"TIME '12:34:56' IS DISTINCT FROM '12:34:56'",
		"'2024-12-31 12:34:56' IS DISTINCT FROM TIMESTAMP '2024-12-31 12:34:56'",
		"TIMESTAMP '2024-12-31 12:34:56' IS DISTINCT FROM '2024-12-31 12:34:56'",
	} {
		q := "SELECT " + expr + " FROM t"
		want := [][]any{{false}, {false}, {false}, {false}}
		rows, err := db.Query(q)
		compareNullRows(t, rows, err, []string{"?column?"}, []string{"BOOL"}, want)
		stmt, err := db.Prepare(q)
		if err != nil {
			t.Fatal(err)
		}
		rows, err = stmt.Query()
		compareNullRows(t, rows, err, []string{"?column?"}, []string{"BOOL"}, want)
		stmt.Close()
	}
	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "BEGIN"); err != nil {
		t.Fatal(err)
	}
	defer conn.ExecContext(ctx, "ROLLBACK")
	rows, err := conn.QueryContext(ctx, "SELECT TIMESTAMP 'now' FROM t")
	if err != nil {
		t.Fatal(err)
	}
	var captured any
	if !rows.Next() {
		t.Fatal("missing clock row")
	}
	if err := rows.Scan(&captured); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	stmt, err := conn.PrepareContext(ctx, "SELECT 'now' IS DISTINCT FROM TIMESTAMP 'now',TIMESTAMP 'now' IS DISTINCT FROM $1,'now' IS DISTINCT FROM $1 FROM t")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		rows, err := stmt.Query(captured)
		compareNullRows(t, rows, err, []string{"?column?", "?column?", "?column?"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{false, false, false}, {false, false, false}, {false, false, false}, {false, false, false}})
	}
}
