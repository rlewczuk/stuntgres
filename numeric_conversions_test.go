package stuntgres_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"stuntgres"
	"testing"
)

// Expectations are independent integer limits and REL_18_0 numeric_out ->
// float input diagnostics, not results obtained from the numeric provider.
func TestNumericConversionFailuresAndAtomicity(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), "numeric-conversion-boundaries")
			execute := func(q string) (sql.Result, error) {
				t.Helper()
				if !prepared {
					return db.Exec(q)
				}
				stmt, err := db.Prepare(q)
				if err != nil {
					t.Fatal("conversion must not fail Prepare", q, err)
				}
				defer stmt.Close()
				return stmt.Exec()
			}
			check := func(q string, names []string, want [][]any) {
				t.Helper()
				var rows *sql.Rows
				var err error
				if prepared {
					stmt, e := db.Prepare(q)
					if e != nil {
						t.Fatal("successful conversion Prepare", q, e)
					}
					defer stmt.Close()
					rows, err = stmt.Query()
				} else {
					rows, err = db.Query(q)
				}
				if err != nil {
					t.Fatal(q, err)
				}
				defer rows.Close()
				types, err := rows.ColumnTypes()
				if err != nil || len(types) != len(names) {
					t.Fatal(types, err)
				}
				for i, name := range names {
					if types[i].DatabaseTypeName() != name {
						t.Fatal(q, types[i], name)
					}
				}
				got := [][]any{}
				for rows.Next() {
					row := make([]any, len(names))
					dst := make([]any, len(names))
					for i := range dst {
						dst[i] = &row[i]
					}
					if err := rows.Scan(dst...); err != nil {
						t.Fatal(err)
					}
					got = append(got, row)
				}
				if rows.Err() != nil || !reflect.DeepEqual(got, want) {
					t.Fatal(q, got, want, rows.Err())
				}
			}
			for _, tc := range []struct {
				name, decl, positive, negative, message string
				maximum, minimum                        int64
			}{
				{"i2", "SMALLINT", "32767", "32768", "smallint out of range", 32767, -32768},
				{"i4", "INTEGER", "2147483647", "2147483648", "integer out of range", 2147483647, -2147483648},
				{"i8", "BIGINT", "9223372036854775807", "9223372036854775808", "bigint out of range", 9223372036854775807, -9223372036854775808},
			} {
				execSQL(t, db, "CREATE TABLE "+tc.name+"(id INT,v "+tc.decl+")")
				r, err := execute("INSERT INTO " + tc.name + " VALUES(0,99),(1," + tc.positive + ".49),(2,-" + tc.negative + ".49)")
				if err != nil {
					t.Fatal(err)
				}
				if n, err := r.RowsAffected(); err != nil || n != 3 {
					t.Fatal(n, err)
				}
				want := [][]any{{int64(0), int64(99)}, {int64(1), tc.maximum}, {int64(2), tc.minimum}}
				wantTuples(t, db, "SELECT * FROM "+tc.name+" ORDER BY id", want)
				for _, literal := range []string{tc.positive + ".5", "-" + tc.negative + ".5"} {
					// The first new tuple is valid and rounds to 2; the later carry fails.
					r, err := execute("INSERT INTO " + tc.name + " VALUES(3,1.5),(4," + literal + ")")
					if r != nil {
						t.Fatal("failed assignment returned affected-count result")
					}
					typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22003", Message: tc.message})
					wantTuples(t, db, "SELECT * FROM "+tc.name+" ORDER BY id", want)
				}
			}
			execSQL(t, db, "CREATE TABLE small_compare(id INT,v SMALLINT)")
			r, err := execute("INSERT INTO small_compare VALUES(0,32767.49),(1,-32768.49),(2,1.5),(3,-1.5),(4,NULL)")
			if err != nil {
				t.Fatal(err)
			}
			if n, err := r.RowsAffected(); err != nil || n != 5 {
				t.Fatal(n, err)
			}
			check("SELECT * FROM small_compare ORDER BY id", []string{"INT4", "INT2"}, [][]any{{int64(0), int64(32767)}, {int64(1), int64(-32768)}, {int64(2), int64(2)}, {int64(3), int64(-2)}, {int64(4), nil}})
			for _, tc := range []struct {
				literal string
				want    [][]any
			}{
				{"32767.0", [][]any{{int64(32767)}}},
				{"-32768.0", [][]any{{int64(-32768)}}},
				{"32767.1", [][]any{}},
				{"32768.0", [][]any{}},
				{"-32769.0", [][]any{}},
				{"1e100", [][]any{}},
				{"NULL", [][]any{}},
			} {
				check("SELECT v FROM small_compare WHERE v="+tc.literal, []string{"INT2"}, tc.want)
			}
			execSQL(t, db, "CREATE TABLE floats(id INT,r REAL,d DOUBLE PRECISION)")
			execSQL(t, db, "INSERT INTO floats VALUES(99,1.0,1.0)")
			unchanged := [][]any{{int64(99), float64(1), float64(1)}}
			execSQL(t, db, "CREATE TABLE float_controls(id INT,r REAL,d DOUBLE PRECISION)")
			for i, literal := range []string{"1.5", "-1.5", "0e-400"} {
				r, err := execute(fmt.Sprintf("INSERT INTO float_controls VALUES(%d,%s,%s)", i, literal, literal))
				if err != nil {
					t.Fatal(err)
				}
				if n, err := r.RowsAffected(); err != nil || n != 1 {
					t.Fatal(n, err)
				}
			}
			check("SELECT * FROM float_controls ORDER BY id", []string{"INT4", "FLOAT4", "FLOAT8"}, [][]any{{int64(0), float64(1.5), float64(1.5)}, {int64(1), float64(-1.5), float64(-1.5)}, {int64(2), float64(0), float64(0)}})
			for _, tc := range []struct{ column, literal, text, name string }{
				{"r", "1e40", "1" + strings.Repeat("0", 40), "real"},
				{"r", "1e-50", "0." + strings.Repeat("0", 49) + "1", "real"},
				{"d", "1e400", "1" + strings.Repeat("0", 400), "double precision"},
				{"d", "1e-400", "0." + strings.Repeat("0", 399) + "1", "double precision"},
			} {
				r, err := execute("INSERT INTO floats(id," + tc.column + ") VALUES(1,.5),(2," + tc.literal + ")")
				if r != nil {
					t.Fatal("failed float assignment returned affected-count result")
				}
				typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22003", Message: `"` + tc.text + `" is out of range for type ` + tc.name})
				wantTuples(t, db, "SELECT * FROM floats ORDER BY id", unchanged)
			}
			for _, column := range []string{"r", "d"} {
				name := "FLOAT4"
				if column == "d" {
					name = "FLOAT8"
				}
				check("SELECT "+column+" FROM floats WHERE "+column+"=1.0", []string{name}, [][]any{{float64(1)}})
				check("SELECT "+column+" FROM floats WHERE "+column+"=1e100", []string{name}, [][]any{})
				for _, tc := range []struct{ literal, text string }{
					{"1e400", "1" + strings.Repeat("0", 400)},
					{"1e-400", "0." + strings.Repeat("0", 399) + "1"},
				} {
					q := "SELECT id FROM floats WHERE " + column + "=" + tc.literal
					var rows *sql.Rows
					var err error
					if prepared {
						stmt, e := db.Prepare(q)
						if e != nil {
							t.Fatal("known numeric comparison must not fail Prepare", e)
						}
						rows, err = stmt.Query()
						stmt.Close()
					} else {
						rows, err = db.Query(q)
					}
					if rows != nil {
						rows.Close()
						t.Fatal("failed comparison exposed cursor")
					}
					typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22003", Message: `"` + tc.text + `" is out of range for type double precision`})
					wantTuples(t, db, "SELECT * FROM floats ORDER BY id", unchanged)
				}
			}
		})
	}
}
