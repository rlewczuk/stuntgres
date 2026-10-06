package stuntgres

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"stuntgres/internal/engine"
	"stuntgres/internal/sqltype"
	"testing"
)

type typeCoreValuer struct {
	calls *int
	err   error
}

func (v typeCoreValuer) Value() (driver.Value, error) {
	(*v.calls)++
	return "7", v.err
}
func TestTypeCoreAdapterOwnershipAndErrors(t *testing.T) {
	ctx := context.Background()
	e := engine.New()
	integer, _ := e.CatalogRead().LookupType(sqltype.Int4)
	text, _ := e.CatalogRead().LookupType(sqltype.Text)
	bytes := []byte("owned")
	values, err := normalizeArguments([]driver.NamedValue{{Ordinal: 1, Value: bytes}})
	if err != nil {
		t.Fatal(err)
	}
	bytes[0] = 'X'
	v, err := importParameter(ctx, values[0], text.Usage(), sqltype.InputEnvironment{})
	if err != nil {
		t.Fatal(err)
	}
	exported, err := exportValue(ctx, v)
	if err != nil || exported != "owned" {
		t.Fatal(exported, err)
	}
	v, err = importParameter(ctx, int64(7), integer.Usage(), sqltype.InputEnvironment{})
	if err != nil || v.Type() != integer {
		t.Fatal(v, err)
	}
	if scanType(integer) != reflect.TypeFor[int64]() {
		t.Fatal(scanType(integer))
	}
	exported, err = exportValue(ctx, sqltype.Null(integer))
	if err != nil || exported != nil {
		t.Fatal(exported, err)
	}
	calls := 0
	sentinel := errors.New("valuer failed")
	_, err = normalizeArguments([]driver.NamedValue{{Ordinal: 1, Value: typeCoreValuer{&calls, sentinel}}})
	if !errors.Is(err, sentinel) || calls != 1 {
		t.Fatal(err, calls)
	}
	var sqlErr interface{ SQLState() string }
	if errors.As(err, &sqlErr) {
		t.Fatal("valuer error became SQL error")
	}
	for _, a := range []driver.NamedValue{{Ordinal: 1, Name: "named", Value: 1}, {Ordinal: 2, Value: 1}, {Ordinal: 1, Value: make(chan int)}} {
		_, err = normalizeArguments([]driver.NamedValue{a})
		if err == nil || errors.As(err, &sqlErr) {
			t.Fatal("ordinary argument error", err)
		}
	}
	if _, err = exportValue(ctx, sqltype.Value{}); err == nil {
		t.Fatal("zero result accepted")
	}
}

func TestTypeCoreScalarImports(t *testing.T) {
	ctx := context.Background()
	e := engine.New()
	for _, tc := range []struct {
		name string
		id   sqltype.ID
		raw  driver.Value
	}{
		{"int2", sqltype.Int2, int64(1000)},
		{"int2_min", sqltype.Int2, int64(-32768)},
		{"int2_max", sqltype.Int2, int64(32767)},
		{"int4", sqltype.Int4, int64(1000)},
		{"int4_min", sqltype.Int4, int64(-2147483648)},
		{"int4_max", sqltype.Int4, int64(2147483647)},
		{"int8", sqltype.Int8, int64(1000)},
		{"int8_min", sqltype.Int8, int64(-9223372036854775808)},
		{"int8_max", sqltype.Int8, int64(9223372036854775807)},
		{"bool_true", sqltype.Bool, true},
		{"bool_false", sqltype.Bool, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			target, ok := e.CatalogRead().LookupType(tc.id)
			if !ok {
				t.Fatal("missing target type")
			}
			usage := target.Usage()
			v, err := importParameter(ctx, tc.raw, usage, sqltype.InputEnvironment{})
			if err != nil || v.Type() != target || v.IsNull() {
				t.Fatal(v, err)
			}
			switch raw := tc.raw.(type) {
			case int64:
				got, err := v.Integer()
				if err != nil || got != raw {
					t.Fatal(got, err)
				}
			case bool:
				got, err := v.Boolean()
				if err != nil || got != raw {
					t.Fatal(got, err)
				}
			}
			// Arguments and descriptors are already normalized/owned, just as in
			// the locked frame importer. Measure only successful scalar import.
			var imported sqltype.Value
			var importErr error
			allocs := testing.AllocsPerRun(1000, func() {
				imported, importErr = importParameter(ctx, tc.raw, usage, sqltype.InputEnvironment{})
			})
			if importErr != nil || imported.Type() != target || allocs != 0 {
				t.Fatalf("scalar import: type=%v error=%v allocations=%g", imported.Type(), importErr, allocs)
			}
			canceled, cancel := context.WithCancel(ctx)
			cancel()
			if _, err := importParameter(canceled, tc.raw, usage, sqltype.InputEnvironment{}); !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled scalar import: %v", err)
			}
		})
	}
	target, ok := e.CatalogRead().LookupType(sqltype.Int4)
	if !ok {
		t.Fatal("missing INT4 target")
	}
	for _, tc := range []struct {
		raw     int64
		message string
	}{
		{2147483648, `value "2147483648" is out of range for type integer`},
		{-2147483649, `value "-2147483649" is out of range for type integer`},
	} {
		_, err := importParameter(ctx, tc.raw, target.Usage(), sqltype.InputEnvironment{})
		var got *Error
		want := Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22003", Message: tc.message}
		if !errors.As(err, &got) || *got != want {
			t.Fatalf("got %#v (%v), want %#v", got, err, want)
		}
	}
}

func TestFloatAdapterImports(t *testing.T) {
	ctx := context.Background()
	e := engine.New()
	for _, id := range []sqltype.ID{sqltype.Float4, sqltype.Float8} {
		target, _ := e.CatalogRead().LookupType(id)
		for _, raw := range []any{float32(0.1), float64(0.1), int64(16777217), "0.1", nil} {
			normalized, err := normalizeArguments([]driver.NamedValue{{Ordinal: 1, Value: raw}})
			if err != nil {
				t.Fatal(err)
			}
			v, err := importParameter(ctx, normalized[0], target.Usage(), sqltype.InputEnvironment{})
			if err != nil || v.Type() != target {
				t.Fatal(v, err)
			}
			got, err := exportValue(ctx, v)
			if err != nil {
				t.Fatal(err)
			}
			if raw == nil {
				if got != nil {
					t.Fatal(got)
				}
				continue
			}
			want := 0.1
			switch n := raw.(type) {
			case float32:
				want = float64(n)
			case float64:
				want = n
			case int64:
				want = float64(n)
			}
			if id == sqltype.Float4 {
				want = float64(float32(want))
			}
			if got != want {
				t.Fatalf("target%d raw%T: %v want %.17g", id, raw, got, want)
			}
		}
		if scanType(target) != reflect.TypeFor[float64]() {
			t.Fatal(scanType(target))
		}
	}
}

func TestSmallintScalarOverflow(t *testing.T) {
	e := engine.New()
	target, _ := e.CatalogRead().LookupType(sqltype.Int2)
	for _, tc := range []struct {
		raw     int64
		message string
	}{{32768, `value "32768" is out of range for type smallint`}, {-32769, `value "-32769" is out of range for type smallint`}} {
		_, err := importParameter(context.Background(), tc.raw, target.Usage(), sqltype.InputEnvironment{})
		var got *Error
		want := Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22003", Message: tc.message}
		if !errors.As(err, &got) || *got != want {
			t.Fatalf("got %#v (%v), want %#v", got, err, want)
		}
	}
}
