package sqltype

import (
	"context"
	"errors"
	"strings"
	"testing"
)

const partitionTestSeed uint64 = 0x7a5b22367996dcfd

func TestPartitionHashPrimitives(t *testing.T) {
	for _, c := range []struct {
		k    uint32
		want int64
	}{{0, -4403592609991167795}, {1, 5968994663651403477}, {2, -6784076462669891433}, {17, 1629907112932383753}, {42, 7363975540656877951}} {
		if got := int64(partitionHashUint32(c.k, partitionTestSeed)); got != c.want {
			t.Fatalf("%d: %d != %d", c.k, got, c.want)
		}
	}
	for _, c := range []struct {
		value string
		want  int64
	}{{"", -5700645584453517373}, {"abc", 3628778498291917250}, {"\x00\x80\xff", -3010803245567247261}} {
		got, err := partitionHashBytes(context.Background(), c.value, partitionTestSeed)
		if err != nil || int64(got) != c.want {
			t.Fatal(c.value, int64(got), err)
		}
	}
	// Equal integer values share support functions, including signed high-bit folding.
	for _, k := range []int64{-2147483648, -1, 0, 1, 2147483647} {
		if partitionHashInt64(k, partitionTestSeed) != partitionHashUint32(uint32(k), partitionTestSeed) {
			t.Fatal(k)
		}
	}
	h := partitionHashUint32(0, partitionTestSeed) + uint64(0x49a0f4dd15e5a8e3)
	if h != 901916981443598768 || h%3 != 2 {
		t.Fatal("unsigned combined hash", h)
	}
}

func TestPartitionHashTypes(t *testing.T) {
	types := testTypes()
	for _, c := range []struct {
		id    ID
		input string
		want  int64
	}{
		{Char, "\\200", 3045614218496662928}, {Char, "\\377", -5017072347659237694},
		{Xid, "0", -4403592609991167795}, {Xid, "1", 5968994663651403477},
		{Cid, "0", -4403592609991167795}, {Cid, "2", -6784076462669891433},
		{Xid8, "9223372036854775808", -6050265599104649060}, {Xid8, "18446744073709551615", -5017072347659237694},
		{Numeric, "0", 8816678312871386364}, {Numeric, "-0.000", 8816678312871386364}, {Numeric, "NaN", 8816678312871386365}, {Numeric, "Infinity", 8816678312871386365}, {Numeric, "-Infinity", 8816678312871386365},
		{Numeric, "1", -3231555093127941538}, {Numeric, "-1", -3231555093127941538}, {Numeric, "1.00", -3231555093127941538}, {Numeric, "0.1", -7869483101049934924}, {Numeric, "10000", -3231555093127941537}, {Numeric, "12345.6789", 6445526082213146339},
		{Timetz, "00:00+00", 0}, {Timetz, "12:00+02", 303720168613422178}, {Timetz, "10:00+00", 896213977459802112},
		{Name, "abc", 3628778498291917250}, {Name, "", -5700645584453517373},
		{Float8, "NaN", -6475869854796787835}, {Float8, "0", 8816678312871386365}, {Float8, "-0", 8816678312871386365}, {Float8, "1", 3988910132942752730}, {Float8, "Infinity", -4970436883654098436}, {Float8, "-Infinity", 7927086137625499499},
		{Date, "-Infinity", 4938542303000433043}, {Date, "Infinity", -6050265599104649060}, {Timestamp, "-Infinity", -6050265599104649060}, {Timestamp, "Infinity", 4938542303000433043},
		{Interval, "-Infinity", 6982671906058149066}, {Interval, "Infinity", 5746409742142248859},
		{UUID, "00000000-0000-0000-0000-000000000000", -5281082146396728638},
		{JSONB, "[]", 8816678312871386365}, {JSONB, "{}", 8816678312871386365}, {JSONB, "null", -406693721835905794}, {JSONB, "true", -3254377375350855979}, {JSONB, "false", 4819779429011091661},
		{JSONB, "1", 5991816941579350622}, {JSONB, "-1", 5991816941579350622}, {JSONB, "[1]", 5991816941579350622}, {JSONB, "[[]]", 0}, {JSONB, "[{}]", 6917529029251694592}, {JSONB, `{"a":1}`, 1640352816913874601}, {JSONB, `{"aa":1,"b":2}`, -5436459084680285896}, {JSONB, "[1,2]", -2216640663082688800}, {JSONB, "[2,1]", 6756016062372759270},
	} {
		typ := types[c.id]
		v, err := typ.Input(context.Background(), c.input, -1)
		if err != nil {
			t.Fatal(c, err)
		}
		class, ok := DefaultHashIndexClass(typ)
		if !ok {
			t.Fatal("missing class", c)
		}
		provider, err := BindPartitionHash(typ.Usage(), class)
		if err != nil {
			t.Fatal(c, err)
		}
		got, err := provider.Hash(context.Background(), v, partitionTestSeed)
		if err != nil || int64(got) != c.want {
			t.Fatalf("%d %q: %d != %d (%v)", c.id, c.input, int64(got), c.want, err)
		}
	}
	// Class normalization must preserve identity while applying class semantics.
	text := types[Text]
	class, _ := LookupHashIndexClass("bpchar_ops")
	p, err := BindPartitionHash(text.Usage(), class)
	if err != nil {
		t.Fatal(err)
	}
	got, err := p.Hash(context.Background(), String(text, "abc   "), partitionTestSeed)
	if err != nil || int64(got) != 3628778498291917250 {
		t.Fatal(got, err)
	}
	enum, err := EnumWithMembers(45000, "hp_enum", "hp_enum", []EnumMember{{ID: 1, Label: "one", SortOrder: 9}})
	if err != nil {
		t.Fatal(err)
	}
	c, _ := DefaultHashIndexClass(enum)
	provider, err := BindPartitionHash(enum.Usage(), c)
	if err != nil {
		t.Fatal(err)
	}
	v, err := enum.Input(context.Background(), "one", -1)
	if err != nil {
		t.Fatal(err)
	}
	h, err := provider.Hash(context.Background(), v, partitionTestSeed)
	if err != nil || int64(h) != 5968994663651403477 {
		t.Fatal(h, err)
	}
}

func TestPartitionHashCancellation(t *testing.T) {
	types := testTypes()
	for _, c := range []struct {
		id    ID
		input string
	}{{Text, strings.Repeat("x", 16384)}, {Numeric, strings.Repeat("1234", 4096)}, {JSONB, "[" + strings.Repeat("1,", 4095) + "1]"}} {
		typ := types[c.id]
		value, err := typ.Input(context.Background(), c.input, -1)
		if err != nil {
			t.Fatal(err)
		}
		class, _ := DefaultHashIndexClass(typ)
		provider, err := BindPartitionHash(typ.Usage(), class)
		if err != nil {
			t.Fatal(err)
		}
		ctx := &groupingCancelContext{Context: context.Background(), at: 3}
		if _, err := provider.Hash(ctx, value, partitionTestSeed); !errors.Is(err, context.Canceled) || ctx.calls != 3 {
			t.Fatal(c.id, err, ctx.calls)
		}
	}
}
