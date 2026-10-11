package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func referenceMathGcdLcmBackends(t *testing.T, p *fixture.Pair, body func(*testing.T, fixture.Backend)) {
	t.Helper()
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) { body(t, backend) })
	}
}

func referenceMathGcdLcmMetadata(t *testing.T, rows *sql.Rows, pg bool, nonNullableColumns ...int) {
	t.Helper()
	columns, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	for i, column := range columns {
		if _, ok := column.Length(); ok {
			t.Fatalf("computed %s inherited length", column.Name())
		}
		wantKnown := false
		for _, index := range nonNullableColumns {
			wantKnown = wantKnown || !pg && i == index
		}
		if nullable, known := column.Nullable(); nullable || known != wantKnown {
			t.Fatalf("%s nullability %v/%v want false/%v", column.Name(), nullable, known, wantKnown)
		}
		precision, scale, sized := column.DecimalSize()
		if pg && column.DatabaseTypeName() == "NUMERIC" {
			if !sized || precision != 65535 || scale != 65531 {
				t.Fatalf("unconstrained NUMERIC metadata %d,%d,%v", precision, scale, sized)
			}
		} else if sized {
			t.Fatalf("computed %s inherited decimal typmod", column.Name())
		}
		want := reflect.TypeOf(int64(0))
		if column.DatabaseTypeName() == "NUMERIC" {
			want = reflect.TypeOf("")
			if pg {
				want = reflect.TypeFor[any]()
			}
		}
		if pg && column.DatabaseTypeName() == "INT4" {
			want = reflect.TypeOf(int32(0))
		}
		if column.ScanType() != want {
			t.Fatalf("%s ScanType %v want %v", column.Name(), column.ScanType(), want)
		}
	}
}

func referenceMathGcdLcmStep(t *testing.T, p *fixture.Pair, backend fixture.Backend, s fixture.Step) {
	t.Helper()
	if s.SelectRows && s.State == "" && s.CheckMetadata == nil {
		s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { referenceMathGcdLcmMetadata(t, rows, backend.PG) }
	}
	for _, prepared := range []bool{false, true} {
		s.Prepared = prepared
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
	}
}

func referenceMathGcdLcmFailure(query, code, message string) fixture.Step {
	s := fixture.ImplicitError(query, fixture.Diagnostic{Code: code, Message: message})
	s.SelectRows, s.ErrorStage = true, "execute"
	return s
}
