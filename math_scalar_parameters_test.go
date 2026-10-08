package stuntgres_test

import (
	"reflect"
	"testing"

	"stuntgres"
)

func TestMathScalarHeldParameterContracts(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), "math-scalar-held")
	for _, tc := range []struct {
		query, typ, bad, message string
		good, want               any
	}{
		{"SELECT factorial($1)", "NUMERIC", "1.5", `invalid input syntax for type bigint: "1.5"`, int64(5), "120"},
		{"SELECT exp($1)", "FLOAT8", "oops", `invalid input syntax for type double precision: "oops"`, "0", float64(1)},
		{"SELECT div($1,$2)", "NUMERIC", "oops", `invalid input syntax for type numeric: "oops"`, "9.00", "4"},
	} {
		s, err := db.Prepare(tc.query)
		if err != nil {
			t.Fatal(err)
		}
		args := []any{tc.bad}
		if tc.query == "SELECT div($1,$2)" {
			args = append(args, "2")
		}
		_, err = s.Exec(args...)
		mathScalarDiagnostic(t, err, "22P02", tc.message, "", "")
		for _, input := range []any{tc.good, nil, tc.good} {
			args[0] = input
			rows, err := s.Query(args...)
			if err != nil {
				t.Fatal(err)
			}
			types, err := rows.ColumnTypes()
			if err != nil || types[0].DatabaseTypeName() != tc.typ {
				t.Fatal(types, err)
			}
			if !rows.Next() {
				t.Fatal(rows.Err())
			}
			var got any
			err = rows.Scan(&got)
			want := tc.want
			if input == nil {
				want = nil
			}
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatal(got, want, err)
			}
			if rows.Next() || rows.Err() != nil {
				t.Fatal(rows.Err())
			}
			rows.Close()
		}
		s.Close()
	}
	execSQL(t, db, "CREATE TABLE names(abs SMALLINT,ceil REAL,ceiling NUMERIC,floor INT,factorial BIGINT)")
	execSQL(t, db, "INSERT INTO names VALUES(-5,-1.5,-1.50,-1,5)")
	wantTuples(t, db, `SELECT floor,"abs"(abs),ceil(ceil),ceiling(ceiling),factorial(abs(abs)),factorial(factorial) FROM names`, [][]any{{int64(-1), int64(5), float64(-1), "-1", "120", "120"}})
}
