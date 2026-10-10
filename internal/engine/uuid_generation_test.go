package engine

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"math"
	"testing"
	"time"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func uuidUnitBytes(t *testing.T, text string) [16]byte {
	t.Helper()
	raw, err := hex.DecodeString(text)
	if err != nil || len(raw) != 16 {
		t.Fatal(text, err)
	}
	var result [16]byte
	copy(result[:], raw)
	return result
}
func TestUUIDGenerationPacking(t *testing.T) {
	zero := [16]byte{}
	var full [16]byte
	for i := range full {
		full[i] = 255
	}
	for _, tc := range []struct {
		input [16]byte
		want  string
	}{{zero, "00000000000040008000000000000000"}, {full, "ffffffffffff4fffbfffffffffffffff"}} {
		if got := uuidPackV4(tc.input); got != uuidUnitBytes(t, tc.want) {
			t.Fatalf("v4 got %x want %s", got, tc.want)
		}
	}
	for _, tc := range []struct {
		ms      uint64
		ns      int64
		entropy [16]byte
		want    string
	}{
		{0, 500000, zero, "00000000000078008000000000000000"},
		{1, 0, zero, "00000000000170008000000000000000"},
		{0x010203040506, 0, full, "0102030405067000bfffffffffffffff"},
		{0xffffffffffff, 999999, zero, "ffffffffffff7fff8000000000000000"},
	} {
		if got := uuidPackV7(tc.ms, tc.ns, tc.entropy, false); got != uuidUnitBytes(t, tc.want) {
			t.Fatalf("v7 got %x want %s", got, tc.want)
		}
	}
	for _, tc := range []struct {
		ns   int64
		want uint16
	}{{0, 0}, {244, 0}, {245, 1}, {999999, 4095}} {
		got := uuidPackV7(0, tc.ns, zero, false)
		fraction := uint16(got[6]&15)<<8 | uint16(got[7])
		if fraction != tc.want {
			t.Fatal(tc, fraction)
		}
	}
	for bits := range 4 {
		entropy := zero
		entropy[8] = byte(bits << 6)
		got := uuidPackV7(0, 0, entropy, true)
		if got[7] != byte(bits) || got[8] != 0x80 {
			t.Fatal("coarse fraction", bits, got)
		}
	}
}
func TestUUIDGenerationAscendingClock(t *testing.T) {
	for _, step := range []int64{245, 977} {
		for _, tc := range []struct{ previous, observed, want int64 }{{1000, 1000, 1000 + step}, {1000, 999, 1000 + step}, {1000, 1000 + step, 1000 + step}, {1000, 3000, 3000}, {999999, 1000000, 999999 + step}} {
			if got := uuidAscendingTime(tc.previous, tc.observed, step); got != tc.want {
				t.Fatal(step, tc, got)
			}
		}
	}
	if got := uuidPackV7(1, 244, [16]byte{}, false); got[5] != 1 || got[7] != 0 {
		t.Fatal("millisecond carry", got)
	}
}
func TestUUIDGenerationExactShiftBounds(t *testing.T) {
	for _, tc := range []struct {
		us    int64
		valid bool
		ms    uint64
	}{{0, true, 0}, {-1, false, 0}, {uuidMaxUnixMicroseconds, true, 0xffffffffffff}, {uuidMaxUnixMicroseconds + 1, false, 0}} {
		ms, ns, err := uuidShiftTimestamp(tc.us-uuidEpochMicroseconds, 999)
		if tc.valid {
			if err != nil || ms != tc.ms || ns != 999 {
				t.Fatal(tc, ms, ns, err)
			}
		} else {
			nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "22008", Message: "timestamp out of range for UUID version 7", Detail: "UUID version 7 supports timestamps from 1970-01-01 to approximately year 10889."})
		}
	}
	ms, ns, err := uuidShiftTimestamp(1001-uuidEpochMicroseconds, 321)
	if err != nil || ms != 1 || ns != 1321 {
		t.Fatal("submicrosecond preservation", ms, ns, err)
	}
	for _, tc := range []struct {
		m, d int32
		us   int64
		want bool
	}{{math.MinInt32, math.MinInt32, math.MinInt64, true}, {math.MaxInt32, math.MaxInt32, math.MaxInt64, true}, {math.MinInt32, 0, 0, false}, {0, math.MaxInt32, 0, false}, {0, 0, math.MaxInt64, false}} {
		if uuidInfiniteInterval(tc.m, tc.d, tc.us) != tc.want {
			t.Fatal(tc)
		}
	}
	// Even int64 extrema reject cleanly rather than overflowing epoch arithmetic.
	for _, us := range []int64{math.MinInt64, math.MaxInt64} {
		if _, _, err := uuidShiftTimestamp(us, 0); err == nil {
			t.Fatal("extreme timestamp admitted", us)
		}
	}
}
func TestUUIDGenerationBinderNullAndDemand(t *testing.T) {
	b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
	for _, name := range []string{"gen_random_uuid", "uuidv4", "uuidv7"} {
		node, err := b.bind(nullCountRaw(name), expressionPolicy{context: projectionContext})
		if err != nil {
			t.Fatal(err)
		}
		if node.ResultUsage().Type.ID() != sqltype.UUID || node.Properties().Volatility != Volatile {
			t.Fatal(name, node)
		}
	}
	node, err := b.bind(nullCountRaw("uuidv7", parser.ParameterExpr{Number: 1}), expressionPolicy{context: projectionContext})
	if err != nil {
		t.Fatal(err)
	}
	if !node.Properties().ParameterDependent || node.Properties().Volatility != Volatile || closedUUIDDefault(node) {
		t.Fatal("parameter dependence", node.Properties())
	}
	// No session is provided: reaching the clock/entropy provider would fail.
	calls := 0
	iv := b.typ(sqltype.Interval)
	uuid := b.typ(sqltype.UUID)
	probe := nullCountWitness{expressionInfo: expressionInfo{usage: iv.Usage()}, value: sqltype.Null(iv), calls: &calls}
	expr := uuidGenerationExpression{expressionInfo: expressionInfo{usage: uuid.Usage(), properties: ExprProperties{Volatility: Volatile}}, name: "uuidv7", args: []BoundExpr{probe}}
	result, err := expr.Eval(&EvalContext{Context: context.Background()})
	if err != nil || !result.IsNull() || result.Type() != uuid || calls != 1 {
		t.Fatal(result, err, calls)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = expr.Eval(&EvalContext{Context: canceled})
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatal("cancellation demanded child", err, calls)
	}
	// Tree inspection, descriptor remapping and rendering do not evaluate a child.
	stored, err := b.bind(nullCountRaw("uuidv7", parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "0"}}), expressionPolicy{context: projectionContext})
	if err != nil {
		t.Fatal(err)
	}
	other := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
	rebound, changed, err := retagBoundTypes(stored, func(d *sqltype.Descriptor) *sqltype.Descriptor { return other.typ(d.ID()) })
	if err != nil || !changed {
		t.Fatal("UUID retag", changed, err)
	}
	n := rebound.(uuidGenerationExpression)
	if n.timestamp != other.typ(sqltype.Timestamptz) || n.ResultUsage().Type != other.typ(sqltype.UUID) || n.args[0].ResultUsage().Type != other.typ(sqltype.Interval) || n.Properties() != stored.Properties() {
		t.Fatal("retag lost descriptor/dependence")
	}
	if generationImmutable(expr) {
		t.Fatal("UUID admitted to immutable consumer")
	}
	if closedUUIDDefault(randomExpression{expressionInfo: expressionInfo{usage: uuid.Usage(), properties: ExprProperties{Volatility: Volatile}}, name: "random"}) {
		t.Fatal("unrelated volatile default admitted")
	}
}
func TestUUIDGenerationCalendarProvider(t *testing.T) {
	b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
	stamp := b.typ(sqltype.Timestamptz)
	iv := b.typ(sqltype.Interval)
	addition, err := sqltype.BindArithmetic(sqltype.ArithmeticAdd, stamp, iv, stamp)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		start, want time.Time
		m, d        int32
		us          int64
		zone        string
	}{
		{time.Date(2024, 1, 31, 12, 0, 0, 0, time.UTC), time.Date(2024, 2, 29, 12, 0, 0, 0, time.UTC), 1, 0, 0, "UTC"},
		{time.Date(2023, 1, 31, 12, 0, 0, 0, time.UTC), time.Date(2023, 3, 1, 12, 0, 0, 0, time.UTC), 1, 1, 0, "UTC"},
		{time.Date(2005, 4, 2, 19, 0, 0, 0, time.UTC), time.Date(2005, 4, 3, 18, 0, 0, 0, time.UTC), 0, 1, 0, "America/Denver"},
		{time.Date(2005, 4, 2, 19, 0, 0, 0, time.UTC), time.Date(2005, 4, 3, 19, 0, 0, 0, time.UTC), 0, 0, 86400000000, "America/Denver"},
		{time.Date(2024, 1, 31, 12, 0, 0, 0, time.UTC), time.Date(2024, 2, 28, 14, 0, 0, 0, time.UTC), 1, -1, 7200000000, "UTC"},
	} {
		a, err := sqltype.TimestampFromTime(stamp, tc.start)
		if err != nil {
			t.Fatal(err)
		}
		shift, err := sqltype.IntervalValue(iv, tc.m, tc.d, tc.us)
		if err != nil {
			t.Fatal(err)
		}
		zone, err := sqltype.ResolveZone(tc.zone)
		if err != nil {
			t.Fatal(err)
		}
		value, err := addition.Binary(context.Background(), a, shift, sqltype.InputEnvironment{Zone: zone})
		if err != nil {
			t.Fatal(err)
		}
		actual, err := value.TimestampTime()
		if err != nil || !actual.Equal(tc.want) {
			t.Fatal(tc, actual, err)
		}
	}
	a, _ := sqltype.TimestampFromTime(stamp, time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC))
	shift, _ := sqltype.IntervalValue(iv, math.MaxInt32, 0, 0)
	_, err = addition.Binary(context.Background(), a, shift, sqltype.InputEnvironment{})
	nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "22008", Message: "timestamp out of range"})
	// Large valid UUID times pack directly from microseconds, never UnixNano.
	ms, ns, err := uuidShiftTimestamp(33000000000000000-uuidEpochMicroseconds, 123)
	if err != nil {
		t.Fatal(err)
	}
	got := uuidPackV7(ms, ns, [16]byte{}, false)
	if bytes.Equal(got[:6], make([]byte, 6)) || ms != 33000000000000 {
		t.Fatal("far future encoding", got, ms)
	}
}

func TestUUIDGenerationStrictNullPhysicalTrees(t *testing.T) {
	b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
	null := parser.LiteralExpr{Value: parser.Value{Kind: parser.NullValue}}
	typedNull := parser.CastExpr{Child: null, TypeName: parser.TypeName{Name: "interval"}}
	zero := parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "0"}}
	yes := parser.LiteralExpr{Value: parser.Value{Kind: parser.BooleanValue, Boolean: true}}
	for _, child := range []parser.Expr{
		null, typedNull,
		parser.CaseExpr{Arms: []parser.CaseArm{{Condition: yes, Result: typedNull}}, Else: zero},
		parser.ConditionalExpr{Kind: parser.Coalesce, Args: []parser.Expr{typedNull, typedNull}},
	} {
		analyzed, err := b.bind(nullCountRaw("uuidv7", child), expressionPolicy{context: projectionContext})
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := analyzed.(uuidGenerationExpression); !ok || analyzed.Properties().Volatility != Volatile {
			t.Fatal("analysis lost symbolic volatile provider", analyzed)
		}
		for _, preflight := range []bool{false, true} {
			for _, decisive := range []bool{false, true} {
				// Deliberately omit the session: any provider demand would fail.
				evaluation := &EvalContext{Context: context.Background()}
				var planned BoundExpr
				var changed bool
				if preflight {
					planned, changed, err = preflightCoercionsWithPolicy(evaluation, analyzed, decisive)
				} else {
					planned, changed, err = simplifyConditional(evaluation, analyzed, decisive)
				}
				if err != nil || !changed {
					t.Fatal("strict NULL simplification", preflight, decisive, changed, err)
				}
				literal, ok := planned.(literalExpression)
				if !ok || !literal.value.IsNull() || literal.ResultUsage() != analyzed.ResultUsage() || !generationImmutable(planned) || plannedVolatility(planned) != Immutable {
					t.Fatal("strict NULL physical tree", planned)
				}
				value, err := planned.Eval(evaluation)
				if err != nil || !value.IsNull() || value.Type() != b.typ(sqltype.UUID) {
					t.Fatal("typed NULL demanded session", value, err)
				}
			}
		}
		// Planning cannot erase the original definition used by Dump/deparse.
		if _, ok := analyzed.(uuidGenerationExpression); !ok || analyzed.Properties().Volatility != Volatile {
			t.Fatal("analysis mutated")
		}
	}
	for _, raw := range []parser.FunctionExpr{nullCountRaw("uuidv7"), nullCountRaw("uuidv7", zero)} {
		analyzed, err := b.bind(raw, expressionPolicy{context: projectionContext})
		if err != nil {
			t.Fatal(err)
		}
		planned, _, err := preflightCoercionsWithPolicy(&EvalContext{Context: context.Background()}, analyzed, true)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := planned.(uuidGenerationExpression); !ok || plannedVolatility(planned) != Volatile || generationImmutable(planned) {
			t.Fatal("real provider admitted as immutable", planned)
		}
	}
}
