package stuntgres_test

import (
	"database/sql"
	"reflect"
	"stuntgres"
	"testing"
)

func TestTimeBaseline(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE baseline_time(v TIME(6))")
	result, err := db.Exec("INSERT INTO baseline_time VALUES('24:00')")
	if err != nil {
		t.Fatal(err)
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		t.Fatalf("affected rows = %d, error = %v; want 1", n, err)
	}
	rows, err := db.Query("SELECT v FROM baseline_time")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if len(columns) != 1 || columns[0].Name() != "v" || columns[0].DatabaseTypeName() != "TIME" || columns[0].ScanType() != reflect.TypeFor[string]() {
		t.Fatalf("metadata = %#v; want v TIME/string", columns)
	}
	if !rows.Next() {
		t.Fatalf("missing row: %v", rows.Err())
	}
	var value any
	if err := rows.Scan(&value); err != nil {
		t.Fatal(err)
	}
	if value != "24:00:00" {
		t.Fatalf("value = %#v; want string 24:00:00", value)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatalf("unexpected extra row or iteration error: %v", rows.Err())
	}
}

func timeCheckRows(t *testing.T, rows *sql.Rows, names, types []string, want [][]any) {
	t.Helper()
	defer rows.Close()
	gotNames, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(gotNames, names) {
		t.Fatal(gotNames, err)
	}
	metadata, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	for i, typ := range types {
		if metadata[i].DatabaseTypeName() != typ {
			t.Fatal(metadata[i])
		}
		if typ == "TIME" && metadata[i].ScanType() != reflect.TypeFor[string]() {
			t.Fatal(metadata[i])
		}
	}
	var got [][]any
	for rows.Next() {
		row, dst := make([]any, len(names)), make([]any, len(names))
		for i := range row {
			dst[i] = &row[i]
		}
		if err = rows.Scan(dst...); err != nil {
			t.Fatal(err)
		}
		got = append(got, row)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatalf("rows %#v want %#v", got, want)
	}
	for i := range got {
		if !reflect.DeepEqual(got[i], want[i]) {
			t.Fatalf("row %#v want %#v", got[i], want[i])
		}
	}
}

func timeQuery(t *testing.T, db *sql.DB, q string, names, types []string, want [][]any, args ...any) {
	t.Helper()
	rows, err := db.Query(q, args...)
	if err != nil {
		t.Fatal(q, err)
	}
	timeCheckRows(t, rows, names, types, want)
}

func TestTimeDeclarationsValuesAndMetadata(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), t.Name())
			execSQL(t, db, "CREATE TABLE t(id INTEGER,v TIME(0),fine TIME(6) WITHOUT TIME ZONE)")
			q := "INSERT INTO t VALUES(1,'00:00','00:00'),(2,'24:00','24:00'),(3,'04:05 PM','04:05:06.120000'),(4,NULL,NULL)"
			var result sql.Result
			var err error
			if prepared {
				stmt, e := db.Prepare(q)
				if e != nil {
					t.Fatal(e)
				}
				defer stmt.Close()
				result, err = stmt.Exec()
			} else {
				result, err = db.Exec(q)
			}
			if err != nil {
				t.Fatal(err)
			}
			n, err := result.RowsAffected()
			if err != nil || n != 4 {
				t.Fatal(n, err)
			}
			timeQuery(t, db, "SELECT id,v,fine FROM t ORDER BY id", []string{"id", "v", "fine"}, []string{"INT4", "TIME", "TIME"}, [][]any{{int64(1), "00:00:00", "00:00:00"}, {int64(2), "24:00:00", "24:00:00"}, {int64(3), "16:05:00", "04:05:06.12"}, {int64(4), nil, nil}})
			timeQuery(t, db, "SELECT TIME '24:00',TIME(3) '04:05:06.1235' FROM t WHERE id=1", []string{"time", "time"}, []string{"TIME", "TIME"}, [][]any{{"24:00:00", "04:05:06.124"}})
			execSQL(t, db, `CREATE TABLE quoted(v "time"(3) NOT NULL,w U&"t\0069me"(6))`)
			rows, err := db.Query("SELECT v,w FROM quoted")
			if err != nil {
				t.Fatal(err)
			}
			meta, err := rows.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			rows.Close()
			if nullable, known := meta[0].Nullable(); !known || nullable {
				t.Fatal("NOT NULL metadata", meta[0])
			}
			if nullable, known := meta[1].Nullable(); !known || !nullable {
				t.Fatal("NULL metadata", meta[1])
			}
		})
	}
}
