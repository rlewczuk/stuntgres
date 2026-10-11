package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func numericArithmeticFailures(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	for i, tc := range []struct{ expr, code, message string }{
		{"1::numeric/0", "22012", "division by zero"}, {"1::numeric%0", "22012", "division by zero"},
		{"'Infinity'::numeric/0", "22012", "division by zero"}, {"'-Infinity'::numeric%0", "22012", "division by zero"},
		{"0::numeric ^ -1", "2201F", "zero raised to a negative power is undefined"}, {"0::numeric ^ '-Infinity'::numeric", "2201F", "zero raised to a negative power is undefined"},
		{"(-2)::numeric^0.5", "2201F", "a negative number raised to a non-integer power yields a complex result"}, {"'-Infinity'::numeric^0.5", "2201F", "a negative number raised to a non-integer power yields a complex result"},
		{"9e131071::numeric+9e131071::numeric", "22003", "value overflows numeric format"}, {"1e131071::numeric/0.1", "22003", "value overflows numeric format"}, {"1e131071::numeric*10", "22003", "value overflows numeric format"},
		{"(9e131071::numeric+9e131071::numeric)-9e131071::numeric", "22003", "value overflows numeric format"}, {"10::numeric^131072", "22003", "value overflows numeric format"},
	} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("runtime%d/prepared%t", i, prepared), func(t *testing.T) {
				s := fixture.ImplicitError("SELECT "+tc.expr+" FROM unit", fixture.Diagnostic{Code: tc.code, Message: tc.message})
				s.SelectRows = true
				s.Prepared = prepared
				s.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			})
		}
	}
}

func TestReferenceNumericArithmeticDiagnosticsAndBindings(t *testing.T) {
	numericArithmeticBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		numericArithmeticUnit(t, p, b)
		numericArithmeticFailures(t, p, b)
		numericArithmeticDiagnosticRoutes(t, p, b, "SELECT 1::numeric+TRUE FROM unit", fixture.Diagnostic{
			Code: "42883", Message: "operator does not exist: numeric + boolean", Position: "18",
			Hint: "No operator matches the given name and argument types. You might need to add explicit type casts.",
		}, true)
		numericArithmeticDiagnosticRoutes(t, p, b, "SELECT 1::numeric+'bad' FROM unit", fixture.Diagnostic{Code: "22P02", Message: "invalid input syntax for type numeric: \"bad\"", Position: "19"}, true)
		// Zero-argument native Query binds slots; pq's simple protocol instead has
		// no parameter slot. Preserve both predecessor adapter records explicitly.
		missing := fixture.ImplicitError("SELECT $1,1::numeric+$1 FROM unit", fixture.Diagnostic{Code: "42P02", Message: "there is no parameter $1", Position: "8"})
		conflict := fixture.Diagnostic{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "numeric versus text", Position: "8"}
		if !b.PG {
			missing = fixture.ImplicitError(missing.Query, conflict)
		}
		missing.SelectRows = true
		missing.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, missing)
		conflicting := fixture.ImplicitError(missing.Query, conflict)
		conflicting.SelectRows = true
		conflicting.ErrorStage = "prepare"
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(conflicting))
		conflicting.Args = []any{"2"}
		conflicting.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, conflicting)
		numericArithmeticDiagnosticRoutes(t, p, b, "SELECT NULL::numeric+(1::numeric/0) FROM unit", fixture.Diagnostic{Code: "22012", Message: "division by zero"}, false)
		numericArithmeticDiagnosticRoutes(t, p, b, "SELECT 1::numeric+$1 FROM unit", numericArithmeticInputDiagnostic(), false, "bad")
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE empty(n NUMERIC)", 0))
		for _, q := range []string{"SELECT 1/n FROM empty", "SELECT 1/n FROM empty WHERE n=0"} {
			s := fixture.Select(q, []string{"?column?"}, []string{"NUMERIC"}, nil)
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(s))
		}
		numericArithmeticDiagnosticRoutes(t, p, b, "SELECT n+'bad'::numeric FROM empty", fixture.Diagnostic{Code: "22P02", Message: "invalid input syntax for type numeric: \"bad\"", Position: "10"}, true)
	})
}

func numericArithmeticMetadata(pg bool) func(*testing.T, *sql.Rows) {
	return func(t *testing.T, rows *sql.Rows) {
		columns, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range columns {
			scan := reflect.TypeFor[string]()
			precision, scale, sized := int64(0), int64(0), false
			if pg {
				scan = reflect.TypeFor[any]()
				precision, scale, sized = 65535, 65531, true
			}
			p, s, ok := c.DecimalSize()
			length, hasLength := c.Length()
			nullable, known := c.Nullable()
			if c.Name() != "?column?" || c.DatabaseTypeName() != "NUMERIC" || c.ScanType() != scan || p != precision || s != scale || ok != sized || length != 0 || hasLength || nullable || known {
				t.Fatalf("computed metadata %v decimal=%d,%d,%t nullable=%t,%t length=%d,%t", c, p, s, ok, nullable, known, length, hasLength)
			}
		}
	}
}

func numericArithmeticHeld(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE inputs(id INT,n NUMERIC(8,3))", 0))
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO inputs VALUES(1,1.200),(2,NULL)", 2))
	const q = "SELECT n+$1 FROM inputs ORDER BY id"
	stmt, err := b.DB.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{b.DB, stmt, q}
	for _, tc := range []struct{ arg, value any }{{"2.30", "3.500"}, {[]byte("2.30"), "3.500"}, {int64(2), "3.200"}, {nil, nil}, {"0.0001", "1.2001"}} {
		s := fixture.Select(q, []string{"?column?"}, []string{"NUMERIC"}, [][]any{{tc.value}, {nil}}, tc.arg)
		s.CheckMetadata = numericArithmeticMetadata(b.PG)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
	}
	numericArithmeticDiagnosticRoutes(t, p, b, q, numericArithmeticInputDiagnostic(), false, "bad")
	bad := fixture.ImplicitError(q, numericArithmeticInputDiagnostic(), "bad")
	bad.SelectRows = true
	bad.ErrorStage = "execute"
	fixture.RunStep(t, p.Ctx, held, b.Recorder, bad)
	s := fixture.Select(q, []string{"?column?"}, []string{"NUMERIC"}, [][]any{{"3.200"}, {nil}}, int64(2))
	fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
	// Input memory belongs to the caller; mutate only after this frame is imported.
	bytes := []byte("2.30")
	rows, err := stmt.QueryContext(context.WithValue(p.Ctx, fixture.RecorderKey{}, b.Recorder), bytes)
	if err != nil {
		t.Fatal(err)
	}
	bytes[0] = '9'
	columns, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	numericReferenceQueryRows(t, b, rows, columns, fixture.Select(q, []string{"?column?"}, []string{"NUMERIC"}, [][]any{{"3.500"}, {nil}}, []byte("2.30")))
	for _, tc := range []struct {
		expr       string
		arg, value any
	}{
		{"+n", nil, "1.200"}, {"-n", nil, "-1.200"}, {"n-$1", "0.20", "1.000"}, {"n*$1", "2.0", "2.4000"}, {"n/$1", 2, "0.60000000000000000000"}, {"n%$1", "1.00", "0.200"}, {"n^$1", 2, "1.4400000000000000"}, {"n+$1+$1", "0.01", "1.220"},
	} {
		t.Run(tc.expr, func(t *testing.T) {
			query := "SELECT " + tc.expr + " FROM inputs ORDER BY id"
			st, err := b.DB.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			args := []any(nil)
			if tc.arg != nil {
				args = []any{tc.arg}
			}
			step := fixture.Select(query, []string{"?column?"}, []string{"NUMERIC"}, [][]any{{tc.value}, {nil}}, args...)
			step.CheckMetadata = numericArithmeticMetadata(b.PG)
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{b.DB, st, query}, b.Recorder, step)
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, step)
		})
	}
	for _, predicate := range []string{"id=2", "id=3"} {
		want := [][]any(nil)
		if predicate == "id=2" {
			want = [][]any{{nil}}
		}
		s := fixture.Select("SELECT n+1 FROM inputs WHERE "+predicate, []string{"?column?"}, []string{"NUMERIC"}, want)
		s.CheckMetadata = numericArithmeticMetadata(b.PG)
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(s))
	}
	physical := fixture.Select("SELECT n FROM inputs ORDER BY id", []string{"n"}, []string{"NUMERIC"}, [][]any{{"1.200"}, {nil}})
	physical.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		columns, err := rows.ColumnTypes()
		if err != nil || len(columns) != 1 {
			t.Fatal(columns, err)
		}
		c := columns[0]
		scan := reflect.TypeFor[string]()
		if b.PG {
			scan = reflect.TypeFor[any]()
		}
		precision, scale, sized := c.DecimalSize()
		length, hasLength := c.Length()
		nullable, known := c.Nullable()
		if c.Name() != "n" || c.DatabaseTypeName() != "NUMERIC" || c.ScanType() != scan || precision != 8 || scale != 3 || !sized || length != 0 || hasLength || nullable != !b.PG || known != !b.PG {
			t.Fatalf("physical NUMERIC metadata %v decimal=%d,%d,%t nullable=%t,%t", c, precision, scale, sized, nullable, known)
		}
	}
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, physical)
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(physical))
}

func TestReferenceNumericArithmeticParametersAndMetadata(t *testing.T) {
	numericArithmeticBackends(t, numericArithmeticHeld)
}

func numericArithmeticInputDiagnostic() fixture.Diagnostic {
	return fixture.Diagnostic{Code: "22P02", Message: "invalid input syntax for type numeric: \"bad\"", Context: "unnamed portal parameter $1 = '...'"}
}

func numericArithmeticDiagnosticRoutes(t *testing.T, p *fixture.Pair, b fixture.Backend, q string, want fixture.Diagnostic, prepareFails bool, args ...any) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("%s/prepared%t", q, prepared), func(t *testing.T) {
			s := fixture.ImplicitError(q, want, args...)
			s.SelectRows = true
			s.Prepared = prepared
			s.ErrorStage = "execute"
			if prepared && prepareFails {
				s.ErrorStage = "prepare"
			}
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
		})
	}
}
