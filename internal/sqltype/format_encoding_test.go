package sqltype

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestFormatEncodingResolver(t *testing.T) {
	for _, tc := range []struct{ input, candidate, canonical string }{
		{"UTF8", "UTF8", "UTF8"}, {"utf-8", "utf-8", "UTF8"}, {"u.t_f-8", "u.t_f-8", "UTF8"},
		{"UNICODE", "UNICODE", "UTF8"}, {"MULE_INTERNAL", "MULE_INTERNAL", "MULE_INTERNAL"},
		{"LATIN1", "LATIN1", "LATIN1"}, {"SQL_ASCII", "SQL_ASCII", "SQL_ASCII"},
		{"unknown", "unknown", ""}, {strings.Repeat("-", 80) + "utf8", strings.Repeat("-", 63), ""},
		{strings.Repeat("x", 62) + "é", strings.Repeat("x", 62), ""},
	} {
		candidate, canonical := ClientEncodingName(tc.input)
		if candidate != tc.candidate || canonical != tc.canonical {
			t.Fatalf("%q: (%q,%q), want (%q,%q)", tc.input, candidate, canonical, tc.candidate, tc.canonical)
		}
	}
}

func TestFormatEncodingFloatEnvironment(t *testing.T) {
	ctx := context.Background()
	types := testTypes()
	for _, tc := range []struct {
		id                                         ID
		input, shortest, rounded, reduced, minimum string
	}{
		{Float4, "1.23456789", "1.2345679", "1.23457", "1.2346", "1"},
		{Float8, "1.2345678901234567", "1.2345678901234567", "1.23456789012346", "1.2345678901235", "1"},
		{Float4, "1e-45", "1e-45", "1.4013e-45", "1.4013e-45", "1e-45"},
		{Float8, "5e-324", "5e-324", "4.94065645841247e-324", "4.9406564584125e-324", "5e-324"},
		{Float4, "3.4028235e+38", "3.4028235e+38", "3.40282e+38", "3.4028e+38", "3e+38"},
		{Float8, "1.7976931348623157e+308", "1.7976931348623157e+308", "1.79769313486232e+308", "1.7976931348623e+308", "2e+308"},
		{Float4, "33554448", "3.3554448e+07", "3.35544e+07", "3.3554e+07", "3e+07"},
		{Float8, "1.8014398509481992e+16", "1.8014398509481992e+16", "1.8014398509482e+16", "1.8014398509482e+16", "2e+16"},
		{Float4, "100000", "100000", "100000", "1e+05", "1e+05"},
		{Float4, "9.999999", "9.999999", "10", "10", "1e+01"},
		{Float8, "-0", "-0", "-0", "-0", "-0"},
		{Float8, "NaN", "NaN", "NaN", "NaN", "NaN"},
		{Float8, "Infinity", "Infinity", "Infinity", "Infinity", "Infinity"},
		{Float8, "-Infinity", "-Infinity", "-Infinity", "-Infinity", "-Infinity"},
	} {
		d := types[tc.id]
		v, err := d.Input(ctx, tc.input, -1)
		if err != nil {
			t.Fatal(err)
		}
		bits := math.Float64bits(v.floating)
		for _, precision := range []int32{1, 2, 3, 0, -1, -15} {
			want := tc.shortest
			if precision == 0 {
				want = tc.rounded
			}
			if precision == -1 {
				want = tc.reduced
			}
			if precision == -15 {
				want = tc.minimum
			}
			got, err := d.OutputWithEnvironment(ctx, v, OutputEnvironment{FloatPrecisionSet: true, ExtraFloatDigits: precision})
			if err != nil || got != want {
				t.Fatalf("%s/%s precision%d = %q, %v; want %q", d.Name(), tc.input, precision, got, err, want)
			}
			if math.Float64bits(v.floating) != bits {
				t.Fatal("output mutated stored bits")
			}
		}
		canonical, err := d.Output(ctx, v)
		if err != nil || canonical != tc.shortest {
			t.Fatal("zero environment", canonical, err)
		}
		literal, err := d.SQLLiteral(ctx, v)
		if err != nil || literal != "'"+tc.shortest+"'" {
			t.Fatal("canonical literal", literal, err)
		}
	}
}

func TestFormatEncodingDomainArrayCapture(t *testing.T) {
	ctx := context.Background()
	types := testTypes()
	domain, err := Domain(45001, "float_domain", "public.float_domain", types[Float8].Usage())
	if err != nil {
		t.Fatal(err)
	}
	v, err := domain.Input(ctx, "1.2345678901234567", -1)
	if err != nil {
		t.Fatal(err)
	}
	array, err := Array(45002, "_float_domain", domain)
	if err != nil {
		t.Fatal(err)
	}
	values, err := ArrayValue(array, []ArrayDimension{{Lower: 1, Length: 1}}, []Value{v})
	if err != nil {
		t.Fatal(err)
	}
	env := OutputEnvironment{FloatPrecisionSet: true, ExtraFloatDigits: 0}
	captured := env
	env.ExtraFloatDigits = -15
	for _, tc := range []struct {
		env           OutputEnvironment
		scalar, array string
	}{{captured, "1.23456789012346", "{1.23456789012346}"}, {env, "1", "{1}"}, {OutputEnvironment{}, "1.2345678901234567", "{1.2345678901234567}"}} {
		got, err := domain.OutputWithEnvironment(ctx, v, tc.env)
		if err != nil || got != tc.scalar {
			t.Fatal(got, err)
		}
		got, err = array.OutputWithEnvironment(ctx, values, tc.env)
		if err != nil || got != tc.array {
			t.Fatal(got, err)
		}
	}
	for _, target := range []*Descriptor{types[Text], types[VarChar], types[BPChar]} {
		plan, err := PlanCoercion(domain.Usage(), target.Usage(), Explicit)
		if err != nil || !plan.SessionDependent() {
			t.Fatal("float IO classification", plan, err)
		}
		got, err := plan.Apply(ctx, v, InputEnvironment{}, captured)
		if err != nil || got.text != "1.23456789012346" {
			t.Fatal(got, err)
		}
	}
	if _, err := PlanCoercion(types[Numeric].Usage(), types[Text].Usage(), Explicit); err != nil {
		t.Fatal("released numeric output I/O refused", err)
	}
}

func TestFormatEncodingLocaleConsumption(t *testing.T) {
	ctx := context.Background()
	types := testTypes()
	for _, name := range []string{"C", "POSIX", ""} {
		locale, ok := ParseCLocale(name)
		if !ok || locale.policy().decimal != "." || locale.policy().grouping != "," || locale.policy().positive != "+" || locale.policy().negative != "-" || locale.policy().currency != " " || locale.policy().months[9] != "October" || locale.policy().days[4] != "Thursday" {
			t.Fatal(name, locale, ok)
		}
	}
	for _, name := range []string{"c", "posix", "C.UTF-8", "missing", "é🙂"} {
		if _, ok := ParseCLocale(name); ok {
			t.Fatal("unbounded locale accepted", name)
		}
	}
	// Private test-owned policy values prove actual category consumption; they
	// are not SQL locale registrations or advertised platform providers.
	numeric := cLocale
	numeric.decimal = ":"
	numeric.grouping = "_"
	numeric.positive = "P"
	numeric.negative = "N"
	monetary := cLocale
	monetary.currency = "¤"
	months, days := datetimeMonths, datetimeDays
	months[9] = "Harvest"
	days[4] = "Marketday"
	temporal := cLocale
	temporal.months = &months
	temporal.days = &days
	input := InputEnvironment{NumericLocale: Locale{&numeric}, MonetaryLocale: Locale{&monetary}, TimeLocale: Locale{&temporal}}
	output := OutputEnvironment{NumericLocale: input.NumericLocale, MonetaryLocale: input.MonetaryLocale, TimeLocale: input.TimeLocale}
	for _, tc := range []struct{ source, mask, want string }{{"-1234.5", "FMS9G999D00", "N1_234:50"}, {"12", "FML99", "¤12"}, {"1234.5", "FM9,999.00", "1,234.50"}} {
		source, err := types[Numeric].Input(ctx, tc.source, -1)
		if err != nil {
			t.Fatal(err)
		}
		plan, err := BindNumericFormat(ctx, "to_char", types[Text].Usage(), types[Numeric].Usage(), &tc.mask)
		if err != nil {
			t.Fatal(err)
		}
		value, err := plan.Evaluate(ctx, source, String(types[Text], tc.mask), input, output)
		if err != nil || value.text != tc.want {
			t.Fatal("numeric category consumption", value, err, tc.want)
		}
	}
	mask := "S9G999D99"
	reader, err := BindNumericFormat(ctx, "to_number", types[Numeric].Usage(), types[Text].Usage(), &mask)
	if err != nil {
		t.Fatal(err)
	}
	value, err := reader.Evaluate(ctx, String(types[Text], "N1_234:50"), String(types[Text], mask), input, output)
	if err != nil {
		t.Fatal(err)
	}
	text, err := types[Numeric].Output(ctx, value)
	if err != nil || text != "-1234.50" {
		t.Fatal("numeric input category", text, err)
	}
	date, err := types[Timestamp].Input(ctx, "2006-10-19", -1)
	if err != nil {
		t.Fatal(err)
	}
	mask = "TMMonth TMDay"
	writer, err := BindDatetimeFormat(ctx, "to_char", types[Text].Usage(), types[Timestamp].Usage(), &mask)
	if err != nil {
		t.Fatal(err)
	}
	value, err = writer.Evaluate(ctx, date, String(types[Text], mask), input, output)
	if err != nil || value.text != "Harvest Marketday" {
		t.Fatal("time output category", value, err)
	}
	mask = "DD TMMonth YYYY"
	timeReader, err := BindDatetimeFormat(ctx, "to_date", types[Date].Usage(), types[Text].Usage(), &mask)
	if err != nil {
		t.Fatal(err)
	}
	value, err = timeReader.Evaluate(ctx, String(types[Text], "05 Harvest 2000"), String(types[Text], mask), input, output)
	if err != nil {
		t.Fatal(err)
	}
	text, err = types[Date].Output(ctx, value)
	if err != nil || text != "2000-10-05" {
		t.Fatal("time input category", text, err)
	}
}

func TestFormatEncodingCharacterTypmods(t *testing.T) {
	ctx := context.Background()
	types := testTypes()
	for _, tc := range []struct {
		id                   ID
		precision            int32
		input, short, padded string
	}{
		{Float4, 1, "1.23456789", "1.234567", "1.2345679" + strings.Repeat(" ", 11)},
		{Float4, 0, "1.23456789", "1.23457", "1.23457" + strings.Repeat(" ", 13)},
		{Float4, -15, "1.23456789", "1", "1" + strings.Repeat(" ", 19)},
		{Float8, 1, "1.2345678901234567", "1.234567", "1.2345678901234567" + strings.Repeat(" ", 2)},
		{Float8, 0, "1.2345678901234567", "1.234567", "1.23456789012346" + strings.Repeat(" ", 4)},
		{Float8, -15, "1.2345678901234567", "1", "1" + strings.Repeat(" ", 19)},
	} {
		source := types[tc.id]
		value, err := source.Input(ctx, tc.input, -1)
		if err != nil {
			t.Fatal(err)
		}
		env := OutputEnvironment{FloatPrecisionSet: true, ExtraFloatDigits: tc.precision}
		for _, target := range []struct {
			id     ID
			typmod int32
			want   string
		}{{VarChar, 12, tc.short}, {BPChar, 24, tc.padded}} {
			usage := types[target.id].Usage()
			usage.Typmod = target.typmod
			plan, err := PlanCoercion(source.Usage(), usage, Explicit)
			if err != nil {
				t.Fatal(err)
			}
			got, err := plan.Apply(ctx, value, InputEnvironment{}, env)
			if err != nil || got.text != target.want {
				t.Fatal(tc, target, got, err)
			}
			null, err := plan.Apply(ctx, Null(source), InputEnvironment{}, env)
			if err != nil || !null.IsNull() || null.Type() != usage.Type {
				t.Fatal("typed NULL sizing", null, err)
			}
			assignment, err := PlanCoercion(source.Usage(), usage, Assignment)
			if err != nil {
				t.Fatal(err)
			}
			assigned, err := assignment.Apply(ctx, value, InputEnvironment{}, env)
			if target.id == VarChar && len(strings.TrimRight(tc.padded, " ")) > 8 {
				var diagnostic *sqlstate.Error
				if !errors.As(err, &diagnostic) || diagnostic.Code != "22001" {
					t.Fatal("float assignment incorrectly adopted explicit truncation", tc, assigned, err)
				}
			} else if err != nil || assigned.text != target.want {
				t.Fatal("existing float assignment changed", tc, assigned, err)
			}
		}
	}
	// Existing non-float assignment sizing must remain strict.
	target := types[VarChar].Usage()
	target.Typmod = 12
	plan, err := PlanCoercion(types[Text].Usage(), target, Assignment)
	if err != nil {
		t.Fatal(err)
	}
	_, err = plan.Apply(ctx, String(types[Text], "123456789"), InputEnvironment{}, OutputEnvironment{})
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "22001" {
		t.Fatal("unrelated strict assignment changed", err)
	}
}
