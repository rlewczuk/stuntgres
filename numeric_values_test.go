package stuntgres_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"stuntgres"
	"testing"
)

func TestNumericValuesInferenceAndMetadata(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), "numeric-values")
			execute := func(q string, count int64, args ...any) {
				t.Helper()
				var result sql.Result
				var err error
				if prepared {
					stmt, e := db.Prepare(q)
					if e != nil {
						t.Fatal(e)
					}
					defer stmt.Close()
					result, err = stmt.Exec(args...)
				} else {
					result, err = db.Exec(q, args...)
				}
				if err != nil {
					t.Fatal(q, err)
				}
				n, err := result.RowsAffected()
				if err != nil || n != count {
					t.Fatal(n, err)
				}
			}
			execute(`CREATE TABLE n(id INTEGER,v NUMERIC,a DECIMAL,b DEC,c "numeric")`, 0)
			aliases, aliasErr := db.Query("SELECT * FROM n")
			if aliasErr != nil {
				t.Fatal(aliasErr)
			}
			aliasTypes, aliasErr := aliases.ColumnTypes()
			aliases.Close()
			if aliasErr != nil {
				t.Fatal(aliasErr)
			}
			for _, typ := range aliasTypes[1:] {
				if typ.DatabaseTypeName() != "NUMERIC" || typ.ScanType() != reflect.TypeFor[string]() {
					t.Fatal(typ)
				}
				if _, _, ok := typ.DecimalSize(); ok {
					t.Fatal("unconstrained numeric invented sizing")
				}
			}
			exact := "12345678901234567890.1234567890123456789000"
			execute("INSERT INTO n(id,v) VALUES (1,'"+exact+"'),(2,'-"+exact+"'),(3,001.2300),(4,-0.000),(5,.1),(6,1.230e2),(7,1.230e-2),(8,5e2)", 8)
			wantTuples(t, db, "SELECT v FROM n ORDER BY id", [][]any{{exact}, {"-" + exact}, {"1.2300"}, {"0.000"}, {"0.1"}, {"123.0"}, {"0.01230"}, {"500"}})
			execute("CREATE TABLE exact_binds(id INT,v NUMERIC)", 0)
			for i, arg := range []any{exact, "-" + exact, []byte(exact), []byte("-" + exact)} {
				execute("INSERT INTO exact_binds VALUES($1,$2)", 1, int64(i+1), arg)
			}
			bindQuery := "SELECT v FROM exact_binds ORDER BY id"
			var boundRows *sql.Rows
			var bindErr error
			if prepared {
				stmt, err := db.Prepare(bindQuery)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				boundRows, bindErr = stmt.Query()
			} else {
				boundRows, bindErr = db.Query(bindQuery)
			}
			if bindErr != nil {
				t.Fatal(bindErr)
			}
			assertNumericRows(t, boundRows, []string{"v"}, []string{"NUMERIC"}, [][]any{{exact}, {"-" + exact}, {exact}, {"-" + exact}})
			q := "SELECT 2147483647,2147483648,-2147483648,9223372036854775807,-9223372036854775808,9223372036854775808,-9223372036854775809,1.,1e0,.001,1.618_034,1e1_0,010 FROM n WHERE id=1"
			rows, err := db.Query(q)
			if err != nil {
				t.Fatal(err)
			}
			names := make([]string, 13)
			for i := range names {
				names[i] = "?column?"
			}
			assertNumericRows(t, rows, names, []string{"INT4", "INT8", "INT4", "INT8", "INT8", "NUMERIC", "NUMERIC", "NUMERIC", "NUMERIC", "NUMERIC", "NUMERIC", "NUMERIC", "INT4"}, [][]any{{int64(2147483647), int64(2147483648), int64(-2147483648), int64(9223372036854775807), int64(-9223372036854775808), "9223372036854775808", "-9223372036854775809", "1", "1", "0.001", "1.618034", "10000000000", int64(10)}})
			execute("CREATE TABLE radix(v NUMERIC)", 0)
			execute("INSERT INTO radix VALUES(0x8000000000000000),(0o1000000000000000000000),(0b1"+strings.Repeat("0", 63)+")", 3)
			wantTuples(t, db, "SELECT v FROM radix", [][]any{{"9223372036854775808"}, {"9223372036854775808"}, {"9223372036854775808"}})
			execute("CREATE TABLE sized(a NUMERIC(3,1),b NUMERIC(2,-3),c NUMERIC(3,5),d NUMERIC)", 0)
			execute("INSERT INTO sized VALUES(1.25,1500,.009994,NULL),(-1.25,-1500,.000004,1.2300)", 2)
			wantTuples(t, db, "SELECT * FROM sized ORDER BY a", [][]any{{"-1.3", "-2000", "0.00000", "1.2300"}, {"1.3", "2000", "0.00999", nil}})
			rows, err = db.Query("SELECT * FROM sized")
			if err != nil {
				t.Fatal(err)
			}
			types, err := rows.ColumnTypes()
			rows.Close()
			if err != nil {
				t.Fatal(err)
			}
			for i, want := range []struct {
				p, s int64
				ok   bool
			}{{3, 1, true}, {2, -3, true}, {3, 5, true}, {0, 0, false}} {
				p, s, ok := types[i].DecimalSize()
				if p != want.p || s != want.s || ok != want.ok || types[i].ScanType() != reflect.TypeFor[string]() || types[i].DatabaseTypeName() != "NUMERIC" {
					t.Fatal(types[i], p, s, ok)
				}
			}
			execute("CREATE TABLE ints(v BIGINT)", 0)
			execute("INSERT INTO ints VALUES(9007199254740993),(1.5),(-1.5)", 3)
			wantTuples(t, db, "SELECT v FROM ints WHERE v=9007199254740993.0", [][]any{{int64(9007199254740993)}})
			wantTuples(t, db, "SELECT v FROM ints WHERE v=9007199254740992.0", [][]any{})
			wantTuples(t, db, "SELECT v FROM ints WHERE v=9223372036854775808", [][]any{})
			execute("CREATE TABLE converted(i INT,t TEXT,f DOUBLE PRECISION)", 0)
			execute("INSERT INTO converted VALUES(1.5,1.2300,.5),(-1.5,1e2,1e0)", 2)
			wantTuples(t, db, "SELECT * FROM converted ORDER BY i", [][]any{{int64(-2), "100", float64(1)}, {int64(2), "1.2300", float64(.5)}})
			execute("CREATE TABLE real_values(id INT,v REAL)", 0)
			execute("INSERT INTO real_values VALUES(1,'16777217')", 1)
			wantTuples(t, db, "SELECT id FROM real_values WHERE v=16777217.0", [][]any{})
			wantTuples(t, db, "SELECT id FROM real_values WHERE v='16777217'", [][]any{{int64(1)}})
			wantTuples(t, db, "SELECT id FROM real_values WHERE v=1e100", [][]any{})
		})
	}
}
func TestNumericSpecialsPreparedBinds(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), "numeric-specials")
	execSQL(t, db, "CREATE TABLE n(id INT,v NUMERIC)")
	execSQL(t, db, "INSERT INTO n VALUES(1,'-Infinity'),(2,-1),(3,0),(4,1),(5,'Infinity'),(6,'NaN'),(7,'NaN'),(8,NULL)")
	wantTuples(t, db, "SELECT v FROM n ORDER BY v", [][]any{{"-Infinity"}, {"-1"}, {"0"}, {"1"}, {"Infinity"}, {"NaN"}, {"NaN"}, {nil}})
	wantTuples(t, db, "SELECT v FROM n ORDER BY v DESC", [][]any{{nil}, {"NaN"}, {"NaN"}, {"Infinity"}, {"1"}, {"0"}, {"-1"}, {"-Infinity"}})
	wantTuples(t, db, "SELECT id FROM n WHERE v='NaN' ORDER BY id", [][]any{{int64(6)}, {int64(7)}})
	wantTuples(t, db, "SELECT id FROM n WHERE v=NULL", [][]any{})
	execSQL(t, db, "CREATE TABLE sized(v NUMERIC(3,1))")
	stmt, err := db.Prepare("INSERT INTO sized VALUES($1)")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	for _, arg := range []any{"1.25", []byte("-1.25"), nil, int64(2), float64(1.5), "nan"} {
		r, err := stmt.Exec(arg)
		if err != nil {
			t.Fatal(arg, err)
		}
		n, _ := r.RowsAffected()
		if n != 1 {
			t.Fatal(n)
		}
	}
	_, err = stmt.Exec("99.95")
	typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22003", Message: "numeric field overflow", Detail: "A field with precision 3, scale 1 must round to an absolute value less than 10^2."})
	if _, err = stmt.Exec("1.2"); err != nil {
		t.Fatal(err)
	}
	wantTuples(t, db, "SELECT v FROM sized ORDER BY v", [][]any{{"-1.3"}, {"1.2"}, {"1.3"}, {"1.5"}, {"2.0"}, {"NaN"}, {nil}})
	held, err := db.Prepare("SELECT id FROM n WHERE v=$1 ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	for _, arg := range []any{"NaN", nil, "-1"} {
		rows, err := held.Query(arg)
		if err != nil {
			t.Fatal(err)
		}
		want := [][]any{}
		if arg == "NaN" {
			want = [][]any{{int64(6)}, {int64(7)}}
		}
		if arg == "-1" {
			want = [][]any{{int64(2)}}
		}
		assertNumericRows(t, rows, []string{"id"}, []string{"INT4"}, want)
	}
	_, err = db.Prepare("SELECT $1 FROM n WHERE v=$1")
	typeCoreDiagnostic(t, err, stuntgres.Error{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "numeric versus text", Position: "8"})
	for _, args := range [][]any{{"1.2500"}, nil} {
		rows, queryErr := db.Query("SELECT $1 FROM n WHERE v=$1", args...)
		if rows != nil {
			rows.Close()
			t.Fatal("failed Query exposed a cursor")
		}
		typeCoreDiagnostic(t, queryErr, stuntgres.Error{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "numeric versus text", Position: "8"})
	}
	wantTuples(t, db, "SELECT $1 FROM n WHERE id=1", [][]any{{"1.2500"}}, "1.2500")
	execSQL(t, db, "CREATE TABLE repeats(a NUMERIC(3,1),b NUMERIC(3,2))")
	execSQL(t, db, "INSERT INTO repeats VALUES($1,$1)", "1.255")
	wantTuples(t, db, "SELECT * FROM repeats", [][]any{{"1.3", "1.26"}})
}
