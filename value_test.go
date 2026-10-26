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
	exported, err := exportValue(ctx, v, sqltype.OutputEnvironment{})
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
	exported, err = exportValue(ctx, sqltype.Null(integer), sqltype.OutputEnvironment{})
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
	if _, err = exportValue(ctx, sqltype.Value{}, sqltype.OutputEnvironment{}); err == nil {
		t.Fatal("zero result accepted")
	}
}

func TestBitstringParameterNullBoundary(t *testing.T) {
	ctx := context.Background()
	e := engine.New()
	for _, id := range []sqltype.ID{sqltype.Bit, sqltype.VarBit, sqltype.Bytea, sqltype.JSON, sqltype.Name, sqltype.Text} {
		target, _ := e.CatalogRead().LookupType(id)
		for _, raw := range []any{nil, []byte(nil), []byte{}, ""} {
			normalized, err := normalizeArguments([]driver.NamedValue{{Ordinal: 1, Value: raw}})
			if err != nil {
				t.Fatal(err)
			}
			// Normalization retains typed-nil bytes for the destination importer,
			// while taking immutable ownership of every nonnil byte buffer.
			if bytes, ok := raw.([]byte); ok && bytes == nil {
				if value, ok := normalized[0].([]byte); !ok || value != nil {
					t.Fatal("typed-nil authority lost", normalized[0])
				}
			}
			value, err := importParameter(ctx, normalized[0], target.Usage(), sqltype.InputEnvironment{})
			bytes, typedBytes := raw.([]byte)
			wantNull := raw == nil || typedBytes && bytes == nil && id != sqltype.Name && id != sqltype.Text
			if id == sqltype.JSON && !wantNull {
				var diagnostic *Error
				if !errors.As(err, &diagnostic) || diagnostic.Code != "22P02" {
					t.Fatal("empty JSON input", value, err)
				}
				continue
			}
			if err != nil || value.Type() != target || value.IsNull() != wantNull {
				t.Fatal(id, raw, value, err)
			}
			if id == sqltype.Bit || id == sqltype.VarBit {
				bounded := target.Usage()
				bounded.Typmod = 3
				plan, err := sqltype.PlanCoercion(target.Usage(), bounded, sqltype.Assignment)
				if err != nil {
					t.Fatal(err)
				}
				sized, err := plan.Apply(ctx, value, sqltype.InputEnvironment{}, sqltype.OutputEnvironment{})
				if wantNull {
					if err != nil || !sized.IsNull() || sized.Type() != target {
						t.Fatal("NULL passed through bit sizing", sized, err)
					}
				} else if id == sqltype.Bit {
					var diagnostic *Error
					if !errors.As(err, &diagnostic) || diagnostic.Code != "22026" {
						t.Fatal("empty fixed-width input", sized, err)
					}
				} else if err != nil || sized.IsNull() {
					t.Fatal("empty varying input", sized, err)
				}
			}
		}
	}
}

func TestInternalCharParameterImport(t *testing.T) {
	ctx := context.Background()
	e := engine.New()
	char, _ := e.CatalogRead().LookupType(sqltype.Char)
	domain, err := sqltype.Domain(49000, "import_char", "public.import_char", char.Usage(), false)
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []*sqltype.Descriptor{char, domain} {
		for _, tc := range []struct {
			name string
			raw  any
			want string
			null bool
		}{
			{"nil", nil, "", true},
			{"typed_nil", []byte(nil), "", true},
			{"empty_bytes", []byte{}, "", false},
			{"empty_string", "", "", false},
			{"bytes", []byte("abc"), "a", false},
			{"integer", int64(65), "6", false},
			{"utf8", "é", `\303`, false},
		} {
			t.Run(target.Name()+"/"+tc.name, func(t *testing.T) {
				args, err := normalizeArguments([]driver.NamedValue{{Ordinal: 1, Value: tc.raw}})
				if err != nil {
					t.Fatal(err)
				}
				v, err := importParameter(ctx, args[0], target.Usage(), sqltype.InputEnvironment{})
				if err != nil || v.Type() != target || v.IsNull() != tc.null {
					t.Fatal(v, err)
				}
				got, err := exportValue(ctx, v, sqltype.OutputEnvironment{})
				if err != nil || tc.null && got != nil || !tc.null && got != tc.want {
					t.Fatal(got, err)
				}
			})
		}
		for _, raw := range []any{"\xff", "a\xff", "\x00", "a\x00b", []byte("a\xff"), []byte("a\x00b")} {
			args, err := normalizeArguments([]driver.NamedValue{{Ordinal: 1, Value: raw}})
			if err != nil {
				t.Fatal(err)
			}
			v, err := importParameter(ctx, args[0], target.Usage(), sqltype.InputEnvironment{})
			var diagnostic *Error
			message := `invalid byte sequence for encoding "UTF8": 0xff`
			if reflect.DeepEqual(raw, "a\x00b") || reflect.DeepEqual(raw, "\x00") || reflect.DeepEqual(raw, []byte("a\x00b")) {
				message = `invalid byte sequence for encoding "UTF8": 0x00`
			}
			want := Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22021", Message: message}
			if !errors.As(err, &diagnostic) || !reflect.DeepEqual(*diagnostic, want) || v.Type() != nil {
				t.Fatalf("input %q: value=%v diagnostic=%#v", raw, v, err)
			}
		}
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
			got, err := exportValue(ctx, v, sqltype.OutputEnvironment{})
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

func TestDomainIntervalParameterInputFieldsAndIdentity(t *testing.T) {
	ctx := context.Background()
	e := engine.New()
	interval, ok := e.CatalogRead().LookupType(sqltype.Interval)
	if !ok {
		t.Fatal("missing interval")
	}
	for i, tc := range []struct {
		mask, precision int32
		input, output   string
	}{
		{sqltype.IntervalDay, sqltype.IntervalFullPrecision, "1", "1 day"},
		{sqltype.IntervalHour, sqltype.IntervalFullPrecision, "1", "01:00:00"},
		{sqltype.IntervalMonth, sqltype.IntervalFullPrecision, "1", "1 mon"},
		{sqltype.IntervalMinute | sqltype.IntervalSecond, 2, "1:30.126", "00:01:30.13"},
	} {
		base := interval.Usage()
		base.Typmod = tc.mask<<16 | tc.precision
		domain, err := sqltype.Domain(sqltype.ID(20000+i), "fields", "public.fields", base)
		if err != nil {
			t.Fatal(err)
		}
		chain, err := sqltype.Domain(sqltype.ID(30000+i), "chain", "public.chain", domain.Usage())
		if err != nil {
			t.Fatal(err)
		}
		for _, target := range []sqltype.Usage{base, domain.Usage(), chain.Usage()} {
			value, err := importParameter(ctx, tc.input, target, sqltype.InputEnvironment{})
			if err != nil || value.Type() != target.Type {
				t.Fatal(target, value, err)
			}
			text, err := target.Type.Output(ctx, value)
			if err != nil || text != tc.output {
				t.Fatal(target, text, err)
			}
			physical, err := value.BaseValue()
			if err != nil || physical.Type() != interval {
				t.Fatal("physical interval authority", physical, err)
			}
			null, err := importParameter(ctx, nil, target, sqltype.InputEnvironment{})
			if err != nil || !null.IsNull() || null.Type() != target.Type {
				t.Fatal("typed NULL", target, null, err)
			}
		}
	}
}

func TestIdentifierAdapterOwnership(t *testing.T) {
	ctx := context.Background()
	e := engine.New()
	for _, id := range []sqltype.ID{sqltype.Xid, sqltype.Cid, sqltype.Xid8} {
		target, ok := e.CatalogRead().LookupType(id)
		if !ok || scanType(target) != reflect.TypeFor[[]byte]() {
			t.Fatal("identifier metadata", id)
		}
		max := "4294967295"
		if id == sqltype.Xid8 {
			max = "18446744073709551615"
		}
		raw := []byte(max)
		normalized, err := normalizeArguments([]driver.NamedValue{{Ordinal: 1, Value: raw}})
		if err != nil {
			t.Fatal(err)
		}
		raw[0] = '0'
		v, err := importParameter(ctx, normalized[0], target.Usage(), sqltype.InputEnvironment{})
		if err != nil {
			t.Fatal(err)
		}
		first, err := exportValue(ctx, v, sqltype.OutputEnvironment{})
		if err != nil || string(first.([]byte)) != max {
			t.Fatal(first, err)
		}
		first.([]byte)[0] = '0'
		second, err := exportValue(ctx, v, sqltype.OutputEnvironment{})
		if err != nil || string(second.([]byte)) != max {
			t.Fatal("aliased identifier output", second, err)
		}
		for _, raw := range []driver.Value{nil, []byte(nil)} {
			null, err := importParameter(ctx, raw, target.Usage(), sqltype.InputEnvironment{})
			if err != nil || !null.IsNull() || null.Type() != target {
				t.Fatal(null, err)
			}
			result, err := exportValue(ctx, null, sqltype.OutputEnvironment{})
			if err != nil || result != nil {
				t.Fatal("SQL NULL export", result, err)
			}
		}
		if _, err := importParameter(ctx, "", target.Usage(), sqltype.InputEnvironment{}); err == nil {
			t.Fatal("empty identifier accepted")
		}
	}
}
