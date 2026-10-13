package sqltype

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

// Independently specified release tuples, not copied from enumeration at runtime.
func castCatalogExpected() []CastMetadata {
	return []CastMetadata{
		{20000, 21, 23, 313, 'i', 'f'}, {20001, 21, 20, 754, 'i', 'f'}, {20002, 21, 700, 236, 'i', 'f'}, {20003, 21, 701, 235, 'i', 'f'}, {20004, 21, 1700, 1782, 'i', 'f'},
		{20005, 23, 21, 314, 'a', 'f'}, {20006, 23, 20, 481, 'i', 'f'}, {20007, 23, 700, 318, 'i', 'f'}, {20008, 23, 701, 316, 'i', 'f'}, {20009, 23, 1700, 1740, 'i', 'f'},
		{20010, 20, 21, 714, 'a', 'f'}, {20011, 20, 23, 480, 'a', 'f'}, {20012, 20, 700, 652, 'i', 'f'}, {20013, 20, 701, 482, 'i', 'f'}, {20014, 20, 1700, 1781, 'i', 'f'},
		{20015, 700, 21, 238, 'a', 'f'}, {20016, 700, 23, 319, 'a', 'f'}, {20017, 700, 20, 653, 'a', 'f'}, {20018, 700, 701, 311, 'i', 'f'}, {20019, 700, 1700, 1742, 'a', 'f'},
		{20020, 701, 21, 237, 'a', 'f'}, {20021, 701, 23, 317, 'a', 'f'}, {20022, 701, 20, 483, 'a', 'f'}, {20023, 701, 700, 312, 'a', 'f'}, {20024, 701, 1700, 1743, 'a', 'f'},
		{20025, 1700, 21, 1783, 'a', 'f'}, {20026, 1700, 23, 1744, 'a', 'f'}, {20027, 1700, 20, 1779, 'a', 'f'}, {20028, 1700, 700, 1745, 'i', 'f'}, {20029, 1700, 701, 1746, 'i', 'f'},
		{20030, 21, 26, 313, 'i', 'f'}, {20031, 23, 26, 0, 'i', 'b'}, {20032, 20, 26, 1287, 'i', 'f'}, {20033, 26, 23, 0, 'a', 'b'}, {20034, 26, 20, 1288, 'a', 'f'},
		{20035, 1082, 1114, 2024, 'i', 'f'}, {20036, 1082, 1184, 1174, 'i', 'f'}, {20037, 1083, 1266, 2047, 'i', 'f'},
		{20038, 1114, 1082, 2029, 'a', 'f'}, {20039, 1114, 1083, 1316, 'a', 'f'}, {20040, 1114, 1184, 2028, 'i', 'f'},
		{20041, 1184, 1082, 1178, 'a', 'f'}, {20042, 1184, 1083, 2019, 'a', 'f'}, {20043, 1184, 1114, 2027, 'a', 'f'}, {20044, 1184, 1266, 1388, 'a', 'f'}, {20045, 1266, 1083, 2046, 'a', 'f'},
		{20046, 25, 1042, 0, 'i', 'b'}, {20047, 25, 1043, 0, 'i', 'b'}, {20048, 1042, 25, 401, 'i', 'f'}, {20049, 1042, 1043, 401, 'i', 'f'}, {20050, 1043, 25, 0, 'i', 'b'}, {20051, 1043, 1042, 0, 'i', 'b'},
		{20052, 23, 16, 2557, 'e', 'f'}, {20053, 16, 23, 2558, 'e', 'f'}, {20054, 16, 25, 2971, 'a', 'f'},
		{20055, 1042, 1042, 668, 'i', 'f'}, {20056, 1043, 1043, 669, 'i', 'f'}, {20057, 1083, 1083, 1968, 'i', 'f'}, {20058, 1114, 1114, 1961, 'i', 'f'},
		{20059, 1184, 1184, 1967, 'i', 'f'}, {20060, 1186, 1186, 1200, 'i', 'f'}, {20061, 1266, 1266, 1969, 'i', 'f'}, {20062, 1700, 1700, 1703, 'i', 'f'},
		{20063, 114, 3802, 0, 'a', 'i'}, {20064, 3802, 114, 0, 'a', 'i'},
		{20065, 790, 1700, 3823, 'a', 'f'}, {20066, 1700, 790, 3824, 'a', 'f'},
		{20067, 23, 790, 3811, 'a', 'f'}, {20068, 20, 790, 3812, 'a', 'f'},
		{20069, 5069, 28, 5071, 'e', 'f'},
	}
}

func TestCastCatalogMetadata(t *testing.T) {
	want := castCatalogExpected()
	if CastMetadataCount() != 70 || len(want) != 70 {
		t.Fatal("cast count", CastMetadataCount(), len(want))
	}
	ids, pairs := map[uint32]bool{}, map[[2]ID]bool{}
	for i, expected := range want {
		got, ok := CastMetadataAt(i)
		if !ok || got != expected {
			t.Fatal("exact cast", i, got, expected, ok)
		}
		pair := [2]ID{got.Source, got.Target}
		if got.OID == 0 || ids[got.OID] || pairs[pair] {
			t.Fatal("duplicate/invalid cast", got)
		}
		if (got.Method == 'f') != (got.Function != 0) {
			t.Fatal("function/method", got)
		}
		ids[got.OID], pairs[pair] = true, true
		got.Function = 0
		got.OID = 0
		if again, _ := CastMetadataAt(i); again != expected {
			t.Fatal("mutable metadata escaped", i, again)
		}
	}
	for _, i := range []int{-1, 70, 71} {
		if got, ok := CastMetadataAt(i); ok || got != (CastMetadata{}) {
			t.Fatal("out of range", i, got, ok)
		}
	}
	for _, pair := range [][2]ID{
		{Int4, Text}, {Text, Int4}, {Float4, Text}, {Float8, Text}, {Oid, Text}, {Text, Text}, {Int4, Int4}, {Bool, Bool},
		{Name, Text}, {Name, BPChar}, {Name, VarChar}, {Text, Name}, {BPChar, Name}, {VarChar, Name},
		{Regclass, Oid}, {Oid, Regclass}, {Text, Regclass}, {Int4, Regclass},
		{Bit, VarBit}, {VarBit, Bit}, {Bit, Bit}, {VarBit, VarBit}, {PgNodeTree, Text}, {Char, Text},
		{JSONB, Int4}, {JSONB, Bool}, {JSONB, Numeric}, {Time, Interval}, {Interval, Time},
	} {
		if pairs[pair] {
			t.Fatal("overclaimed cast", pair)
		}
	}
}

func castCatalogText(id ID) string {
	switch id {
	case Bool:
		return "true"
	case Text, BPChar, VarChar:
		return "ab"
	case Date:
		return "2000-01-01"
	case Time:
		return "00:00:00"
	case Timetz:
		return "00:00:00+00"
	case Timestamp:
		return "2000-01-01 00:00:00"
	case Timestamptz:
		return "2000-01-01 00:00:00+00"
	case Interval:
		return "00:00:00"
	case JSON:
		return "{\"n\":1}"
	case JSONB:
		return "{\"n\": 1}"
	default:
		return "7"
	}
}

func TestCastCatalogCompleteContextClosure(t *testing.T) {
	types, foreign := testTypes(), testTypes()
	ctx := context.Background()
	zone, err := ResolveZone("UTC")
	if err != nil {
		t.Fatal(err)
	}
	input := InputEnvironment{Zone: zone, TransactionStart: time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)}
	output := OutputEnvironment{Zone: zone}
	for _, row := range castCatalogExpected() {
		minimum := Implicit
		if row.Context == 'a' {
			minimum = Assignment
		} else if row.Context == 'e' {
			minimum = Explicit
		}
		for _, mode := range []CoercionContext{Implicit, Assignment, Explicit} {
			plan, err := PlanCoercion(types[row.Source].Usage(), types[row.Target].Usage(), mode)
			if mode < minimum {
				if err == nil {
					t.Fatal("lesser context admitted", row, mode)
				}
				continue
			}
			if err != nil {
				t.Fatal("claimed closure absent", row, mode, err)
			}
			value, err := types[row.Source].InputWithEnvironment(ctx, castCatalogText(row.Source), -1, input)
			if err != nil {
				t.Fatal(row, err)
			}
			got, err := plan.Apply(ctx, value, input, output)
			if err != nil || got.Type() != types[row.Target] || got.IsNull() {
				t.Fatal("apply", row, mode, got, err)
			}
			text, err := types[row.Target].OutputWithEnvironment(ctx, got, output)
			want := castCatalogText(row.Target)
			if row.Target == Money {
				want = "$7.00"
			}
			if row.Source == Money && row.Target == Numeric {
				want = "7.00"
			}
			if row.Source == Bool && row.Target == Int4 {
				want = "1"
			}
			if row.Source == Bool && row.Target == Text {
				want = "true"
			}
			if row.Source == Int4 && row.Target == Bool {
				want = "t"
			}
			if row.Source == JSONB && row.Target == JSON {
				want = "{\"n\": 1}"
			}
			if err != nil || text != want {
				t.Fatal("value", row, mode, text, want, err)
			}
			null, err := plan.Apply(ctx, Null(types[row.Source]), input, output)
			if err != nil || !null.IsNull() || null.Type() != types[row.Target] {
				t.Fatal("typed NULL", row, mode, null, err)
			}
			if _, err := plan.Apply(ctx, Null(foreign[row.Source]), input, output); err == nil {
				t.Fatal("foreign source value admitted", row)
			}
			canceled, cancel := context.WithCancel(ctx)
			cancel()
			if _, err := plan.Apply(canceled, value, input, output); !errors.Is(err, context.Canceled) {
				t.Fatal("cancellation", row, err)
			}
		}
	}
}

func TestCastCatalogTypmodSizing(t *testing.T) {
	ctx, types := context.Background(), testTypes()
	zone, err := ResolveZone("UTC")
	if err != nil {
		t.Fatal(err)
	}
	input, output := InputEnvironment{Zone: zone}, OutputEnvironment{Zone: zone}
	for _, tc := range []struct {
		id          ID
		mods        []int32
		input, want string
	}{
		{BPChar, []int32{3}, "ab", "ab "}, {VarChar, []int32{3}, "ab", "ab"},
		{Time, []int32{2}, "12:34:56.125", "12:34:56.13"},
		{Timestamp, []int32{2}, "2000-01-01 12:34:56.125", "2000-01-01 12:34:56.13"},
		{Timestamptz, []int32{2}, "2000-01-01 12:34:56.125+00", "2000-01-01 12:34:56.13+00"},
		{Interval, []int32{IntervalFullRange, 2}, "00:00:01.125", "00:00:01.13"},
		{Timetz, []int32{2}, "12:34:56.125+00", "12:34:56.13+00"},
		{Numeric, []int32{5, 2}, "7.125", "7.13"},
	} {
		target, warning, err := types[tc.id].ResolveModifiers(tc.mods...)
		if err != nil || warning != nil {
			t.Fatal(tc, err, warning)
		}
		value, err := types[tc.id].InputWithEnvironment(ctx, tc.input, -1, input)
		if err != nil {
			t.Fatal(tc, err)
		}
		for _, mode := range []CoercionContext{Implicit, Assignment, Explicit} {
			plan, err := PlanCoercion(types[tc.id].Usage(), target, mode)
			if err != nil {
				t.Fatal(tc, mode, err)
			}
			got, err := plan.Apply(ctx, value, input, output)
			if err != nil {
				t.Fatal(tc, mode, err)
			}
			text, err := types[tc.id].OutputWithEnvironment(ctx, got, output)
			if err != nil || text != tc.want || got.Type() != target.Type {
				t.Fatal(tc, mode, text, err)
			}
		}
	}
	// Binary catalog method must not replace signed OID execution semantics.
	minus, _ := Integer(types[Int4], -1)
	toOID, _ := PlanCoercion(types[Int4].Usage(), types[Oid].Usage(), Implicit)
	high, err := toOID.Apply(ctx, minus, input, output)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := high.OID(); n != 4294967295 || err != nil {
		t.Fatal(n, err)
	}
	toInt, _ := PlanCoercion(types[Oid].Usage(), types[Int4].Usage(), Assignment)
	back, err := toInt.Apply(ctx, high, input, output)
	if err != nil || !reflect.DeepEqual(back, minus) {
		t.Fatal(back, minus, err)
	}
}
