package stuntgres_test

import (
	"database/sql"
	"reflect"
	"stuntgres"
	"testing"
)

func TestByteaNullAndEmptyMetadata(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	for _, tc := range []struct {
		name, decl        string
		nullable, nullRow bool
	}{
		{"empty", "BYTEA", true, false}, {"nulls", "BYTEA", true, true}, {"required", "BYTEA NOT NULL", false, false},
	} {
		execSQL(t, db, "CREATE TABLE "+tc.name+"(v "+tc.decl+")")
		if tc.nullRow {
			execSQL(t, db, "INSERT INTO "+tc.name+" VALUES(NULL)")
		}
		for _, prepared := range []bool{false, true} {
			var rows *sql.Rows
			var err error
			if prepared {
				s, e := db.Prepare("SELECT v FROM " + tc.name)
				if e != nil {
					t.Fatal(e)
				}
				rows, err = s.Query()
				s.Close()
			} else {
				rows, err = db.Query("SELECT v FROM " + tc.name)
			}
			if err != nil {
				t.Fatal(err)
			}
			types, err := rows.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			check := func() {
				t.Helper()
				c := types[0]
				if c.Name() != "v" || c.DatabaseTypeName() != "BYTEA" || c.ScanType() != reflect.TypeFor[[]byte]() {
					t.Fatal(c)
				}
				n, k := c.Nullable()
				if !k || n != tc.nullable {
					t.Fatal(n, k)
				}
				if _, k := c.Length(); k {
					t.Fatal("length")
				}
				if _, _, k := c.DecimalSize(); k {
					t.Fatal("decimal")
				}
			}
			check()
			n := 0
			for rows.Next() {
				var v any
				if err := rows.Scan(&v); err != nil || v != nil {
					t.Fatal(v, err)
				}
				n++
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			check()
			rows.Close()
			check()
			want := 0
			if tc.nullRow {
				want = 1
			}
			if n != want {
				t.Fatal(n, want)
			}
		}
	}
}
