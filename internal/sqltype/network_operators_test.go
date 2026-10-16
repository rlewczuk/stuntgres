package sqltype

import (
	"context"
	"errors"
	"math"
	"stuntgres/internal/sqlstate"
	"testing"
)

func networkOperatorValue(t *testing.T, d *Descriptor, text string) Value {
	t.Helper()
	v, err := d.Input(context.Background(), text, -1)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func networkOperatorDiagnostic(t *testing.T, err error, code, message, context string) {
	t.Helper()
	var d *sqlstate.Error
	if !errors.As(err, &d) || d.Code != code || d.Message != message || d.Context != context || d.Position != "" || d.Detail != "" || d.Hint != "" {
		t.Fatal("wrong network diagnostic", err)
	}
}

func TestNetworkOperatorBitwise(t *testing.T) {
	inet := testTypes()[Inet]
	ctx := context.Background()
	for _, tc := range []struct {
		op         BitwiseOperator
		a, b, want string
	}{
		{BitwiseNot, "192.168.1.6/24", "", "63.87.254.249/24"},
		{BitwiseNot, "::/64", "", "ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff/64"},
		{BitwiseNot, "ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff", "", "::"},
		{BitwiseAnd, "192.168.1.6/24", "0.0.0.255/25", "0.0.0.6/25"},
		{BitwiseOr, "192.168.1.6/24", "0.0.0.255/25", "192.168.1.255/25"},
		{BitwiseAnd, "2001:db8:aaaa:5555:ffff::f0/64", "ffff:ffff:5555:aaaa::ff/96", "2001:db8::f0/96"},
		{BitwiseOr, "2001:db8:aaaa:5555:ffff::f0/64", "ffff:ffff:5555:aaaa::ff/96", "ffff:ffff:ffff:ffff:ffff::ff/96"},
	} {
		a := networkOperatorValue(t, inet, tc.a)
		var b Value
		right := inet
		if tc.op == BitwiseNot {
			right = nil
		} else {
			b = networkOperatorValue(t, inet, tc.b)
		}
		p, err := BindNetworkBitwise(tc.op, inet, right)
		if err != nil {
			t.Fatal(err)
		}
		beforeA, beforeB := a, b
		var got Value
		if right == nil {
			got, err = p.Unary(ctx, a)
		} else {
			got, err = p.Binary(ctx, a, b)
		}
		if err != nil || got.typ != inet || got.null || !got.network.valid(false) || got.network.output(false) != tc.want {
			t.Fatal(tc, got, err)
		}
		if a != beforeA || b != beforeB {
			t.Fatal("input mutation")
		}
		if right != nil {
			reverse, err := p.Binary(ctx, b, a)
			if err != nil || reverse != got {
				t.Fatal("noncommutative bitwise", reverse, err)
			}
		}
	}
	for _, op := range []BitwiseOperator{BitwiseAnd, BitwiseOr} {
		p, err := BindNetworkBitwise(op, inet, inet)
		if err != nil {
			t.Fatal(err)
		}
		name := "AND"
		if op == BitwiseOr {
			name = "OR"
		}
		for _, v6 := range []string{"::1", "::ffff:1.2.3.4"} {
			a, b := networkOperatorValue(t, inet, "1.2.3.4"), networkOperatorValue(t, inet, v6)
			_, err := p.Binary(ctx, a, b)
			networkOperatorDiagnostic(t, err, "22023", "cannot "+name+" inet values of different sizes", "")
			for _, pair := range [][2]Value{{Null(inet), b}, {a, Null(inet)}} {
				v, err := p.Binary(ctx, pair[0], pair[1])
				if err != nil || !v.null || v.typ != inet {
					t.Fatal(v, err)
				}
			}
		}
	}
}

func TestNetworkOperatorArithmetic(t *testing.T) {
	types := testTypes()
	inet, int8 := types[Inet], types[Int8]
	ctx := context.Background()
	for _, tc := range []struct {
		op      ArithmeticOperator
		address string
		offset  int64
		want    string
		fails   bool
	}{
		{ArithmeticAdd, "192.168.1.6", 25, "192.168.1.31", false},
		{ArithmeticSubtract, "192.168.1.43", 36, "192.168.1.7", false},
		{ArithmeticAdd, "192.168.1.255/24", 1, "192.168.2.0/24", false},
		{ArithmeticAdd, "::ffff:ffff:ffff:ffff", 1, "0:0:0:1::", false},
		{ArithmeticSubtract, "0:0:0:1::", 1, "::ffff:ffff:ffff:ffff", false},
		{ArithmeticAdd, "2001:db8::ffff/64", 1, "2001:db8::1:0/64", false},
		{ArithmeticAdd, "::8000:0:0:0", math.MinInt64, "::", false},
		{ArithmeticSubtract, "::8000:0:0:0", math.MinInt64, "::", false},
		{ArithmeticSubtract, "::", math.MinInt64 + 1, "::7fff:ffff:ffff:ffff", false},
		{ArithmeticSubtract, "::", math.MinInt64, "", true},
		{ArithmeticAdd, "0.0.0.0", -1, "", true},
		{ArithmeticSubtract, "0.0.0.0", 1, "", true},
		{ArithmeticAdd, "255.255.255.255", 1, "", true},
		{ArithmeticAdd, "::", -1, "", true},
		{ArithmeticAdd, "ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff", 1, "", true},
		{ArithmeticAdd, "1.2.3.4", 10000000000, "", true},
		{ArithmeticAdd, "1.2.3.4", -10000000000, "", true},
	} {
		a := networkOperatorValue(t, inet, tc.address)
		b, err := Integer(int8, tc.offset)
		if err != nil {
			t.Fatal(err)
		}
		for _, commute := range []bool{false, true} {
			if commute && tc.op != ArithmeticAdd {
				continue
			}
			left, right := a, b
			if commute {
				left, right = b, a
			}
			p, err := BindArithmetic(tc.op, left.typ, right.typ, inet)
			if err != nil {
				t.Fatal(err)
			}
			got, err := p.Binary(ctx, left, right, InputEnvironment{})
			if tc.fails {
				context := ""
				if commute {
					context = "SQL function \"int8pl_inet\" statement 1"
				}
				networkOperatorDiagnostic(t, err, "22003", "result is out of range", context)
			} else if err != nil || got.typ != inet || !got.network.valid(false) || got.network.output(false) != tc.want {
				t.Fatal(tc, commute, got, err)
			}
			if a != networkOperatorValue(t, inet, tc.address) {
				t.Fatal("arithmetic input mutated")
			}
		}
	}
	p, err := BindArithmetic(ArithmeticSubtract, inet, inet, int8)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		a, b  string
		want  int64
		fails bool
	}{
		{"192.168.1.43", "192.168.1.19", 24, false}, {"0.0.0.0", "255.255.255.255", -4294967295, false},
		{"255.255.255.255", "0.0.0.0", 4294967295, false}, {"::1/64", "::1/128", 0, false},
		{"::7fff:ffff:ffff:ffff", "::", math.MaxInt64, false}, {"::", "::8000:0:0:0", math.MinInt64, false},
		{"::8000:0:0:0", "::", 0, true}, {"::", "::8000:0:0:1", 0, true},
		{"ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff", "::", -1, false}, {"::", "ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff", 1, false},
	} {
		a, b := networkOperatorValue(t, inet, tc.a), networkOperatorValue(t, inet, tc.b)
		got, err := p.Binary(ctx, a, b, InputEnvironment{})
		if tc.fails {
			networkOperatorDiagnostic(t, err, "22003", "result is out of range", "")
		} else if err != nil || got.typ != int8 || got.integer != tc.want {
			t.Fatal(tc, got, err)
		}
	}
	_, err = p.Binary(ctx, networkOperatorValue(t, inet, "1.2.3.4"), networkOperatorValue(t, inet, "::1"), InputEnvironment{})
	networkOperatorDiagnostic(t, err, "22023", "cannot subtract inet values of different sizes", "")
	// Every byte boundary, including upper IPv6 bytes, carries and borrows
	// without changing its prefix or leaving nonzero IPv4 tail bytes.
	for _, family := range []uint8{4, 6} {
		width := 4
		if family == 6 {
			width = 16
		}
		for boundary := 1; boundary < width; boundary++ {
			n := networkDatum{family: family, prefix: 17}
			for i := boundary; i < width; i++ {
				n.address[i] = 0xff
			}
			before := n
			got, ok := networkOffset(n, 1)
			want := networkDatum{family: family, prefix: 17}
			want.address[boundary-1] = 1
			if !ok || got != want || n != before {
				t.Fatal(family, boundary, got)
			}
			back, ok := networkOffset(got, -1)
			if !ok || back != before {
				t.Fatal("borrow", family, boundary, back)
			}
		}
	}
}

func TestNetworkOperatorResolution(t *testing.T) {
	types := testTypes()
	for _, tc := range []struct {
		op                  ArithmeticOperator
		left, right, result ID
	}{
		{ArithmeticAdd, Inet, Int8, Inet}, {ArithmeticAdd, Int8, Inet, Inet},
		{ArithmeticSubtract, Inet, Int8, Inet}, {ArithmeticSubtract, Inet, Inet, Int8},
		{ArithmeticAdd, Cidr, Int4, Inet}, {ArithmeticAdd, Int2, Cidr, Inet},
	} {
		s, err := ResolveArithmetic(tc.op, types[tc.left], types[tc.right], false)
		if err != nil || s.Result != tc.result {
			t.Fatal(tc, s, err)
		}
		p, err := BindArithmetic(tc.op, types[s.Left], types[s.Right], types[s.Result])
		if err != nil || p.SessionDependent() {
			t.Fatal(p, err)
		}
	}
	for _, tc := range []struct {
		op          ArithmeticOperator
		left, right ID
	}{
		{ArithmeticSubtract, Int8, Inet}, {ArithmeticAdd, Inet, Inet}, {ArithmeticAdd, Inet, Numeric}, {ArithmeticMultiply, Inet, Int8},
	} {
		if _, err := ResolveArithmetic(tc.op, types[tc.left], types[tc.right], false); err == nil {
			t.Fatal("unsupported signature", tc)
		}
	}
	for _, op := range []ArithmeticOperator{ArithmeticAdd, ArithmeticSubtract} {
		if _, err := ResolveArithmetic(op, types[Inet], nil, true); err == nil {
			t.Fatal("unary network arithmetic")
		}
	}
	s, err := ResolveArithmetic(ArithmeticSubtract, types[Inet], nil, false)
	if err != nil || s != (ArithmeticSignature{Inet, Inet, Int8}) {
		t.Fatal("unknown subtraction", s, err)
	}
	s, err = ResolveArithmetic(ArithmeticAdd, types[Inet], nil, false)
	if err != nil || s != (ArithmeticSignature{Inet, Int8, Inet}) {
		t.Fatal("unknown offset", s, err)
	}
	for _, op := range []BitwiseOperator{BitwiseXor, BitwiseShiftLeft, BitwiseShiftRight, BitwiseOperator(255)} {
		if _, err := BindNetworkBitwise(op, types[Inet], types[Inet]); err == nil {
			t.Fatal("network signature widened", op)
		}
	}
	for _, pair := range [][2]*Descriptor{{nil, nil}, {types[Cidr], nil}, {types[Inet], types[Cidr]}, {types[Inet], testTypes()[Inet]}} {
		if _, err := BindNetworkBitwise(BitwiseAnd, pair[0], pair[1]); err == nil {
			t.Fatal("invalid descriptor pair")
		}
	}
	if _, err := BindNetworkBitwise(BitwiseNot, types[Inet], types[Inet]); err == nil {
		t.Fatal("unary accepted right")
	}
	for _, sig := range [][3]*Descriptor{{types[Inet], types[Int8], types[Int8]}, {types[Cidr], types[Int8], types[Inet]}, {types[Inet], types[Inet], types[Inet]}} {
		if _, err := BindArithmetic(ArithmeticSubtract, sig[0], sig[1], sig[2]); err == nil {
			t.Fatal("invalid arithmetic bind")
		}
	}
}

func TestNetworkOperatorOwnershipAndCancellation(t *testing.T) {
	types, foreign := testTypes(), testTypes()
	inet, int8 := types[Inet], types[Int8]
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	a := networkOperatorValue(t, inet, "::3")
	b := networkOperatorValue(t, inet, "::1")
	for _, op := range []BitwiseOperator{BitwiseNot, BitwiseAnd, BitwiseOr} {
		right := inet
		if op == BitwiseNot {
			right = nil
		}
		p, err := BindNetworkBitwise(op, inet, right)
		if err != nil {
			t.Fatal(err)
		}
		for _, null := range []bool{false, true} {
			x := a
			if null {
				x = Null(inet)
			}
			var got Value
			if right == nil {
				got, err = p.Unary(ctx, x)
			} else {
				got, err = p.Binary(ctx, x, b)
			}
			if !errors.Is(err, context.Canceled) || got.typ != nil {
				t.Fatal(got, err)
			}
		}
		wrong := networkOperatorValue(t, foreign[Inet], "::3")
		if right == nil {
			_, err = p.Unary(context.Background(), wrong)
		} else {
			_, err = p.Binary(context.Background(), a, wrong)
		}
		if err == nil {
			t.Fatal("foreign bitwise value accepted")
		}
		invalid := a
		invalid.network.family = 7
		if right == nil {
			_, err = p.Unary(context.Background(), invalid)
		} else {
			_, err = p.Binary(context.Background(), invalid, b)
		}
		if err == nil {
			t.Fatal("invalid network payload accepted")
		}
	}
	one, err := Integer(int8, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		op     ArithmeticOperator
		a, b   Value
		result *Descriptor
	}{
		{ArithmeticAdd, a, one, inet}, {ArithmeticAdd, one, a, inet}, {ArithmeticSubtract, a, one, inet}, {ArithmeticSubtract, a, b, int8},
	} {
		p, err := BindArithmetic(tc.op, tc.a.typ, tc.b.typ, tc.result)
		if err != nil {
			t.Fatal(err)
		}
		for _, pair := range [][2]Value{{tc.a, tc.b}, {Null(tc.a.typ), tc.b}, {tc.a, Null(tc.b.typ)}} {
			got, err := p.Binary(ctx, pair[0], pair[1], InputEnvironment{})
			if !errors.Is(err, context.Canceled) || got.typ != nil {
				t.Fatal(got, err)
			}
		}
		for _, pair := range [][2]Value{{Null(tc.a.typ), tc.b}, {tc.a, Null(tc.b.typ)}} {
			got, err := p.Binary(context.Background(), pair[0], pair[1], InputEnvironment{})
			if err != nil || !got.null || got.typ != tc.result {
				t.Fatal(got, err)
			}
		}
		wrong, err := tc.a.Retag(foreign[tc.a.typ.id])
		if err != nil {
			t.Fatal(err)
		}
		if _, err := p.Binary(context.Background(), wrong, tc.b, InputEnvironment{}); err == nil {
			t.Fatal("foreign arithmetic value accepted")
		}
	}
	invalid := networkOperatorValue(t, inet, "1.2.3.4")
	invalid.network.address[15] = 1
	p, err := BindArithmetic(ArithmeticAdd, inet, int8, inet)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Binary(context.Background(), invalid, one, InputEnvironment{}); err == nil {
		t.Fatal("IPv4 tail contamination accepted")
	}
}

func TestNetworkOperatorAllocations(t *testing.T) {
	types := testTypes()
	inet, int8 := types[Inet], types[Int8]
	ctx := context.Background()
	a, b := networkOperatorValue(t, inet, "2001:db8::1/64"), networkOperatorValue(t, inet, "2001:db8::/96")
	one, err := Integer(int8, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range []BitwiseOperator{BitwiseNot, BitwiseAnd, BitwiseOr} {
		right := inet
		if op == BitwiseNot {
			right = nil
		}
		p, err := BindNetworkBitwise(op, inet, right)
		if err != nil {
			t.Fatal(err)
		}
		var result Value
		allocs := testing.AllocsPerRun(100, func() {
			if right == nil {
				result, err = p.Unary(ctx, a)
			} else {
				result, err = p.Binary(ctx, a, b)
			}
		})
		if allocs != 0 || err != nil || result.typ != inet {
			t.Fatal(op, allocs, err)
		}
	}
	for _, tc := range []struct {
		op     ArithmeticOperator
		a, b   Value
		result *Descriptor
	}{
		{ArithmeticAdd, a, one, inet}, {ArithmeticAdd, one, a, inet}, {ArithmeticSubtract, a, one, inet}, {ArithmeticSubtract, a, b, int8},
	} {
		p, err := BindArithmetic(tc.op, tc.a.typ, tc.b.typ, tc.result)
		if err != nil {
			t.Fatal(err)
		}
		var result Value
		allocs := testing.AllocsPerRun(100, func() { result, err = p.Binary(ctx, tc.a, tc.b, InputEnvironment{}) })
		if allocs != 0 || err != nil || result.typ != tc.result {
			t.Fatal(tc.op, allocs, err)
		}
	}
}
