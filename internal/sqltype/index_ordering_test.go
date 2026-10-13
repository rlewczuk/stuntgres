package sqltype

import (
	"context"
	"errors"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestIndexClausesProviders(t *testing.T) {
	types := testTypes()
	for _, tc := range []struct {
		name string
		id   ID
	}{
		{"bool_ops", Bool}, {"bytea_ops", Bytea}, {"char_ops", Char}, {"name_ops", Name},
		{"int2_ops", Int2}, {"int4_ops", Int4}, {"int8_ops", Int8}, {"text_ops", Text}, {"varchar_ops", VarChar}, {"bpchar_ops", BPChar}, {"oid_ops", Oid},
		{"float4_ops", Float4}, {"float8_ops", Float8}, {"date_ops", Date}, {"time_ops", Time}, {"timestamp_ops", Timestamp}, {"timestamptz_ops", Timestamptz}, {"timetz_ops", Timetz}, {"interval_ops", Interval}, {"numeric_ops", Numeric}, {"uuid_ops", UUID},
		{"text_pattern_ops", Text}, {"varchar_pattern_ops", VarChar}, {"bpchar_pattern_ops", BPChar},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, ok := LookupIndexClass(tc.name)
			if !ok || c.Name != tc.name || c.Family == "" || !c.Accepts(types[tc.id]) {
				t.Fatal(c, ok)
			}
			// Binding, not non-NULL data, establishes a provider on empty/all-NULL roots.
			if _, err := BindIndexOrdering(types[tc.id].Usage(), c); err != nil {
				t.Fatal(err)
			}
			if tc.id == Text || tc.id == VarChar || tc.id == BPChar {
				for _, id := range []uint32{100, 950, 951, 811, 6411} {
					u := types[tc.id].Usage()
					u.Collation = CollationRef{ID: id, Derivation: Default}
					if _, err := BindIndexOrdering(u, c); err != nil {
						t.Fatal(id, err)
					}
				}
			}
		})
	}
	for _, tc := range []struct {
		id   ID
		name string
	}{{VarChar, "text_ops"}, {Regclass, "oid_ops"}, {Int4, "int4_ops"}, {NameArray, "array_ops"}} {
		c, ok := DefaultIndexClass(types[tc.id])
		if !ok || c.Name != tc.name {
			t.Fatal(tc, c, ok)
		}
	}
	for _, tc := range []struct {
		id     ID
		name   string
		accept bool
	}{
		{Text, "varchar_ops", true}, {VarChar, "text_ops", true}, {Text, "bpchar_ops", true}, {VarChar, "bpchar_pattern_ops", true},
		{Int4, "oid_ops", true}, {Regclass, "oid_ops", true}, {Oid, "int4_ops", false}, {Int4, "int8_ops", false}, {Name, "text_ops", false}, {BPChar, "text_ops", false},
		{Int4, "array_ops", false}, {Text, "record_ops", false},
	} {
		c, _ := LookupIndexClass(tc.name)
		if c.Accepts(types[tc.id]) != tc.accept {
			t.Fatal(tc)
		}
		_, err := BindIndexOrdering(types[tc.id].Usage(), c)
		if tc.accept {
			if err != nil {
				t.Fatal(tc, err)
			}
		} else {
			var d *sqlstate.Error
			if !errors.As(err, &d) || d.Code != "42804" {
				t.Fatal(tc, err)
			}
		}
	}
	c, _ := LookupIndexClass("array_ops")
	_, err := BindIndexOrdering(types[NameArray].Usage(), c)
	var d *sqlstate.Error
	if !errors.As(err, &d) || d.Code != "0A000" || d.Message != "index operator class is unsupported" {
		t.Fatal(err)
	}
	if _, ok := LookupIndexClass("missing_ops"); ok {
		t.Fatal("invented class")
	}
}

func TestIndexClausesProviderSemantics(t *testing.T) {
	types := testTypes()
	ctx := context.Background()
	for _, tc := range []struct {
		id                 ID
		class, left, right string
		want               int
	}{
		{Int4, "oid_ops", "1", "-1", -1}, {Int4, "int4_ops", "1", "-1", 1},
		{Text, "bpchar_ops", "a", "a ", 0}, {VarChar, "bpchar_pattern_ops", "a ", "a", 0},
		{Text, "text_ops", "a", "a ", -1}, {Text, "varchar_pattern_ops", "a", "a ", -1},
		{Regclass, "oid_ops", "1", "4294967295", -1},
	} {
		t.Run(tc.class+types[tc.id].Name(), func(t *testing.T) {
			for _, domain := range []bool{false, true} {
				typ := types[tc.id]
				if domain {
					var err error
					typ, err = Domain(20000+tc.id, "d", "public.d", typ.Usage())
					if err != nil {
						t.Fatal(err)
					}
				}
				a, err := typ.Input(ctx, tc.left, -1)
				if err != nil {
					t.Fatal(err)
				}
				b, err := typ.Input(ctx, tc.right, -1)
				if err != nil {
					t.Fatal(err)
				}
				oldA, oldB := a, b
				c, _ := LookupIndexClass(tc.class)
				p, err := BindIndexOrdering(typ.Usage(), c)
				if err != nil {
					t.Fatal(err)
				}
				got, err := p.Compare(ctx, a, b, InputEnvironment{})
				if err != nil || got != tc.want {
					t.Fatal(got, tc.want, err)
				}
				if a != oldA || b != oldB {
					t.Fatal("provider mutated storage")
				}
				canceled, cancel := context.WithCancel(ctx)
				cancel()
				if _, err := p.Compare(canceled, a, b, InputEnvironment{}); !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestIndexClausesSystemIdentity(t *testing.T) {
	for _, tc := range []struct {
		attribute, name string
		id              ID
		defaultClass    string
	}{
		{"ctid", "tid", 27, "tid_ops"}, {"xmin", "xid", 28, ""}, {"xmax", "xid", 28, ""},
		{"cmin", "cid", 29, ""}, {"cmax", "cid", 29, ""}, {"tableoid", "oid", Oid, "oid_ops"},
	} {
		id, name := SystemAttributeType(tc.attribute)
		if id != tc.id || name != tc.name {
			t.Fatal(tc, id, name)
		}
		class, exists := DefaultIndexClassID(id)
		if exists != (tc.defaultClass != "") || class.Name != tc.defaultClass {
			t.Fatal(tc, class, exists)
		}
		if exists && !class.AcceptsID(id) {
			t.Fatal("default incompatible with diagnostic identity", tc)
		}
	}
	class, _ := LookupIndexClass("int4_ops")
	if class.AcceptsID(27) {
		t.Fatal("integer class accepted tid")
	}
	// TID retains diagnostic-only identity; public identifiers do not gain ordering.
	for _, d := range Builtins() {
		if d.ID() == 27 {
			if d.Concrete() || d.Usage().Valid() {
				t.Fatal("system diagnostic added a storage type", d)
			}
			if _, err := BindOrdering(d.Usage()); err == nil {
				t.Fatal("system diagnostic added an ordering provider", d)
			}
			class, _ := LookupIndexClass("tid_ops")
			if class.Accepts(d) {
				t.Fatal("system diagnostic admitted an executable index key", d)
			}
		}
		if d.ID() == Xid || d.ID() == Cid {
			if !d.Concrete() || !d.Usage().Valid() {
				t.Fatal("identifier lost scalar admission", d)
			}
			if _, exists := DefaultIndexClassID(d.ID()); exists {
				t.Fatal("identifier acquired a btree storage operator class", d)
			}
			_, err := BindOrdering(d.Usage())
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != "42883" || diagnostic.Message != "could not identify an ordering operator for type "+d.Name() {
				t.Fatal("identifier lost SQL ordering boundary", d, err)
			}
		}
	}
}
