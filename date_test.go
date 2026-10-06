package stuntgres_test

import (
	"database/sql"
	"reflect"
	"stuntgres"
	"testing"
)

func dateCheckRows(t *testing.T, rows *sql.Rows, want []any) {
	t.Helper()
	defer rows.Close()
	meta, err := rows.ColumnTypes()
	if err != nil || len(meta) != 1 {
		t.Fatalf("metadata = %#v, %v", meta, err)
	}
	if meta[0].DatabaseTypeName() != "DATE" || meta[0].ScanType() != reflect.TypeFor[string]() {
		t.Fatalf("metadata = %s/%v; want DATE/string", meta[0].DatabaseTypeName(), meta[0].ScanType())
	}
	if _, _, ok := meta[0].DecimalSize(); ok {
		t.Fatal("DATE must not expose decimal precision")
	}
	if _, ok := meta[0].Length(); ok {
		t.Fatal("DATE must not expose storage length")
	}
	var got []any
	for rows.Next() {
		var value any
		if err := rows.Scan(&value); err != nil {
			t.Fatal(err)
		}
		got = append(got, value)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	// database/sql closes Rows at EOF; the captured metadata remains usable.
	if meta[0].DatabaseTypeName() != "DATE" || meta[0].ScanType() != reflect.TypeFor[string]() {
		t.Fatal("DATE metadata changed at EOF", meta[0])
	}
	if len(got) != len(want) {
		t.Fatalf("rows = %#v; want %#v", got, want)
	}
	for i := range got {
		if !reflect.DeepEqual(got[i], want[i]) {
			t.Fatalf("row %d = %#v; want %#v", i, got[i], want[i])
		}
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if meta[0].DatabaseTypeName() != "DATE" || meta[0].ScanType() != reflect.TypeFor[string]() {
		t.Fatal("DATE metadata changed after Close", meta[0])
	}
	if _, _, ok := meta[0].DecimalSize(); ok {
		t.Fatal("retained DATE must not expose decimal precision")
	}
	if _, ok := meta[0].Length(); ok {
		t.Fatal("retained DATE must not expose storage length")
	}
}

func TestDateDeclarationsDocumentedInputsAndMetadata(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), t.Name())
			r, err := db.Exec("CREATE TABLE d(v date)")
			if err != nil {
				t.Fatal(err)
			}
			if n, err := r.RowsAffected(); err != nil || n != 0 {
				t.Fatal(n, err)
			}
			forms := []string{"1999-01-08", "January 8, 1999", "1999-Jan-08", "Jan-08-1999", "08-Jan-1999", "19990108", "990108", "1999.008", "J2451187", "1999-01-08"}
			var stmt *sql.Stmt
			if prepared {
				stmt, err = db.Prepare("INSERT INTO d VALUES($1)")
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
			}
			for _, input := range forms {
				if prepared {
					r, err = stmt.Exec(input)
				} else {
					r, err = db.Exec("INSERT INTO d VALUES($1)", input)
				}
				if err != nil {
					t.Fatal(input, err)
				}
				if n, err := r.RowsAffected(); err != nil || n != 1 {
					t.Fatal(n, err)
				}
			}
			want := make([]any, len(forms))
			for i := range want {
				want[i] = "1999-01-08"
			}
			for _, q := range []string{"SELECT v FROM d", "SELECT DATE '1999-01-08' FROM d WHERE v='1999-01-08'"} {
				rows, err := db.Query(q)
				if err != nil {
					t.Fatal(err)
				}
				dateCheckRows(t, rows, want)
			}
			execSQL(t, db, `CREATE TABLE date(date "date")`)
			rows, err := db.Query("SELECT date FROM date")
			if err != nil {
				t.Fatal(err)
			}
			dateCheckRows(t, rows, nil)
			execSQL(t, db, "INSERT INTO date VALUES(NULL)")
			rows, err = db.Query("SELECT date FROM date")
			if err != nil {
				t.Fatal(err)
			}
			dateCheckRows(t, rows, []any{nil})
		})
	}
}

func TestDateCivilDomainAndSpecialValues(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE d(id INTEGER,v DATE)")
	ins, err := db.Prepare("INSERT INTO d VALUES($1,$2)")
	if err != nil {
		t.Fatal(err)
	}
	defer ins.Close()
	cases := []struct{ input, output string }{
		{"4714-11-24 BC", "4714-11-24 BC"}, {"5874897-12-31", "5874897-12-31"},
		{"300000-01-01", "300000-01-01"}, {"0001-02-29 BC", "0001-02-29 BC"},
		{"1600-02-29", "1600-02-29"}, {"2000-02-29", "2000-02-29"}, {"2400-02-29", "2400-02-29"},
		{"epoch", "1970-01-01"}, {"infinity", "infinity"}, {"-infinity", "-infinity"},
		{"January 8, 99 BC", "0099-01-08 BC"}, {"01/02/03", "2003-01-02"},
		{"Jan 8 69", "2069-01-08"}, {"Jan 8 70", "1970-01-08"},
	}
	want := make([]any, len(cases))
	for i, tc := range cases {
		r, err := ins.Exec(i, tc.input)
		if err != nil {
			t.Fatal(tc.input, err)
		}
		if n, err := r.RowsAffected(); err != nil || n != 1 {
			t.Fatal(n, err)
		}
		want[i] = tc.output
	}
	rows, err := db.Query("SELECT v FROM d ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	dateCheckRows(t, rows, want)
}
