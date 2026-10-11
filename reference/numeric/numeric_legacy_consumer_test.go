package reference_test

import (
	"bytes"
	"database/sql"
	"database/sql/driver"
	"io"
	"reflect"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

// Preserves Context and ordinary DB/held-statement routes from numeric smoke.
func TestReferenceNumericLegacyConsumerRoutes(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) { numericLegacyConsumer(t, p, backend) })
	}
}
func numericLegacyConsumer(t *testing.T, p *fixture.Pair, backend fixture.Backend) {
	ctx := p.Ctx
	db := backend.DB
	preparedControls := false
	exec := func(q string, count int64, args ...any) {
		t.Helper()
		var r sql.Result
		var err error
		if preparedControls {
			statement, e := db.PrepareContext(ctx, q)
			if e != nil {
				t.Fatal("successful conversion Prepare", q, e)
			}
			defer statement.Close()
			r, err = statement.ExecContext(ctx, args...)
		} else {
			r, err = db.ExecContext(ctx, q, args...)
		}
		if err != nil {
			t.Fatal(q, err)
		}
		n, err := r.RowsAffected()
		if err != nil || n != count {
			t.Fatal(n, err)
		}
	}
	check := func(q string, types []string, want [][]any, args ...any) {
		t.Helper()
		var rows *sql.Rows
		var err error
		if preparedControls {
			statement, e := db.PrepareContext(ctx, q)
			if e != nil {
				t.Fatal("successful comparison Prepare", q, e)
			}
			defer statement.Close()
			rows, err = statement.QueryContext(ctx, args...)
		} else {
			rows, err = db.QueryContext(ctx, q, args...)
		}
		if err != nil {
			t.Fatal(q, err)
		}
		defer rows.Close()
		metadata, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		for i, name := range types {
			if metadata[i].DatabaseTypeName() != name {
				t.Fatal(metadata[i], name)
			}
		}
		got := [][]any{}
		for rows.Next() {
			row := make([]any, len(types))
			dst := make([]any, len(types))
			for i := range dst {
				dst[i] = &row[i]
			}
			if err := rows.Scan(dst...); err != nil {
				t.Fatal(err)
			}
			for i := range row {
				fixture.AssertRawValue(t, backend.PG, types[i], row[i])
				row[i] = fixture.Normalize(row[i])
			}
			got = append(got, row)
		}
		if rows.Err() != nil || !reflect.DeepEqual(got, want) {
			t.Fatal(q, got, want, rows.Err())
		}
	}
	diagnostic := func(err error, want stuntgres.Error) {
		t.Helper()
		fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: want.Code, Message: want.Message, Detail: want.Detail, Hint: want.Hint, Position: want.Position, Context: want.Context, Schema: want.Schema, Table: want.Table, Column: want.Column, DataType: want.DataType, Constraint: want.Constraint})
	}
	exact := "12345678901234567890.1234567890123456789000"
	exec("CREATE TABLE n(id INT,v NUMERIC,a NUMERIC(2,-3),b NUMERIC(3,5))", 0)
	exec("INSERT INTO n VALUES(1,$1,1500,.009994),(2,'NaN',NULL,NULL),(3,'Infinity',NULL,NULL),(4,'-Infinity',NULL,NULL),(5,NULL,NULL,NULL)", 5, []byte(exact))
	want := [][]any{{int64(1), exact, "2000", "0.00999"}, {int64(2), "NaN", nil, nil}, {int64(3), "Infinity", nil, nil}, {int64(4), "-Infinity", nil, nil}, {int64(5), nil, nil, nil}}
	check("SELECT * FROM n ORDER BY id", []string{"INT4", "NUMERIC", "NUMERIC", "NUMERIC"}, want)
	check("SELECT 9223372036854775808,0x8000000000000000,1.2300 FROM n WHERE id=1", []string{"NUMERIC", "NUMERIC", "NUMERIC"}, [][]any{{"9223372036854775808", "9223372036854775808", "1.2300"}})
	check("SELECT v FROM n ORDER BY v", []string{"NUMERIC"}, [][]any{{"-Infinity"}, {exact}, {"Infinity"}, {"NaN"}, {nil}})
	check("SELECT id FROM n WHERE v='NaN'", []string{"INT4"}, [][]any{{int64(2)}})
	rows, err := db.Query("SELECT * FROM n")
	if err != nil {
		t.Fatal(err)
	}
	types, err := rows.ColumnTypes()
	rows.Close()
	if err != nil {
		t.Fatal(err)
	}
	precision, scale, ok := types[2].DecimalSize()
	scan := reflect.TypeFor[string]()
	if backend.PG {
		scan = reflect.TypeFor[any]()
	}
	wantScale := int64(-3)
	if backend.PG {
		wantScale = 2045
	}
	if !ok || precision != 2 || scale != wantScale || types[1].ScanType() != scan {
		t.Fatal(types, precision, scale, ok)
	}
	exec("CREATE TABLE sized(v NUMERIC(3,1))", 0)
	stmt, err := db.Prepare("INSERT INTO sized VALUES($1)")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	for _, arg := range []any{"1.25", "-1.25", nil} {
		r, err := stmt.Exec(arg)
		if err != nil {
			t.Fatal(err)
		}
		n, _ := r.RowsAffected()
		if n != 1 {
			t.Fatal(n)
		}
	}
	overflow := stuntgres.Error{Code: "22003", Message: "numeric field overflow", Detail: "A field with precision 3, scale 1 must round to an absolute value less than 10^2."}
	_, err = stmt.Exec("99.95")
	diagnostic(err, overflow)
	if _, err = stmt.Exec("1.2"); err != nil {
		t.Fatal(err)
	}
	check("SELECT v FROM sized ORDER BY v", []string{"NUMERIC"}, [][]any{{"-1.3"}, {"1.2"}, {"1.3"}, {nil}})
	fixed, err := db.Prepare("INSERT INTO sized VALUES(99.95)")
	if err != nil {
		t.Fatal("Prepare sized constant", err)
	}
	_, err = fixed.Exec()
	fixed.Close()
	diagnostic(err, overflow)
	_, err = db.Exec("INSERT INTO sized VALUES(1.2),(99.95)")
	diagnostic(err, overflow)
	check("SELECT v FROM sized ORDER BY v", []string{"NUMERIC"}, [][]any{{"-1.3"}, {"1.2"}, {"1.3"}, {nil}})
	exec("CREATE TABLE ints(v BIGINT)", 0)
	_, err = db.Exec("INSERT INTO ints VALUES(1),(9223372036854775808)")
	diagnostic(err, stuntgres.Error{Code: "22003", Message: "bigint out of range"})
	check("SELECT v FROM ints", []string{"INT8"}, [][]any{})
	exec("CREATE TABLE carry4(v INTEGER)", 0)
	exec("INSERT INTO carry4 VALUES(2147483647.49),(-2147483648.49)", 2)
	exec("INSERT INTO ints VALUES(9223372036854775807.49),(-9223372036854775808.49)", 2)
	exec("CREATE TABLE conversion_floats(id INT,r REAL,d DOUBLE PRECISION)", 0)
	exec("INSERT INTO conversion_floats VALUES(99,1.0,1.0)", 1)
	for _, prepared := range []bool{false, true} {
		preparedControls = prepared
		controls := "float_controls_direct"
		if prepared {
			controls = "float_controls_prepared"
		}
		small := "small_controls_direct"
		if prepared {
			small = "small_controls_prepared"
		}
		exec("CREATE TABLE "+small+"(id INT,v SMALLINT,n NUMERIC(5,0))", 0)
		exec("INSERT INTO "+small+" VALUES(0,99,99),(1,32767.49,32767),(2,-32768.49,-32768),(3,1.5,2),(4,-1.5,-2),(5,NULL,NULL)", 6)
		check("SELECT v FROM "+small+" WHERE v=32767.0", []string{"INT2"}, [][]any{{int64(32767)}})
		check("SELECT v FROM "+small+" WHERE v=32767.1", []string{"INT2"}, [][]any{})
		check("SELECT v FROM "+small+" WHERE v=32768.0", []string{"INT2"}, [][]any{})
		check("SELECT v FROM "+small+" WHERE v=-32768.0", []string{"INT2"}, [][]any{{int64(-32768)}})
		check("SELECT v FROM "+small+" WHERE v=NULL", []string{"INT2"}, [][]any{})
		exec("CREATE TABLE "+controls+"(id INT,r REAL,d DOUBLE PRECISION)", 0)
		for _, tc := range []struct{ id, literal string }{{"0", "1.5"}, {"1", "-1.5"}, {"2", "0e-400"}} {
			exec("INSERT INTO "+controls+" VALUES("+tc.id+","+tc.literal+","+tc.literal+")", 1)
		}
		check("SELECT * FROM "+controls+" ORDER BY id", []string{"INT4", "FLOAT4", "FLOAT8"}, [][]any{{int64(0), float64(1.5), float64(1.5)}, {int64(1), float64(-1.5), float64(-1.5)}, {int64(2), float64(0), float64(0)}})
		failExec := func(q string, want stuntgres.Error) {
			t.Helper()
			var result sql.Result
			var err error
			if prepared {
				statement, e := db.Prepare(q)
				if e != nil {
					t.Fatal("conversion failed Prepare", e)
				}
				result, err = statement.Exec()
				statement.Close()
			} else {
				result, err = db.Exec(q)
			}
			if result != nil {
				t.Fatal("failed conversion returned affected count")
			}
			diagnostic(err, want)
		}
		for _, literal := range []string{"32767.5", "-32768.5"} {
			failExec("INSERT INTO "+small+" VALUES(6,1.5,1),(7,"+literal+",1)", stuntgres.Error{Code: "22003", Message: "smallint out of range"})
			check("SELECT * FROM "+small+" ORDER BY id", []string{"INT4", "INT2", "NUMERIC"}, [][]any{{int64(0), int64(99), "99"}, {int64(1), int64(32767), "32767"}, {int64(2), int64(-32768), "-32768"}, {int64(3), int64(2), "2"}, {int64(4), int64(-2), "-2"}, {int64(5), nil, nil}})
		}
		for _, tc := range []struct{ table, literal, message string }{
			{"carry4", "2147483647.5", "integer out of range"},
			{"carry4", "-2147483648.5", "integer out of range"},
			{"ints", "9223372036854775807.5", "bigint out of range"},
			{"ints", "-9223372036854775808.5", "bigint out of range"},
		} {
			failExec("INSERT INTO "+tc.table+" VALUES(1.5),("+tc.literal+")", stuntgres.Error{Code: "22003", Message: tc.message})
			check("SELECT v FROM carry4 ORDER BY v", []string{"INT4"}, [][]any{{int64(-2147483648)}, {int64(2147483647)}})
			check("SELECT v FROM ints ORDER BY v", []string{"INT8"}, [][]any{{int64(-9223372036854775808)}, {int64(9223372036854775807)}})
		}
		for _, tc := range []struct{ column, literal, text, name string }{
			{"r", "1e40", "1" + strings.Repeat("0", 40), "real"},
			{"r", "1e-50", "0." + strings.Repeat("0", 49) + "1", "real"},
			{"d", "1e400", "1" + strings.Repeat("0", 400), "double precision"},
			{"d", "1e-400", "0." + strings.Repeat("0", 399) + "1", "double precision"},
		} {
			failExec("INSERT INTO conversion_floats(id,"+tc.column+") VALUES(1,.5),(2,"+tc.literal+")", stuntgres.Error{Code: "22003", Message: `"` + tc.text + `" is out of range for type ` + tc.name})
			check("SELECT * FROM conversion_floats", []string{"INT4", "FLOAT4", "FLOAT8"}, [][]any{{int64(99), float64(1), float64(1)}})
		}
		for _, column := range []string{"r", "d"} {
			name := "FLOAT4"
			if column == "d" {
				name = "FLOAT8"
			}
			check("SELECT "+column+" FROM conversion_floats WHERE "+column+"=1.0", []string{name}, [][]any{{float64(1)}})
			check("SELECT "+column+" FROM conversion_floats WHERE "+column+"=1e100", []string{name}, [][]any{})
			for _, tc := range []struct{ literal, text string }{
				{"1e400", "1" + strings.Repeat("0", 400)},
				{"1e-400", "0." + strings.Repeat("0", 399) + "1"},
			} {
				q := "SELECT id FROM conversion_floats WHERE " + column + "=" + tc.literal
				var rows *sql.Rows
				var err error
				if prepared {
					statement, e := db.Prepare(q)
					if e != nil {
						t.Fatal("comparison failed Prepare", e)
					}
					rows, err = statement.Query()
					statement.Close()
				} else {
					rows, err = db.Query(q)
				}
				if rows != nil {
					rows.Close()
					t.Fatal("failed comparison exposed cursor")
				}
				diagnostic(err, stuntgres.Error{Code: "22003", Message: `"` + tc.text + `" is out of range for type double precision`})
			}
		}
	}
	preparedControls = false
	if backend.PG {
		return
	} // Native-only controls; shared SQL assertions above run on both.
	raw, err := p.NativeConnector.Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	held, err := raw.(driver.QueryerContext).QueryContext(ctx, "SELECT * FROM n ORDER BY id", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	var dump bytes.Buffer
	if err = stuntgres.Dump(ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	if err = stuntgres.Reset(ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err = stuntgres.Restore(ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	check("SELECT * FROM n ORDER BY id", []string{"INT4", "NUMERIC", "NUMERIC", "NUMERIC"}, want)
	var redump bytes.Buffer
	if err = stuntgres.Dump(ctx, p.Native, &redump); err != nil || redump.String() != dump.String() {
		t.Fatal(err)
	}
	dst := make([]driver.Value, 4)
	for _, row := range want {
		if err = held.Next(dst); err != nil {
			t.Fatal(err)
		}
		for i, value := range row {
			if dst[i] != value {
				t.Fatal(dst, row)
			}
		}
	}
	if err = held.Next(dst); err != io.EOF {
		t.Fatal(err)
	}
	err = stuntgres.Restore(ctx, p.Native, strings.NewReader("CREATE TABLE staged(v NUMERIC(3,1)); INSERT INTO staged VALUES(1.2),(99.95)"))
	diagnostic(err, overflow)
	check("SELECT * FROM n ORDER BY id", []string{"INT4", "NUMERIC", "NUMERIC", "NUMERIC"}, want)
}
