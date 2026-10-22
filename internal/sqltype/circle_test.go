package sqltype

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"math"
	"stuntgres/internal/sqlstate"
	"testing"
)

func circleUnitInput(t *testing.T, d *Descriptor, text string) Value {
	t.Helper()
	v, err := d.Input(context.Background(), text, -1)
	if err != nil {
		t.Fatal(text, err)
	}
	return v
}
func circleUnitError(t *testing.T, err error, code, message string) {
	t.Helper()
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != code || diagnostic.Message != message {
		t.Fatalf("got %v; want %s %s", err, code, message)
	}
}
func TestCircleInputOutput(t *testing.T) {
	ctx := context.Background()
	types := testTypes()
	d := types[Circle]
	for _, text := range []string{"<(1,2),3>", "((1,2),3)", "(1,2),3", "1,2,3", " < ( 1 , 2 ) , 3 > ", "<(1,2)3>", "((1,2)3)", "<(1,2),3)", "((1,2),3>"} {
		v := circleUnitInput(t, d, text)
		got, err := d.Output(ctx, v)
		if err != nil || got != "<(1,2),3>" {
			t.Fatal(text, got, err)
		}
	}
	v := circleUnitInput(t, d, "0.10000000000000002,5e-324,3")
	got, err := d.OutputWithEnvironment(ctx, v, OutputEnvironment{FloatPrecisionSet: true, ExtraFloatDigits: 0})
	if err != nil || got != "<(0.1,4.94065645841247e-324),3>" {
		t.Fatal(got, err)
	}
	literal, err := d.SQLLiteral(ctx, v)
	if err != nil || literal != "'<(0.10000000000000002,5e-324),3>'" {
		t.Fatal(literal, err)
	}
	round := circleUnitInput(t, d, literal[1:len(literal)-1])
	if !SameDatum(v, round) {
		t.Fatal("lossy literal")
	}
	metadata, ok := d.PGTypeMetadata()
	if !ok || metadata.Category != "G" || metadata.Input != 1450 || metadata.Output != 1451 || metadata.Receive != 2490 || metadata.Send != 2491 {
		t.Fatal(metadata, ok)
	}
	if equal, order := ComparisonCapabilities(d); equal || order {
		t.Fatal("default circle comparison capability")
	}
	if array := types[719]; array.Concrete() || !array.IsArray() || array.Element() != d || d.AssociatedArray() != 719 {
		t.Fatal("circle companion must retain canonical lookup-only array identity")
	}
	ctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := d.Input(ctx, "1,2,3", -1); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
func TestCircleNumericEdges(t *testing.T) {
	d := testTypes()[Circle]
	for _, tc := range []struct{ in, out string }{
		{"-0,0,-0", "<(-0,0),-0>"}, {"NaN,Infinity,NaN", "<(NaN,Infinity),NaN>"},
		{"-Infinity,+Infinity,+Inf", "<(-Infinity,Infinity),Infinity>"},
		{"0x1p0,0,1", "<(1,0),1>"}, {"+NaN,0,1", "<(NaN,0),1>"}, {"NaN(payload),0,1", "<(NaN,0),1>"},
		{"0.10000000000000002,5e-324,1.7976931348623157e308", "<(0.10000000000000002,5e-324),1.7976931348623157e+308>"},
	} {
		v := circleUnitInput(t, d, tc.in)
		text, err := d.Output(context.Background(), v)
		if err != nil || text != tc.out {
			t.Fatal(tc.in, text, err)
		}
	}
	for _, text := range []string{"", "bad", "0,0,-1", "0,0,-Infinity", "(1 2),3", "<(1,2),3", "1,2,3junk", "1,2", "1,,3"} {
		_, err := d.Input(context.Background(), text, -1)
		circleUnitError(t, err, "22P02", "invalid input syntax for type circle: \""+text+"\"")
	}
	for _, tc := range []struct{ in, token string }{{"1e999,broken", "1e999"}, {"0,0,1e-999", "1e-999"}, {"(1,1e999junk),3", "1e999"}} {
		_, err := d.Input(context.Background(), tc.in, -1)
		circleUnitError(t, err, "22003", "\""+tc.token+"\" is out of range for type double precision")
	}
}
func TestCircleDatumIdentity(t *testing.T) {
	d := testTypes()[Circle]
	zero := circleUnitInput(t, d, "0,0,0")
	for _, text := range []string{"-0,0,0", "0,-0,0", "0,0,-0", "0,0,0.0000001"} {
		if SameDatum(zero, circleUnitInput(t, d, text)) {
			t.Fatal("collapsed representation", text)
		}
	}
	nan := Value{typ: d, circle: circleDatum{math.Float64frombits(0x7ff8000000000001), 0, 1}}
	other := nan
	other.circle.x = math.Float64frombits(0x7ff8000000000002)
	if !SameDatum(nan, nan) || SameDatum(nan, other) {
		t.Fatal("NaN payload identity")
	}
	if SameDatum(zero, circleUnitInput(t, testTypes()[Circle], "0,0,0")) {
		t.Fatal("foreign descriptor")
	}
}
func TestCircleBinary(t *testing.T) {
	ctx := context.Background()
	d := testTypes()[Circle]
	raw, _ := hex.DecodeString("3ff000000000000040000000000000004008000000000000")
	v, err := d.BinaryInputWithEnvironment(ctx, raw, -1, InputEnvironment{})
	if err != nil || !SameDatum(v, circleUnitInput(t, d, "1,2,3")) {
		t.Fatal(v, err)
	}
	got, err := d.AppendBinaryWithEnvironment(ctx, nil, v, OutputEnvironment{})
	if err != nil || !bytes.Equal(got, raw) {
		t.Fatal(got, err)
	}
	for _, text := range []string{"-0,0,-0", "NaN,Infinity,NaN"} {
		v := circleUnitInput(t, d, text)
		got, err := d.AppendBinaryWithEnvironment(ctx, nil, v, OutputEnvironment{})
		if err != nil {
			t.Fatal(err)
		}
		round, err := d.BinaryInputWithEnvironment(ctx, got, -1, InputEnvironment{})
		if err != nil || !SameDatum(v, round) {
			t.Fatal(text, err)
		}
	}
	_, err = d.BinaryInputWithEnvironment(ctx, raw[:23], -1, InputEnvironment{})
	circleUnitError(t, err, "08P01", "insufficient data left in message")
	_, err = d.BinaryInputWithEnvironment(ctx, append(raw, 0), -1, InputEnvironment{})
	circleUnitError(t, err, "22P03", "incorrect binary data format")
	binary.BigEndian.PutUint64(raw[16:], math.Float64bits(-1))
	_, err = d.BinaryInputWithEnvironment(ctx, raw, -1, InputEnvironment{})
	circleUnitError(t, err, "22P03", "invalid radius in external \"circle\" value")
}
func TestCircleOperators(t *testing.T) {
	types := testTypes()
	d := types[Circle]
	ctx := context.Background()
	for _, tc := range []struct {
		op, a, b string
		want     bool
	}{
		{"=", "0,0,2", "9,9,2", true}, {"~=", "0,0,2", "9,9,2", false}, {"~=", "NaN,0,NaN", "NaN,0,NaN", true},
		{"<", "0,0,1", "9,9,2", true}, {"<=", "0,0,1", "0,0,1", true}, {">=", "0,0,1", "0,0,1", true},
		{"<>", "0,0,1", "0,0,2", true}, {">", "0,0,1", "0,0,2", false},
		{"<<", "0,0,1", "5,0,1", true}, {">>", "5,0,1", "0,0,1", true}, {"&<", "0,0,1", "5,0,1", true}, {"&>", "5,0,1", "0,0,1", true},
		{"<<|", "0,0,1", "0,5,1", true}, {"|>>", "0,5,1", "0,0,1", true}, {"&<|", "0,0,1", "0,5,1", true}, {"|&>", "0,5,1", "0,0,1", true},
		{"&&", "0,0,1", "2,0,1", true}, {"@>", "0,0,2", "1,0,1", true}, {"<@", "1,0,1", "0,0,2", true},
		{"=", "0,0,1", "9,9,1.0000001", true}, {"<>", "0,0,1", "9,9,1.0000002", true},
	} {
		op, _ := CircleKind(tc.op)
		p, err := BindCircle(op, types[Bool].Usage(), d.Usage(), d.Usage())
		if err != nil {
			t.Fatal(err)
		}
		args := [2]Value{circleUnitInput(t, d, tc.a), circleUnitInput(t, d, tc.b)}
		v, err := p.Evaluate(ctx, args)
		if err != nil || v.boolean != tc.want {
			t.Fatal(tc, v, err)
		}
		for i := range args {
			nulls := args
			nulls[i] = Null(d)
			v, err = p.Evaluate(ctx, nulls)
			if err != nil || !v.null || v.typ != types[Bool] {
				t.Fatal(tc, v, err)
			}
		}
	}
	for op := CircleEqual; op <= CircleGreaterEqual; op++ {
		p, _ := BindCircle(op, types[Bool].Usage(), d.Usage(), d.Usage())
		nan := circleUnitInput(t, d, "0,0,NaN")
		v, err := p.Evaluate(ctx, [2]Value{nan, nan})
		if err != nil || v.boolean {
			t.Fatal("NaN area", op, v, err)
		}
	}
	for _, tc := range []struct {
		a, b string
		want float64
	}{{"0,0,1", "5,0,1", 3}, {"0,0,2", "1,0,1", 0}} {
		p, _ := BindCircle(CircleDistance, types[Float8].Usage(), d.Usage(), d.Usage())
		v, err := p.Evaluate(ctx, [2]Value{circleUnitInput(t, d, tc.a), circleUnitInput(t, d, tc.b)})
		if err != nil || v.floating != tc.want {
			t.Fatal(v, err)
		}
	}
	for _, text := range []string{"0,0,1e200", "0,0,1e-200"} {
		p, _ := BindCircle(CircleEqual, types[Bool].Usage(), d.Usage(), d.Usage())
		v := circleUnitInput(t, d, text)
		_, err := p.Evaluate(ctx, [2]Value{v, v})
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "22003" {
			t.Fatal("area range", text, err)
		}
	}
	if _, err := BindCircle(CircleEqual, testTypes()[Bool].Usage(), d.Usage(), d.Usage()); err == nil {
		t.Fatal("foreign result")
	}
	for op := CompareEqual; op <= CompareGreaterEqual; op++ {
		s, err := ResolveComparison(op, d, nil)
		if err != nil || s != (EqualitySignature{Circle, Circle}) {
			t.Fatal(s, err)
		}
		predicate, err := BindComparisonPredicate(op, d, d, types[Bool], CollationRef{}, false)
		if err != nil {
			t.Fatal(op, err)
		}
		same, err := BindComparisonPredicate(op, d, d, types[Bool], CollationRef{}, false)
		if err != nil || !predicate.Equal(same) {
			t.Fatal("retained predicate identity", op, err)
		}
		for _, input := range []struct {
			a, b string
			want bool
		}{
			{"0,0,2", "9,9,2", op == CompareEqual || op == CompareLessEqual || op == CompareGreaterEqual},
			{"0,0,NaN", "0,0,NaN", false},
		} {
			value, err := predicate.Evaluate(ctx, circleUnitInput(t, d, input.a), circleUnitInput(t, d, input.b), InputEnvironment{})
			if err != nil || value.boolean != input.want || value.IsNull() {
				t.Fatal("shared scalar predicate", op, input, value, err)
			}
		}
		value, err := predicate.Evaluate(ctx, Null(d), circleUnitInput(t, d, "0,0,1"), InputEnvironment{})
		if err != nil || !value.IsNull() || value.Type() != types[Bool] {
			t.Fatal("shared strict NULL", op, value, err)
		}
		if predicate.EqualityFor(d.Usage()) {
			t.Fatal("circle area predicate advertised hash equality", op)
		}
		if _, err := predicate.EqualityGrouping(); err == nil {
			t.Fatal("circle area predicate acquired hash grouping", op)
		}
	}
	if _, err := BindComparison(d, d, CollationRef{}, false); err == nil {
		t.Fatal("default comparator")
	}
	t.Run("distance_edges", circleUnitDistanceEdges)
}

func TestCircleDefaultCapabilities(t *testing.T) {
	d := testTypes()[Circle]
	if equality, ordering := ComparisonCapabilities(d); equality || ordering {
		t.Fatal("scalar circle predicates must not grant default capabilities", equality, ordering)
	}
	if class, ok := DefaultIndexClass(d); ok {
		t.Fatal("circle must not acquire a default index class", class)
	}
	_, err := BindOrdering(d.Usage())
	want := sqlstate.Error{
		Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883",
		Message: "could not identify an ordering operator for type circle",
		Hint:    "Use an explicit ordering operator or modify the query.",
	}
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || *diagnostic != want {
		t.Fatalf("got %#v (%v), want %#v", diagnostic, err, want)
	}
}

func circleUnitDistanceEdges(t *testing.T) {
	types := testTypes()
	d := types[Circle]
	p, err := BindCircle(CircleDistance, types[Float8].Usage(), d.Usage(), d.Usage())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		a, b string
		want float64
	}{
		{"Infinity,0,1", "0,0,1", math.Inf(1)},
		{"NaN,0,1", "0,0,1", math.NaN()},
		{"Infinity,0,1", "Infinity,0,1", math.NaN()},
		{"1e308,0,1", "0,0,1", 1e308},
	} {
		v, err := p.Evaluate(context.Background(), [2]Value{circleUnitInput(t, d, tc.a), circleUnitInput(t, d, tc.b)})
		if err != nil || v.floating != tc.want && !(math.IsNaN(v.floating) && math.IsNaN(tc.want)) {
			t.Fatal(tc, v, err)
		}
	}
	_, err = p.Evaluate(context.Background(), [2]Value{circleUnitInput(t, d, "1e308,0,1"), circleUnitInput(t, d, "-1e308,0,1")})
	circleUnitError(t, err, "22003", "value out of range: overflow")
	_, err = p.Evaluate(context.Background(), [2]Value{circleUnitInput(t, d, "0,0,1"), circleUnitInput(t, testTypes()[Circle], "0,0,1")})
	if err == nil {
		t.Fatal("foreign input")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.Evaluate(ctx, [2]Value{Null(d), Null(d)}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
