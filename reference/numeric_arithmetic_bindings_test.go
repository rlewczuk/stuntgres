package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"testing"
)

func numericArithmeticFailures(t *testing.T, p *referencePair, b referenceBackend) {
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
				s := referenceImplicitError("SELECT "+tc.expr+" FROM unit", referenceDiagnostic{Code: tc.code, Message: tc.message})
				s.selectRows = true
				s.prepared = prepared
				s.errorStage = "execute"
				referenceRunStep(t, p.ctx, b.db, b.recorder, s)
			})
		}
	}
}

func TestReferenceNumericArithmeticDiagnosticsAndBindings(t *testing.T) {
	numericArithmeticBackends(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		numericArithmeticUnit(t, p, b)
		numericArithmeticFailures(t, p, b)
		numericArithmeticDiagnosticRoutes(t, p, b, "SELECT 1::numeric+TRUE FROM unit", referenceDiagnostic{
			Code: "42883", Message: "operator does not exist: numeric + boolean", Position: "18",
			Hint: "No operator matches the given name and argument types. You might need to add explicit type casts.",
		}, true)
		numericArithmeticDiagnosticRoutes(t, p, b, "SELECT 1::numeric+'bad' FROM unit", referenceDiagnostic{Code: "22P02", Message: "invalid input syntax for type numeric: \"bad\"", Position: "19"}, true)
		// Zero-argument native Query binds slots; pq's simple protocol instead has
		// no parameter slot. Preserve both predecessor adapter records explicitly.
		missing := referenceImplicitError("SELECT $1,1::numeric+$1 FROM unit", referenceDiagnostic{Code: "42P02", Message: "there is no parameter $1", Position: "8"})
		conflict := referenceDiagnostic{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "numeric versus text", Position: "8"}
		if !b.pg {
			missing = referenceImplicitError(missing.query, conflict)
		}
		missing.selectRows = true
		missing.errorStage = "execute"
		referenceRunStep(t, p.ctx, b.db, b.recorder, missing)
		conflicting := referenceImplicitError(missing.query, conflict)
		conflicting.selectRows = true
		conflicting.errorStage = "prepare"
		referenceRunStep(t, p.ctx, b.db, b.recorder, referencePrepared(conflicting))
		conflicting.args = []any{"2"}
		conflicting.errorStage = "execute"
		referenceRunStep(t, p.ctx, b.db, b.recorder, conflicting)
		numericArithmeticDiagnosticRoutes(t, p, b, "SELECT NULL::numeric+(1::numeric/0) FROM unit", referenceDiagnostic{Code: "22012", Message: "division by zero"}, false)
		numericArithmeticDiagnosticRoutes(t, p, b, "SELECT 1::numeric+$1 FROM unit", numericArithmeticInputDiagnostic(), false, "bad")
		referenceRunStep(t, p.ctx, b.db, b.recorder, referenceExec("CREATE TABLE empty(n NUMERIC)", 0))
		for _, q := range []string{"SELECT 1/n FROM empty", "SELECT 1/n FROM empty WHERE n=0"} {
			s := referenceSelect(q, []string{"?column?"}, []string{"NUMERIC"}, nil)
			referenceRunStep(t, p.ctx, b.db, b.recorder, s)
			referenceRunStep(t, p.ctx, b.db, b.recorder, referencePrepared(s))
		}
		numericArithmeticDiagnosticRoutes(t, p, b, "SELECT n+'bad'::numeric FROM empty", referenceDiagnostic{Code: "22P02", Message: "invalid input syntax for type numeric: \"bad\"", Position: "10"}, true)
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

func numericArithmeticHeld(t *testing.T, p *referencePair, b referenceBackend) {
	t.Helper()
	referenceRunStep(t, p.ctx, b.db, b.recorder, referenceExec("CREATE TABLE inputs(id INT,n NUMERIC(8,3))", 0))
	referenceRunStep(t, p.ctx, b.db, b.recorder, referenceExec("INSERT INTO inputs VALUES(1,1.200),(2,NULL)", 2))
	const q = "SELECT n+$1 FROM inputs ORDER BY id"
	stmt, err := b.db.PrepareContext(p.ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := referenceHeldStatement{b.db, stmt, q}
	for _, tc := range []struct{ arg, value any }{{"2.30", "3.500"}, {[]byte("2.30"), "3.500"}, {int64(2), "3.200"}, {nil, nil}, {"0.0001", "1.2001"}} {
		s := referenceSelect(q, []string{"?column?"}, []string{"NUMERIC"}, [][]any{{tc.value}, {nil}}, tc.arg)
		s.checkMetadata = numericArithmeticMetadata(b.pg)
		referenceRunStep(t, p.ctx, held, b.recorder, s)
		referenceRunStep(t, p.ctx, b.db, b.recorder, s)
	}
	numericArithmeticDiagnosticRoutes(t, p, b, q, numericArithmeticInputDiagnostic(), false, "bad")
	bad := referenceImplicitError(q, numericArithmeticInputDiagnostic(), "bad")
	bad.selectRows = true
	bad.errorStage = "execute"
	referenceRunStep(t, p.ctx, held, b.recorder, bad)
	s := referenceSelect(q, []string{"?column?"}, []string{"NUMERIC"}, [][]any{{"3.200"}, {nil}}, int64(2))
	referenceRunStep(t, p.ctx, held, b.recorder, s)
	// Input memory belongs to the caller; mutate only after this frame is imported.
	bytes := []byte("2.30")
	rows, err := stmt.QueryContext(context.WithValue(p.ctx, referenceRecorderKey{}, b.recorder), bytes)
	if err != nil {
		t.Fatal(err)
	}
	bytes[0] = '9'
	columns, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	numericReferenceQueryRows(t, b, rows, columns, referenceSelect(q, []string{"?column?"}, []string{"NUMERIC"}, [][]any{{"3.500"}, {nil}}, []byte("2.30")))
	for _, tc := range []struct {
		expr       string
		arg, value any
	}{
		{"+n", nil, "1.200"}, {"-n", nil, "-1.200"}, {"n-$1", "0.20", "1.000"}, {"n*$1", "2.0", "2.4000"}, {"n/$1", 2, "0.60000000000000000000"}, {"n%$1", "1.00", "0.200"}, {"n^$1", 2, "1.4400000000000000"}, {"n+$1+$1", "0.01", "1.220"},
	} {
		t.Run(tc.expr, func(t *testing.T) {
			query := "SELECT " + tc.expr + " FROM inputs ORDER BY id"
			st, err := b.db.PrepareContext(p.ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			args := []any(nil)
			if tc.arg != nil {
				args = []any{tc.arg}
			}
			step := referenceSelect(query, []string{"?column?"}, []string{"NUMERIC"}, [][]any{{tc.value}, {nil}}, args...)
			step.checkMetadata = numericArithmeticMetadata(b.pg)
			referenceRunStep(t, p.ctx, referenceHeldStatement{b.db, st, query}, b.recorder, step)
			referenceRunStep(t, p.ctx, b.db, b.recorder, step)
		})
	}
	for _, predicate := range []string{"id=2", "id=3"} {
		want := [][]any(nil)
		if predicate == "id=2" {
			want = [][]any{{nil}}
		}
		s := referenceSelect("SELECT n+1 FROM inputs WHERE "+predicate, []string{"?column?"}, []string{"NUMERIC"}, want)
		s.checkMetadata = numericArithmeticMetadata(b.pg)
		referenceRunStep(t, p.ctx, b.db, b.recorder, s)
		referenceRunStep(t, p.ctx, b.db, b.recorder, referencePrepared(s))
	}
	physical := referenceSelect("SELECT n FROM inputs ORDER BY id", []string{"n"}, []string{"NUMERIC"}, [][]any{{"1.200"}, {nil}})
	physical.checkMetadata = func(t *testing.T, rows *sql.Rows) {
		columns, err := rows.ColumnTypes()
		if err != nil || len(columns) != 1 {
			t.Fatal(columns, err)
		}
		c := columns[0]
		scan := reflect.TypeFor[string]()
		if b.pg {
			scan = reflect.TypeFor[any]()
		}
		precision, scale, sized := c.DecimalSize()
		length, hasLength := c.Length()
		nullable, known := c.Nullable()
		if c.Name() != "n" || c.DatabaseTypeName() != "NUMERIC" || c.ScanType() != scan || precision != 8 || scale != 3 || !sized || length != 0 || hasLength || nullable != !b.pg || known != !b.pg {
			t.Fatalf("physical NUMERIC metadata %v decimal=%d,%d,%t nullable=%t,%t", c, precision, scale, sized, nullable, known)
		}
	}
	referenceRunStep(t, p.ctx, b.db, b.recorder, physical)
	referenceRunStep(t, p.ctx, b.db, b.recorder, referencePrepared(physical))
}

func TestReferenceNumericArithmeticParametersAndMetadata(t *testing.T) {
	numericArithmeticBackends(t, numericArithmeticHeld)
}

func numericArithmeticInputDiagnostic() referenceDiagnostic {
	return referenceDiagnostic{Code: "22P02", Message: "invalid input syntax for type numeric: \"bad\"", Context: "unnamed portal parameter $1 = '...'"}
}

func numericArithmeticDiagnosticRoutes(t *testing.T, p *referencePair, b referenceBackend, q string, want referenceDiagnostic, prepareFails bool, args ...any) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("%s/prepared%t", q, prepared), func(t *testing.T) {
			s := referenceImplicitError(q, want, args...)
			s.selectRows = true
			s.prepared = prepared
			s.errorStage = "execute"
			if prepared && prepareFails {
				s.errorStage = "prepare"
			}
			referenceRunStep(t, p.ctx, b.db, b.recorder, s)
		})
	}
}
