package stuntgres_test

import (
	"bytes"
	"context"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
)

func TestMathScalarValuesAndMetadata(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), "math-scalar-values")
	execSQL(t, db, "CREATE TABLE m(s SMALLINT,i INTEGER,b BIGINT,r REAL,d DOUBLE PRECISION,n NUMERIC(8,3))")
	execSQL(t, db, "INSERT INTO m VALUES(-17,-17,-17,-17.5,-17.5,-17.400)")
	q := "SELECT abs(s),abs(i),abs(b),abs(r),abs(d),abs(n),ceil(n),ceiling(n),floor(n),div(n,4),exp(0.0),factorial(5) FROM m"
	want := [][]any{{int64(17), int64(17), int64(17), float64(17.5), float64(17.5), "17.400", "-17", "-17", "-18", "-4", "1.0000000000000000", "120"}}
	wantTuples(t, db, q, want)
	s, err := db.Prepare(q)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	rows, err := s.Query()
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	types, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	for i, typ := range []string{"INT2", "INT4", "INT8", "FLOAT4", "FLOAT8", "NUMERIC", "NUMERIC", "NUMERIC", "NUMERIC", "NUMERIC", "NUMERIC", "NUMERIC"} {
		if types[i].DatabaseTypeName() != typ {
			t.Fatal(i, types[i])
		}
		if typ == "NUMERIC" {
			if _, _, ok := types[i].DecimalSize(); ok {
				t.Fatal("derived typmod")
			}
			if types[i].ScanType() != reflect.TypeFor[string]() {
				t.Fatal(types[i])
			}
		}
	}
	if !rows.Next() {
		t.Fatal(rows.Err())
	}
	got := make([]any, len(types))
	dst := make([]any, len(types))
	for i := range dst {
		dst[i] = &got[i]
	}
	if err := rows.Scan(dst...); err != nil || !reflect.DeepEqual(got, want[0]) {
		t.Fatal(got, err)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal(rows.Err())
	}
	rows.Close()
	for _, query := range []string{"SELECT abs(NULL),ceil(NULL),ceiling(NULL),floor(NULL),exp(NULL),div(NULL,1),factorial(NULL)", "SELECT abs(s),ceil(r),ceiling(d),floor(n),exp(n),div(n,1),factorial(b) FROM m WHERE false"} {
		r, err := db.Query(query)
		if err != nil {
			t.Fatal(err)
		}
		ts, err := r.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		for _, typ := range ts {
			if _, _, ok := typ.DecimalSize(); ok {
				t.Fatal("empty/NULL typmod leaked")
			}
		}
		r.Close()
	}
}

func TestMathScalarParameters(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), "math-scalar-parameters")
	s, err := db.Prepare("SELECT div($1,$2)")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, tc := range []struct{ a, b, want any }{{"9007199254740993", "2", "4503599627370496"}, {nil, "2", nil}, {"-9.00", "4.000", "-2"}, {"9", "0", nil}, {"9", "4", "2"}} {
		var got any
		err := s.QueryRow(tc.a, tc.b).Scan(&got)
		if tc.b == "0" {
			mathScalarDiagnostic(t, err, "22012", "division by zero", "", "")
			continue
		}
		if err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Fatal(tc, got, err)
		}
	}
	for _, tc := range []struct {
		q         string
		arg, want any
	}{{"SELECT exp($1)", "0", float64(1)}, {"SELECT factorial($1)", "20", "2432902008176640000"}, {"SELECT abs($1)", "-17.5", float64(17.5)}} {
		s, err := db.Prepare(tc.q)
		if err != nil {
			t.Fatal(err)
		}
		var got any
		err = s.QueryRow(tc.arg).Scan(&got)
		s.Close()
		if err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Fatal(tc, got, err)
		}
	}
}

func mathScalarDiagnostic(t *testing.T, err error, code, message, position, hint string) {
	t.Helper()
	var got *stuntgres.Error
	want := stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message, Position: position, Hint: hint}
	if !errors.As(err, &got) || *got != want {
		t.Fatalf("got %#v (%v) want %#v", got, err, want)
	}
}

func TestMathScalarDiagnostics(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), "math-scalar-errors")
	hint := "No function matches the given name and argument types. You might need to add explicit type casts."
	for _, name := range []string{"abs", "ceil", "ceiling", "floor", "exp", "div", "factorial"} {
		q := "SELECT " + name + "()"
		_, err := db.Prepare(q)
		mathScalarDiagnostic(t, err, "42883", "function "+name+"() does not exist", "8", hint)
	}
	for _, tc := range []struct{ q, code, message, pos, hint string }{
		{"SELECT abs('oops')", "22P02", `invalid input syntax for type double precision: "oops"`, "12", ""},
		{"SELECT ceil('oops')", "22P02", `invalid input syntax for type double precision: "oops"`, "13", ""},
		{"SELECT exp('oops')", "22P02", `invalid input syntax for type double precision: "oops"`, "12", ""},
		{"SELECT div('oops',1)", "22P02", `invalid input syntax for type numeric: "oops"`, "12", ""},
		{"SELECT factorial('oops')", "22P02", `invalid input syntax for type bigint: "oops"`, "18", ""},
		{"SELECT factorial(5.0)", "42883", "function factorial(numeric) does not exist", "8", hint},
		{"/*é*/ SELECT factorial(5.0)", "42883", "function factorial(numeric) does not exist", "14", hint},
		{"SELECT abs(" + strings.Repeat("1,", 100) + "1)", "54023", "cannot pass more than 100 arguments to a function", "8", ""},
	} {
		_, err := db.Prepare(tc.q)
		mathScalarDiagnostic(t, err, tc.code, tc.message, tc.pos, tc.hint)
	}
	for _, tc := range []struct{ q, code, message string }{
		{"SELECT div(1,0)", "22012", "division by zero"}, {"SELECT div(NULL,div(1,0))", "22012", "division by zero"},
		{"SELECT factorial(-1)", "22003", "factorial of a negative number is undefined"}, {"SELECT factorial(32178)", "22003", "value overflows numeric format"},
		{"SELECT exp(6000.0)", "22003", "value overflows numeric format"}, {"SELECT abs(-2147483648)", "22003", "integer out of range"},
	} {
		s, err := db.Prepare(tc.q)
		if err != nil {
			t.Fatal("provider evaluated during Prepare", tc.q, err)
		}
		_, err = s.Exec()
		s.Close()
		mathScalarDiagnostic(t, err, tc.code, tc.message, "", "")
		_, err = db.Exec(tc.q)
		mathScalarDiagnostic(t, err, tc.code, tc.message, "", "")
	}
}

func TestMathScalarConsumersAndControls(t *testing.T) {
	ctx := context.Background()
	db := openLocal(t, stuntgres.NewDriver(), "math-scalar-controls")
	execSQL(t, db, "CREATE TABLE m(id INT,n NUMERIC,d NUMERIC,i BIGINT)")
	execSQL(t, db, "INSERT INTO m VALUES(1,-9.00,4,5),(2,9.00,0,-1),(3,NULL,4,NULL)")
	wantTuples(t, db, "SELECT abs(floor(n)) FROM m ORDER BY id", [][]any{{"9"}, {"9"}, {nil}})
	wantTuples(t, db, "SELECT factorial(count(*)) FROM m", [][]any{{"6"}})
	wantTuples(t, db, "SELECT abs(n),count(*) FROM m GROUP BY 1 ORDER BY 1", [][]any{{"9.00", int64(2)}, {nil, int64(1)}})
	execSQL(t, db, "CREATE TABLE copied(n NUMERIC)")
	for _, q := range []string{"UPDATE m SET n=div(n,d)", "INSERT INTO copied SELECT div(n,d) FROM m", "UPDATE m SET n=factorial(i)"} {
		_, err := db.Exec(q)
		if err == nil {
			t.Fatal("non-atomic error succeeded", q)
		}
		wantTuples(t, db, "SELECT n FROM m ORDER BY id", [][]any{{"-9.00"}, {"9.00"}, {nil}})
		wantTuples(t, db, "SELECT n FROM copied", [][]any{})
	}
	result, err := db.Exec("UPDATE m SET n=abs(n)")
	if err != nil {
		t.Fatal(err)
	}
	count, err := result.RowsAffected()
	if err != nil || count != 3 {
		t.Fatal(count, err)
	}
	execSQL(t, db, "CREATE VIEW scalar_view AS SELECT id,ceil(n) AS c,ceiling(n) AS cc,floor(n) AS f,div(n,4) AS q,exp(0.0) AS e,factorial(5) AS fac FROM m")
	execSQL(t, db, "INSERT INTO copied SELECT div(n,4) FROM m")
	held, err := db.Query("SELECT n FROM copied")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	var dump bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	wantTuples(t, db, "SELECT c,cc,f,q,e,fac FROM scalar_view ORDER BY id", [][]any{{"9", "9", "9", "2", "1.0000000000000000", "120"}, {"9", "9", "9", "2", "1.0000000000000000", "120"}, {nil, nil, nil, nil, "1.0000000000000000", "120"}})
	if !held.Next() {
		t.Fatal(held.Err())
	}
	var old string
	if err := held.Scan(&old); err != nil || old != "2" {
		t.Fatal(old, err)
	}
	var redump bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &redump); err != nil || redump.String() != dump.String() {
		t.Fatal("deterministic roundtrip", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = db.ExecContext(cancelled, "UPDATE m SET n=exp(n)")
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	wantTuples(t, db, "SELECT n FROM m ORDER BY id", [][]any{{"9.00"}, {"9.00"}, {nil}})
	var zero float64
	if err := db.QueryRow("SELECT ceil('-0.5')").Scan(&zero); err != nil || zero != 0 || !math.Signbit(zero) {
		t.Fatal(zero, err)
	}
}
