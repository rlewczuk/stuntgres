package stuntgres_test

import (
	"bytes"
	"context"
	"database/sql/driver"
	"errors"
	"io"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"stuntgres"
)

// This public witness deliberately depends only on existing driver APIs, so
// the failing-before run observes missing behavior rather than missing symbols.
func TestSmallintEndpoints(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), "smallint-endpoints")
	execSQL(t, db, "CREATE TABLE typed(s SMALLINT NOT NULL,n int2)")
	result, err := db.Exec("INSERT INTO typed VALUES(-32768,NULL),(32767,NULL)")
	if err != nil {
		t.Fatal(err)
	}
	if count, err := result.RowsAffected(); err != nil || count != 2 {
		t.Fatal(count, err)
	}
	wantTuples(t, db, "SELECT * FROM typed ORDER BY s", [][]any{{int64(-32768), nil}, {int64(32767), nil}})
	rows, err := db.Query("SELECT * FROM typed WHERE s=0")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	types, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	for i, column := range types {
		nullable, known := column.Nullable()
		if column.DatabaseTypeName() != "INT2" || column.ScanType() != reflect.TypeFor[int64]() || !known || nullable != (i == 1) {
			t.Fatalf("column %d: type=%s scan=%v nullable=%v known=%v", i, column.DatabaseTypeName(), column.ScanType(), nullable, known)
		}
	}
}

func TestSmallintPreparedInputsAndAliases(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), "smallint-inputs")
	for i, declaration := range []string{"SMALLINT", "int2", `"int2"`} {
		stmt, err := db.Prepare("CREATE TABLE alias" + strconv.Itoa(i) + "(s " + declaration + ")")
		if err != nil {
			t.Fatal(err)
		}
		_, err = stmt.Exec()
		stmt.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	execSQL(t, db, "CREATE TABLE typed(s SMALLINT,n int2)")
	stmt, err := db.Prepare("INSERT INTO typed VALUES($1,$2)")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	owned := []byte("-32768")
	if _, err = stmt.Exec(owned, nil); err != nil {
		t.Fatal(err)
	}
	owned[0] = '0'
	for _, raw := range []any{int64(32767), "0x_FF", " +0007 ", "010", "08", "0o17", "0b10_10", "1_000", "-0X_10", "-0", "\t\n\r\f\v-8\t"} {
		if _, err = stmt.Exec(raw, nil); err != nil {
			t.Fatal(raw, err)
		}
	}
	execSQL(t, db, "INSERT INTO typed(s) VALUES(9)")
	wantTuples(t, db, "SELECT * FROM typed ORDER BY s", [][]any{{int64(-32768), nil}, {int64(-16), nil}, {int64(-8), nil}, {int64(0), nil}, {int64(7), nil}, {int64(8), nil}, {int64(9), nil}, {int64(10), nil}, {int64(10), nil}, {int64(15), nil}, {int64(255), nil}, {int64(1000), nil}, {int64(32767), nil}})
	for _, q := range []string{"SELECT s FROM typed WHERE s=32768", "SELECT s FROM typed WHERE s=2147483648"} {
		wantTuples(t, db, q, [][]any{})
	}
	wantTuples(t, db, "SELECT s FROM typed WHERE s=$1", [][]any{}, nil)
	execSQL(t, db, "CREATE TABLE repeats(a SMALLINT,b SMALLINT)")
	repeat, err := db.Prepare("INSERT INTO repeats VALUES($1,$1)")
	if err != nil {
		t.Fatal(err)
	}
	defer repeat.Close()
	for _, n := range []int{7, 8} {
		r, err := repeat.Exec(n)
		if err != nil {
			t.Fatal(err)
		}
		if count, err := r.RowsAffected(); err != nil || count != 1 {
			t.Fatal(count, err)
		}
	}
	wantTuples(t, db, "SELECT * FROM repeats ORDER BY a", [][]any{{int64(7), int64(7)}, {int64(8), int64(8)}})
}

func TestSmallintDiagnosticsAndAtomicity(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), "smallint-errors")
	execSQL(t, db, "CREATE TABLE failures(i SMALLINT NOT NULL)")
	for _, tc := range []struct{ text, code string }{{"oops", "22P02"}, {"32768", "22003"}, {"-32769", "22003"}, {"32768x", "22P02"}, {"327680x", "22003"}, {"0x8000x", "22P02"}, {"0x80000x", "22003"}} {
		message := "invalid input syntax for type smallint: \"" + tc.text + "\""
		if tc.code == "22003" {
			message = "value \"" + tc.text + "\" is out of range for type smallint"
		}
		for _, q := range []string{"INSERT INTO failures(i) VALUES('" + tc.text + "')", "/*é*/ SELECT i FROM failures WHERE i='" + tc.text + "'"} {
			position := "32"
			if strings.HasPrefix(q, "/*") {
				position = "38"
			}
			stmt, err := db.Prepare(q)
			if stmt != nil {
				stmt.Close()
				t.Fatal("quoted input prepared")
			}
			typeCoreDiagnostic(t, err, stuntgres.Error{Code: tc.code, Message: message, Position: position})
			if strings.HasPrefix(q, "/*") {
				rows, err := db.Query(q)
				if rows != nil {
					rows.Close()
					t.Fatal("error cursor")
				}
				typeCoreDiagnostic(t, err, stuntgres.Error{Code: tc.code, Message: message, Position: position})
			} else {
				_, err := db.Exec(q)
				typeCoreDiagnostic(t, err, stuntgres.Error{Code: tc.code, Message: message, Position: position})
			}
		}
		for _, raw := range []any{tc.text, []byte(tc.text)} {
			for _, q := range []string{"INSERT INTO failures(i) VALUES($1)", "/*é*/ SELECT i FROM failures WHERE i=$1"} {
				stmt, err := db.Prepare(q)
				if err != nil {
					t.Fatal(err)
				}
				if strings.HasPrefix(q, "/*") {
					rows, err := stmt.Query(raw)
					if rows != nil {
						rows.Close()
						t.Fatal("error cursor")
					}
					typeCoreDiagnostic(t, err, stuntgres.Error{Code: tc.code, Message: message})
					rows, err = db.Query(q, raw)
					if rows != nil {
						rows.Close()
						t.Fatal("error cursor")
					}
					typeCoreDiagnostic(t, err, stuntgres.Error{Code: tc.code, Message: message})
				} else {
					_, err = stmt.Exec(raw)
					typeCoreDiagnostic(t, err, stuntgres.Error{Code: tc.code, Message: message})
					_, err = db.Exec(q, raw)
					typeCoreDiagnostic(t, err, stuntgres.Error{Code: tc.code, Message: message})
				}
				stmt.Close()
			}
		}
	}
	execSQL(t, db, "INSERT INTO failures VALUES(99)")
	unchanged := func() { wantTuples(t, db, "SELECT * FROM failures", [][]any{{int64(99)}}) }
	for _, n := range []int64{-32769, 32768} {
		for _, prepared := range []bool{false, true} {
			q := "INSERT INTO failures VALUES(1),($1)"
			var err error
			if prepared {
				stmt, e := db.Prepare(q)
				if e != nil {
					t.Fatal(e)
				}
				_, err = stmt.Exec(n)
				stmt.Close()
			} else {
				_, err = db.Exec(q, n)
			}
			typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22003", Message: "value \"" + strconv.FormatInt(n, 10) + "\" is out of range for type smallint"})
			unchanged()
		}
	}
	for _, text := range []string{"32768", "-32769", "2147483648"} {
		q := "INSERT INTO failures VALUES(1),(" + text + ")"
		stmt, err := db.Prepare(q)
		if err != nil {
			t.Fatal("typed narrowing failed at prepare", err)
		}
		_, err = stmt.Exec()
		stmt.Close()
		typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22003", Message: "smallint out of range"})
		unchanged()
		_, err = db.Exec(q)
		typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22003", Message: "smallint out of range"})
		unchanged()
	}
	_, err := db.Exec("INSERT INTO failures VALUES(1),('oops')")
	typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type smallint: "oops"`, Position: "33"})
	unchanged()
	_, err = db.Exec("INSERT INTO failures VALUES(1),(NULL)")
	typeCoreDiagnostic(t, err, stuntgres.Error{Code: "23502", Message: `null value in column "i" of relation "failures" violates not-null constraint`, Detail: "Failing row contains (null).", Schema: "public", Table: "failures", Column: "i"})
	unchanged()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = db.ExecContext(ctx, "INSERT INTO failures VALUES(1),(2)")
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	unchanged()
	for _, tc := range []struct{ q, code, message, hint, position string }{
		{`CREATE TABLE bad(i "smallint")`, "42704", `type "smallint" does not exist`, "", "20"},
		{`CREATE TABLE bad(i "INT2")`, "42704", `type "INT2" does not exist`, "", "20"},
		{"INSERT INTO failures(i) VALUES(TRUE)", "42804", `column "i" is of type smallint but expression is of type boolean`, "You will need to rewrite or cast the expression.", "32"},
		{"SELECT i FROM failures WHERE i=TRUE", "42883", "operator does not exist: smallint = boolean", "No operator matches the given name and argument types. You might need to add explicit type casts.", "31"},
		{"INSERT INTO failures VALUES()", "42601", `syntax error at or near ")"`, "", "29"},
	} {
		_, err := db.Exec(tc.q)
		typeCoreDiagnostic(t, err, stuntgres.Error{Code: tc.code, Message: tc.message, Hint: tc.hint, Position: tc.position})
		unchanged()
	}
}

func TestSmallintControlsAndRetainedCursor(t *testing.T) {
	d := stuntgres.NewDriver()
	db := openLocal(t, d, "smallint-controls")
	execSQL(t, db, "CREATE TABLE typed(s SMALLINT NOT NULL,i INTEGER,b BIGINT,n int2)")
	execSQL(t, db, "CREATE TABLE empty(s SMALLINT)")
	execSQL(t, db, "INSERT INTO typed VALUES(-32768,-2147483648,-9223372036854775808,NULL),(32767,2147483647,9223372036854775807,NULL),(0,NULL,NULL,NULL)")
	raw, err := d.Open("smallint-controls")
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	held, err := raw.(driver.QueryerContext).QueryContext(context.Background(), "SELECT * FROM typed ORDER BY s", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	var dump bytes.Buffer
	if err = stuntgres.Dump(context.Background(), db, &dump); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"SMALLINT", "INTEGER", "BIGINT"} {
		if !strings.Contains(dump.String(), name) {
			t.Fatal(dump.String())
		}
	}
	if err = stuntgres.Reset(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if err = stuntgres.Restore(context.Background(), db, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	var redump bytes.Buffer
	if err = stuntgres.Dump(context.Background(), db, &redump); err != nil || redump.String() != dump.String() {
		t.Fatal(redump.String(), err)
	}
	want := [][]any{{int64(-32768), int64(-2147483648), int64(-9223372036854775808), nil}, {int64(0), nil, nil, nil}, {int64(32767), int64(2147483647), int64(9223372036854775807), nil}}
	wantTuples(t, db, "SELECT * FROM typed ORDER BY s", want)
	for i, name := range []string{"INT2", "INT4", "INT8", "INT2"} {
		if held.(driver.RowsColumnTypeDatabaseTypeName).ColumnTypeDatabaseTypeName(i) != name || held.(driver.RowsColumnTypeScanType).ColumnTypeScanType(i) != reflect.TypeFor[int64]() {
			t.Fatal("retained width changed")
		}
		nullable, known := held.(driver.RowsColumnTypeNullable).ColumnTypeNullable(i)
		if !known || nullable != (i != 0) {
			t.Fatal(nullable, known)
		}
	}
	dst := make([]driver.Value, 4)
	for _, row := range want {
		if err = held.Next(dst); err != nil {
			t.Fatal(err)
		}
		for i, v := range row {
			if dst[i] != v {
				t.Fatal(dst, row)
			}
		}
	}
	if err = held.Next(dst); err != io.EOF {
		t.Fatal(err)
	}
	for _, script := range []string{"CREATE TABLE replacement(s SMALLINT); INSERT INTO replacement VALUES(1),(32768)", "CREATE TABLE replacement(s SMALLINT); INSERT INTO replacement VALUES(1),('oops')"} {
		if err = stuntgres.Restore(context.Background(), db, strings.NewReader(script)); err == nil {
			t.Fatal("bad restore accepted")
		}
		wantTuples(t, db, "SELECT * FROM typed ORDER BY s", want)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err = stuntgres.Restore(ctx, db, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	wantTuples(t, db, "SELECT * FROM typed ORDER BY s", want)
	for _, q := range []string{"SELECT * FROM typed", "SELECT * FROM empty"} {
		rows, err := db.Query(q)
		if err != nil {
			t.Fatal(err)
		}
		types, err := rows.ColumnTypes()
		rows.Close()
		if err != nil {
			t.Fatal(err)
		}
		for i, c := range types {
			name := []string{"INT2", "INT4", "INT8", "INT2"}[i]
			if c.DatabaseTypeName() != name || c.ScanType() != reflect.TypeFor[int64]() {
				t.Fatal(c)
			}
		}
	}
}

func TestSmallintAuthorizationPrecedence(t *testing.T) {
	ctx := context.Background()
	db := openLocal(t, stuntgres.NewDriver(), "smallint-authority")
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	for _, q := range []string{"CREATE ROLE reader", "CREATE TABLE secret(i SMALLINT)", "INSERT INTO secret VALUES(7)", "SET SESSION AUTHORIZATION reader"} {
		if _, err = conn.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	defer conn.ExecContext(ctx, "RESET SESSION AUTHORIZATION")
	for _, prepared := range []bool{false, true} {
		for _, tc := range []struct {
			q, code, message string
			args             []any
		}{
			{"INSERT INTO secret VALUES(9)", "42501", "permission denied for table secret", nil},
			{"INSERT INTO secret VALUES($1)", "42501", "permission denied for table secret", []any{int64(9)}},
			{"INSERT INTO secret VALUES($1)", "22P02", `invalid input syntax for type smallint: "oops"`, []any{"oops"}},
			{"INSERT INTO secret VALUES(32768)", "22003", "smallint out of range", nil},
		} {
			var err error
			if prepared {
				stmt, e := conn.PrepareContext(ctx, tc.q)
				if e != nil {
					t.Fatal(e)
				}
				_, err = stmt.ExecContext(ctx, tc.args...)
				stmt.Close()
			} else {
				_, err = conn.ExecContext(ctx, tc.q, tc.args...)
			}
			typeCoreDiagnostic(t, err, stuntgres.Error{Code: tc.code, Message: tc.message})
		}
	}
	if _, err = conn.ExecContext(ctx, "RESET SESSION AUTHORIZATION"); err != nil {
		t.Fatal(err)
	}
	var n int64
	if err = conn.QueryRowContext(ctx, "SELECT i FROM secret").Scan(&n); err != nil || n != 7 {
		t.Fatal(n, err)
	}
}

func TestSmallintRemainingIntegerDiagnostics(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), "smallint-wide-diagnostics")
	execSQL(t, db, "CREATE TABLE wide(i INTEGER,b BIGINT)")
	for _, tc := range []struct{ column, name, text, code string }{
		{"i", "integer", "oops", "22P02"}, {"b", "bigint", "oops", "22P02"},
		{"i", "integer", "2147483648", "22003"}, {"i", "integer", "-2147483649", "22003"},
		{"b", "bigint", "9223372036854775808", "22003"}, {"b", "bigint", "-9223372036854775809", "22003"},
	} {
		message := "invalid input syntax for type " + tc.name + ": \"" + tc.text + "\""
		if tc.code == "22003" {
			message = "value \"" + tc.text + "\" is out of range for type " + tc.name
		}
		q := "INSERT INTO wide(" + tc.column + ") VALUES($1)"
		stmt, err := db.Prepare(q)
		if err != nil {
			t.Fatal(err)
		}
		for _, raw := range []any{tc.text, []byte(tc.text)} {
			_, err = stmt.Exec(raw)
			typeCoreDiagnostic(t, err, stuntgres.Error{Code: tc.code, Message: message})
			_, err = db.Exec(q, raw)
			typeCoreDiagnostic(t, err, stuntgres.Error{Code: tc.code, Message: message})
		}
		stmt.Close()
	}
	wantTuples(t, db, "SELECT * FROM wide", [][]any{})
}
