package smoke_test

import (
	"database/sql"
	"errors"
	"reflect"
	"testing"

	"stuntgres"
)

func TestBtreeIndexSmoke(t *testing.T) {
	connector, err := stuntgres.NewDriver().OpenConnector(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(connector)
	defer db.Close()
	exec := func(query string, count int64) {
		t.Helper()
		result, err := db.Exec(query)
		if err != nil {
			t.Fatal(query, err)
		}
		if got, err := result.RowsAffected(); err != nil || got != count {
			t.Fatal(query, got, count, err)
		}
	}
	exec("CREATE TABLE t(n INTEGER,s TEXT)", 0)
	exec("INSERT INTO t VALUES(2,'b'),(1,'a'),(2,'b'),(NULL,NULL),(3,'c')", 5)
	exec("CREATE INDEX t_n ON t(n)", 0)
	stmt, err := db.Prepare("CREATE INDEX t_s ON t USING btree(s)")
	if err != nil {
		t.Fatal(err)
	}
	result, err := stmt.Exec()
	stmt.Close()
	if err != nil {
		t.Fatal(err)
	}
	if count, err := result.RowsAffected(); err != nil || count != 0 {
		t.Fatal(count, err)
	}
	check := func(query string, want [][]any, bag bool, args ...any) {
		t.Helper()
		stmt, err := db.Prepare(query)
		if err != nil {
			t.Fatal(query, err)
		}
		defer stmt.Close()
		rows, err := stmt.Query(args...)
		if err != nil {
			t.Fatal(query, err)
		}
		defer rows.Close()
		columns, err := rows.Columns()
		if err != nil || !reflect.DeepEqual(columns, []string{"n", "s"}) {
			t.Fatal(columns, err)
		}
		types, err := rows.ColumnTypes()
		if err != nil || len(types) != 2 {
			t.Fatal(types, err)
		}
		if types[0].DatabaseTypeName() != "INT4" || types[1].DatabaseTypeName() != "TEXT" {
			t.Fatal(types)
		}
		got := make([][]any, 0)
		for rows.Next() {
			var n, s any
			if err := rows.Scan(&n, &s); err != nil {
				t.Fatal(err)
			}
			got = append(got, []any{n, s})
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		if bag {
			if len(got) != len(want) {
				t.Fatal(query, got, want)
			}
			used := make([]bool, len(want))
			for _, row := range got {
				found := false
				for i, expected := range want {
					if !used[i] && reflect.DeepEqual(row, expected) {
						used[i], found = true, true
						break
					}
				}
				if !found {
					t.Fatal(query, got, want)
				}
			}
		} else if !reflect.DeepEqual(got, want) {
			t.Fatal(query, got, want)
		}
	}
	check("SELECT n,s FROM t WHERE n=$1", [][]any{{int64(2), "b"}, {int64(2), "b"}}, true, 2)
	check("SELECT n,s FROM t WHERE n>=2", [][]any{{int64(2), "b"}, {int64(2), "b"}, {int64(3), "c"}}, true)
	check("SELECT n,s FROM t WHERE n IS NULL", [][]any{{nil, nil}}, true)
	check("SELECT n,s FROM t WHERE s>'a'", [][]any{{int64(2), "b"}, {int64(2), "b"}, {int64(3), "c"}}, true)
	check("SELECT n,s FROM t ORDER BY n ASC NULLS LAST", [][]any{{int64(1), "a"}, {int64(2), "b"}, {int64(2), "b"}, {int64(3), "c"}, {nil, nil}}, false)
	check("SELECT n,s FROM t ORDER BY n DESC", [][]any{{nil, nil}, {int64(3), "c"}, {int64(2), "b"}, {int64(2), "b"}, {int64(1), "a"}}, false)
	for _, tc := range []struct{ query, code, message string }{
		{"CREATE INDEX t_n ON t(n)", "42P07", `relation "t_n" already exists`},
		{"CREATE INDEX retry ON missing(n)", "42P01", `relation "missing" does not exist`},
		{"CREATE INDEX retry ON t(missing)", "42703", `column "missing" does not exist`},
	} {
		_, err := db.Exec(tc.query)
		var diagnostic *stuntgres.Error
		if !errors.As(err, &diagnostic) {
			t.Fatal(tc.query, err)
		}
		want := stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message}
		if !reflect.DeepEqual(*diagnostic, want) {
			t.Fatal(tc.query, diagnostic, want)
		}
	}
	exec("CREATE INDEX retry ON t(n)", 0)
}
