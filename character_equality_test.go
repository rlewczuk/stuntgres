package stuntgres_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres"
)

func TestCharacterRightColumnAndPreparedEquality(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE c(c CHAR(2))")
	execSQL(t, db, "INSERT INTO c VALUES('a'),('b'),(NULL)")
	for _, predicate := range []string{"CAST('a ' AS bpchar)=c", "'a    '=c", "CAST('a' AS text)=c", "CAST(c AS text)='a' AND NOT CAST('a ' AS text)=c"} {
		q := "SELECT c FROM c WHERE " + predicate
		wantTuples(t, db, q, [][]any{{"a "}})
		stmt, err := db.Prepare(q)
		if err != nil {
			t.Fatal(err)
		}
		rows, err := stmt.Query()
		if err != nil {
			stmt.Close()
			t.Fatal(err)
		}
		var got []any
		for rows.Next() {
			var v any
			if err := rows.Scan(&v); err != nil {
				rows.Close()
				stmt.Close()
				t.Fatal(err)
			}
			got = append(got, v)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
		stmt.Close()
		if len(got) != 1 || got[0] != "a " {
			t.Fatal(got)
		}
	}
	stmt, err := db.Prepare("SELECT c FROM c WHERE $1=c")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	for _, tc := range []struct {
		arg   any
		count int
		want  string
	}{{"a   ", 1, "a "}, {"b", 1, "b "}, {"missing", 0, ""}, {nil, 0, ""}} {
		rows, err := stmt.Query(tc.arg)
		if err != nil {
			t.Fatal(err)
		}
		count := 0
		for rows.Next() {
			var got string
			if err := rows.Scan(&got); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			if got != tc.want {
				rows.Close()
				t.Fatal(tc, got)
			}
			count++
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		rows.Close()
		if count != tc.count {
			t.Fatal(tc, count)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rows, err := stmt.QueryContext(ctx, "a")
	if rows != nil {
		rows.Close()
		t.Fatal("canceled query exposed rows")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
