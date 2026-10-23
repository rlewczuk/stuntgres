package sqltype

import (
	"context"
	"errors"
	"math"
	"testing"

	"stuntgres/internal/sqlstate"
)

func castBinaryUnitApply(t *testing.T, value Value, target *Descriptor) Value {
	t.Helper()
	p, err := PlanBinaryCoercion(value.Type(), target)
	if err != nil {
		t.Fatal(err)
	}
	got, err := p.Apply(context.Background(), value)
	if err != nil {
		t.Fatal(value.Type().Name(), target.Name(), err)
	}
	if got.Type() != target {
		t.Fatal("lost target descriptor authority")
	}
	return got
}

func castBinaryUnitError(t *testing.T, err error, message string) {
	t.Helper()
	var state *sqlstate.Error
	if !errors.As(err, &state) || state.Code != "0A000" || state.Message != message {
		t.Fatalf("want 0A000 %q, got %v", message, err)
	}
}

func TestCastDefinitionBinaryDatum(t *testing.T) {
	ctx := context.Background()
	types := testTypes()
	t.Run("scalar32", func(t *testing.T) {
		ids := []ID{Int4, Float4, Date, Oid, Xid, Cid, Regclass, Regtype, Regproc, Regprocedure, Regrole, Regoper, Regoperator, Regcollation, Regnamespace, Regconfig, Regdictionary}
		for _, source := range ids {
			for _, target := range ids {
				v, err := binaryDecode32(types[source], 1)
				if err != nil {
					t.Fatal(err)
				}
				got := castBinaryUnitApply(t, v, types[target])
				back := castBinaryUnitApply(t, got, types[source])
				if !SameDatum(v, back) {
					t.Fatal("word1 changed", source, target, v, back)
				}
			}
		}
		for _, word := range []uint32{0, 1, 0x3f800000, 0x80000000, 0x7f800000, 0xff800000, 0x7fc00001, 0x7f800001, 0xffffffff} {
			x := Value{typ: types[Xid], oid: word}
			for _, id := range ids {
				if id == Date {
					continue
				}
				got := castBinaryUnitApply(t, x, types[id])
				back := castBinaryUnitApply(t, got, types[Xid])
				if back.oid != word {
					t.Fatalf("%s word %08x became %08x", types[id].Name(), word, back.oid)
				}
			}
		}
		one := castBinaryUnitApply(t, Value{typ: types[Xid], oid: 0x3f800000}, types[Float4])
		if one.floating != 1 {
			t.Fatal("numeric conversion substituted for binary word", one)
		}
		for _, tc := range []struct {
			days int64
			word uint32
		}{{0, 0}, {1, 1}, {-1, 0xffffffff}, {dateMinimum, uint32(0xffda97a7)}, {DatePositiveInfinity, 0x7fffffff}, {DateNegativeInfinity, 0x80000000}} {
			date, err := DateValue(types[Date], tc.days)
			if err != nil {
				t.Fatal(err)
			}
			word := castBinaryUnitApply(t, date, types[Xid])
			back := castBinaryUnitApply(t, word, types[Date])
			if word.oid != tc.word || back.integer != tc.days {
				t.Fatal("date epoch/sentinel changed", tc, word, back)
			}
		}
		p, _ := PlanBinaryCoercion(types[Int4], types[Date])
		bad, _ := Integer(types[Int4], -2451546)
		_, err := p.Apply(ctx, bad)
		castBinaryUnitError(t, err, "binary cast value is not representable in embedded mode")
	})
	t.Run("scalar64", func(t *testing.T) {
		ids := []ID{Int8, Float8, Money, Time, Timestamp, Timestamptz, Xid8}
		for _, source := range ids {
			for _, target := range ids {
				v, err := binaryDecode64(types[source], 1)
				if err != nil {
					t.Fatal(err)
				}
				back := castBinaryUnitApply(t, castBinaryUnitApply(t, v, types[target]), types[source])
				if !SameDatum(v, back) {
					t.Fatal("64-bit word1 changed", source, target)
				}
			}
		}
		for _, word := range []uint64{0, 1, 0x3ff0000000000000, 0x8000000000000000, 0x7ff0000000000000, 0xfff0000000000000, 0x7ff0000000000001, 0x7ff8000000000001, math.MaxUint64} {
			v := Value{typ: types[Xid8], integer: int64(word)}
			for _, id := range []ID{Int8, Float8, Money, Xid8} {
				back := castBinaryUnitApply(t, castBinaryUnitApply(t, v, types[id]), types[Xid8])
				if uint64(back.integer) != word {
					t.Fatalf("%s word %016x changed", types[id].Name(), word)
				}
			}
		}
		one := Value{typ: types[Float8], floating: 1}
		money := castBinaryUnitApply(t, one, types[Money])
		if money.integer != 4607182418800017408 {
			t.Fatal("money was scaled instead of reinterpreted", money)
		}
		for _, id := range []ID{Timestamp, Timestamptz} {
			for _, n := range []int64{timestampMinimum, -1, 0, 1, TimestampNegativeInfinity, TimestampPositiveInfinity} {
				v, err := TimestampValue(types[id], n)
				if err != nil {
					t.Fatal(err)
				}
				back := castBinaryUnitApply(t, castBinaryUnitApply(t, v, types[Int8]), types[id])
				if back.integer != n {
					t.Fatal("temporal epoch/sentinel changed", id, n, back)
				}
			}
		}
		for _, tc := range []struct {
			id ID
			n  int64
		}{{Time, -1}, {Time, microsPerDay + 1}, {Timestamp, timestampMinimum - 1}, {Timestamptz, timestampEnd + 1}} {
			p, _ := PlanBinaryCoercion(types[Int8], types[tc.id])
			_, err := p.Apply(ctx, Value{typ: types[Int8], integer: tc.n})
			castBinaryUnitError(t, err, "binary cast value is not representable in embedded mode")
		}
	})
	t.Run("authorityAndBoundary", func(t *testing.T) {
		p, _ := PlanBinaryCoercion(types[Xid], types[Float4])
		got, err := p.Apply(ctx, Null(types[Xid]))
		if err != nil || !got.IsNull() || got.Type() != types[Float4] {
			t.Fatal(got, err)
		}
		foreign := testTypes()
		if _, err := p.Apply(ctx, Null(foreign[Xid])); err == nil {
			t.Fatal("accepted foreign typed NULL")
		}
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		if _, err := p.Apply(cancelled, Value{typ: types[Xid], oid: 1}); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		if _, err := (BinaryCoercionPlan{}).Apply(ctx, Null(types[Xid])); err == nil {
			t.Fatal("accepted empty plan")
		}
		for _, pair := range [][2]ID{{JSONB, Bytea}, {Numeric, Text}, {Point, Interval}, {Point, ACLItem}, {Path, Polygon}, {Bit, Bytea}, {Inet, Text}, {JSONPath, JSON}, {TSVector, TSQuery}, {PgNodeTree, Text}, {Bool, Char}, {Int4, Int8}} {
			_, err := PlanBinaryCoercion(types[pair[0]], types[pair[1]])
			castBinaryUnitError(t, err, "binary cast representation is not supported")
		}
	})
}
