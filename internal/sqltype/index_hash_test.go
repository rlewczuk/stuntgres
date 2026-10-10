package sqltype

import (
	"context"
	"errors"
	"math"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestHashIndexClassProviders(t *testing.T) {
	types := testTypes()
	// REL_18_0 pg_opclass.dat; these numeric identities were independently
	// observed on the pinned PG18.0 server before native implementation.
	for _, tc := range []struct {
		name string
		id   ID
		oid  uint32
	}{
		{"array_ops", NameArray, 10001}, {"bool_ops", Bool, 10048}, {"bpchar_ops", BPChar, 10005}, {"bpchar_pattern_ops", BPChar, 10058},
		{"bytea_ops", Bytea, 10049}, {"char_ops", Char, 10008}, {"cid_ops", 29, 10054}, {"date_ops", Date, 10011}, {"enum_ops", 0, 10070},
		{"float4_ops", Float4, 10013}, {"float8_ops", Float8, 10014}, {"int2_ops", Int2, 10019}, {"int4_ops", Int4, 10020}, {"int8_ops", Int8, 10021},
		{"interval_ops", Interval, 10023}, {"name_ops", Name, 10029}, {"numeric_ops", Numeric, 10030}, {"oid_ops", Oid, 10031}, {"text_ops", Text, 10037},
		{"text_pattern_ops", Text, 10056}, {"tid_ops", 27, 10055}, {"time_ops", Time, 10039}, {"timestamp_ops", Timestamp, 10046},
		{"timestamptz_ops", Timestamptz, 10040}, {"timetz_ops", Timetz, 10042}, {"uuid_ops", UUID, 10066}, {"varchar_ops", VarChar, 10045},
		{"varchar_pattern_ops", VarChar, 10057}, {"xid_ops", 28, 10051},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, ok := LookupHashIndexClass(tc.name)
			if !ok || !c.IsHash() || c.OID() != tc.oid || c.Family == "" {
				t.Fatal(tc, c, ok)
			}
			typ := types[tc.id]
			if typ == nil {
				return
			} // catalog-only system/enum identities
			if !c.Accepts(typ) {
				t.Fatal("released input rejected", tc)
			}
			_, err := BindIndexHash(typ.Usage(), c)
			if tc.id == NameArray {
				var d *sqlstate.Error
				if !errors.As(err, &d) || d.Code != "0A000" || d.Message != "index operator class is unsupported" {
					t.Fatal(err)
				}
			} else if err != nil {
				t.Fatal("eager provider binding", err)
			}
			if _, err := BindIndexOrdering(typ.Usage(), c); err == nil {
				t.Fatal("hash class silently ordered")
			}
		})
	}
	for _, tc := range []struct {
		id   ID
		name string
	}{{Bool, "bool_ops"}, {Int4, "int4_ops"}, {VarChar, "text_ops"}, {Regclass, "oid_ops"}, {NameArray, "array_ops"}} {
		c, ok := DefaultHashIndexClass(types[tc.id])
		if !ok || c.Name != tc.name || !c.IsHash() {
			t.Fatal(tc, c, ok)
		}
	}
	if _, ok := DefaultHashIndexClass(types[JSON]); ok {
		t.Fatal("JSON default invented")
	}
	if _, ok := LookupHashIndexClass("missing_ops"); ok {
		t.Fatal("missing class invented")
	}
	for _, tc := range []struct {
		id     ID
		name   string
		accept bool
	}{
		{Text, "varchar_ops", true}, {VarChar, "text_ops", true}, {Text, "bpchar_ops", true}, {VarChar, "bpchar_pattern_ops", true},
		{Int4, "oid_ops", true}, {Regclass, "oid_ops", true}, {Oid, "int4_ops", false}, {Int4, "int8_ops", false}, {Name, "text_ops", false}, {BPChar, "text_ops", false},
	} {
		c, _ := LookupHashIndexClass(tc.name)
		_, err := BindIndexHash(types[tc.id].Usage(), c)
		if c.Accepts(types[tc.id]) != tc.accept || (err == nil) != tc.accept {
			t.Fatal(tc, err)
		}
	}
}

// Value equality alone cannot witness immutability: NaN differs from itself.
// Compare its exact floating representation and every remaining inline field/
// immutable payload identity without changing either original datum.
func sameHashStoredDatum(a, b Value) bool {
	sameFloat := math.Float64bits(a.floating) == math.Float64bits(b.floating)
	a.floating, b.floating = 0, 0
	return sameFloat && a == b
}

func TestHashIndexEqualityNormalization(t *testing.T) {
	types := testTypes()
	ctx := context.Background()
	for _, tc := range []struct {
		id          ID
		class, a, b string
		equal       bool
	}{
		{Bool, "bool_ops", "true", "true", true}, {Bytea, "bytea_ops", `\x0080ff`, `\x0080ff`, true}, {Char, "char_ops", "a", "a", true},
		{Name, "name_ops", "alpha", "alpha", true}, {Int2, "int2_ops", "1", "1", true}, {Int4, "int4_ops", "-1", "-1", true}, {Int8, "int8_ops", "1", "1", true},
		{Text, "text_ops", "a", "a ", false}, {Text, "varchar_ops", "a", "a", true}, {Text, "bpchar_ops", "a", "a ", true},
		{VarChar, "bpchar_pattern_ops", "a ", "a", true}, {BPChar, "bpchar_ops", "a", "a ", true}, {Oid, "oid_ops", "1", "1", true},
		{Int4, "oid_ops", "-1", "-1", true}, {Regclass, "oid_ops", "4294967295", "4294967295", true},
		{Float4, "float4_ops", "0", "-0", true}, {Float8, "float8_ops", "0", "-0", true}, {Float4, "float4_ops", "NaN", "NaN", true}, {Float8, "float8_ops", "NaN", "NaN", true},
		{Numeric, "numeric_ops", "1", "1.00", true}, {Numeric, "numeric_ops", "0", "-0.00", true}, {Numeric, "numeric_ops", "NaN", "NaN", true},
		{Date, "date_ops", "2020-02-29", "2020-02-29", true}, {Time, "time_ops", "12:00", "12:00:00", true},
		{Timestamp, "timestamp_ops", "2020-02-29 12:00", "2020-02-29 12:00:00", true},
		{Timestamptz, "timestamptz_ops", "2020-02-29 12:00+00", "2020-02-29 13:00+01", true},
		{Timetz, "timetz_ops", "12:00+00", "12:00+00", true}, {Interval, "interval_ops", "1 mon", "30 days", true},
		{UUID, "uuid_ops", "12345678-1234-1234-1234-123456789abc", "12345678-1234-1234-1234-123456789abc", true},
	} {
		t.Run(tc.class+types[tc.id].Name()+tc.a, func(t *testing.T) {
			for _, domain := range []bool{false, true} {
				// Domain explicitly excludes internal CHAR; scalar provider
				// admission does not expand that descriptor boundary.
				if domain && tc.id == Char {
					if _, err := Domain(40000+tc.id, "d", "public.d", types[tc.id].Usage()); err == nil {
						t.Fatal("internal CHAR domain unexpectedly admitted")
					}
					continue
				}
				typ := types[tc.id]
				if domain {
					var err error
					typ, err = Domain(40000+tc.id, "d", "public.d", typ.Usage())
					if err != nil {
						t.Fatal(err)
					}
				}
				a, err := typ.Input(ctx, tc.a, -1)
				if err != nil {
					t.Fatal(err)
				}
				b, err := typ.Input(ctx, tc.b, -1)
				if err != nil {
					t.Fatal(err)
				}
				beforeA, beforeB := a, b
				c, _ := LookupHashIndexClass(tc.class)
				provider, err := BindIndexHash(typ.Usage(), c)
				if err != nil {
					t.Fatal(err)
				}
				equal, err := provider.Equal(ctx, a, b)
				if err != nil || equal != tc.equal {
					t.Fatal(equal, err)
				}
				ha, err := provider.Hash(ctx, a)
				if err != nil {
					t.Fatal(err)
				}
				hb, err := provider.Hash(ctx, b)
				if err != nil {
					t.Fatal(err)
				}
				if equal && ha != hb {
					t.Fatal("equal keys have different buckets", ha, hb)
				}
				if !sameHashStoredDatum(a, beforeA) || !sameHashStoredDatum(b, beforeB) {
					t.Fatal("provider changed stored datum")
				}
				canceled, cancel := context.WithCancel(ctx)
				cancel()
				if _, err := provider.Hash(canceled, a); !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
				if _, err := provider.Equal(canceled, a, b); !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			}
		})
	}
	for _, id := range []uint32{100, 950, 951, 811, 6411} {
		u := types[Text].Usage()
		u.Collation = CollationRef{ID: id, Derivation: Default}
		c, _ := LookupHashIndexClass("text_pattern_ops")
		p, err := BindIndexHash(u, c)
		if err != nil {
			t.Fatal(id, err)
		}
		a := String(types[Text], "é")
		b := String(types[Text], "é")
		ha, err := p.Hash(ctx, a)
		if err != nil {
			t.Fatal(err)
		}
		hb, err := p.Hash(ctx, b)
		if err != nil || ha != hb {
			t.Fatal(id, ha, hb, err)
		}
	}
}

func TestHashIndexEnumDomainIdentity(t *testing.T) {
	ctx := context.Background()
	enum, err := Enum(45000, "mood", "public.mood", []string{"z", "a"})
	if err != nil {
		t.Fatal(err)
	}
	domain, err := Domain(45001, "d", "public.d", enum.Usage())
	if err != nil {
		t.Fatal(err)
	}
	for _, typ := range []*Descriptor{enum, domain} {
		c, ok := DefaultHashIndexClass(typ)
		if !ok || c.OID() != 10070 {
			t.Fatal(c, ok)
		}
		p, err := BindIndexHash(typ.Usage(), c)
		if err != nil {
			t.Fatal(err)
		}
		a, err := typ.Input(ctx, "z", -1)
		if err != nil {
			t.Fatal(err)
		}
		b, err := typ.Input(ctx, "z", -1)
		if err != nil {
			t.Fatal(err)
		}
		equal, err := p.Equal(ctx, a, b)
		if err != nil || !equal {
			t.Fatal(equal, err)
		}
		ha, err := p.Hash(ctx, a)
		if err != nil {
			t.Fatal(err)
		}
		hb, err := p.Hash(ctx, b)
		if err != nil || ha != hb {
			t.Fatal(ha, hb, err)
		}
		foreign, err := Enum(enum.ID(), enum.Name(), enum.SQLName(), []string{"z", "a"})
		if err != nil {
			t.Fatal(err)
		}
		wrong, err := foreign.Input(ctx, "z", -1)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := p.Hash(ctx, wrong); err == nil {
			t.Fatal("foreign generation accepted")
		}
	}
}
