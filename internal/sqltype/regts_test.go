package sqltype

import (
	"context"
	"encoding/binary"
	"errors"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestRegTSScalarCodecs(t *testing.T) {
	types := testTypes()
	ctx := context.Background()
	for _, alias := range []ID{Regconfig, Regdictionary} {
		d := types[alias]
		expectedType, expectedArray := ID(3734), ID(3735)
		if alias == Regdictionary {
			expectedType, expectedArray = 3769, 3770
		}
		if d.ID() != expectedType || d.AssociatedArray() != expectedArray {
			t.Fatal("fixed scalar/companion identities", d.ID(), d.AssociatedArray())
		}
		if !d.Concrete() || !StoredScalarCastType(alias) || d.PostgreSQLTypeLength() != 4 || !d.ContainsRegTS() || !d.ContainsCatalogIdentifier() || !d.OutputSessionDependent() {
			t.Fatal("scalar identity capability", alias)
		}
		array := types[d.AssociatedArray()]
		if !array.Concrete() || !array.Usage().Valid() || array.Element() != d {
			t.Fatal("alias array generation", alias)
		}
		if custom, err := Array(90000, "reg_ts_array", d); err != nil || custom.Element() != d || !custom.Usage().Valid() {
			t.Fatal("alias array codec admission", alias, err)
		}
		if _, err := Null(d).RegTSOID(); err == nil {
			t.Fatal("NULL interpreted as zero")
		}
		if _, err := RegTSValue(types[Int4], 0); err == nil {
			t.Fatal("wrong constructor descriptor admitted")
		}
		order, err := BindOrdering(d.Usage())
		if err != nil {
			t.Fatal(err)
		}
		group, err := BindGrouping(d.Usage())
		if err != nil {
			t.Fatal(err)
		}
		var previous Value
		for i, tc := range []struct {
			input, output string
			id            uint32
		}{{"-", "-", 0}, {"010", "8", 8}, {"2147483648", "2147483648", 2147483648}, {"4294967295", "4294967295", 4294967295}} {
			v, err := d.Input(ctx, tc.input, -1)
			if err != nil {
				t.Fatal(tc.input, err)
			}
			id, err := v.RegTSOID()
			if err != nil || id != tc.id {
				t.Fatal(id, tc.id, err)
			}
			text, err := d.Output(ctx, v)
			if err != nil || text != tc.output {
				t.Fatal(text, tc.output, err)
			}
			literal, err := d.SQLLiteral(ctx, v)
			if err != nil || literal != "'"+tc.output+"'" {
				t.Fatal(literal, err)
			}
			if i > 0 {
				sign, err := order.Compare(ctx, previous, v, InputEnvironment{})
				if err != nil || sign != -1 {
					t.Fatal("unsigned ordering", sign, err)
				}
			}
			if equal, err := group.Equal(ctx, v, v); err != nil || !equal {
				t.Fatal("identity equality", equal, err)
			}
			hash, err := group.Hash(ctx, v)
			if err != nil {
				t.Fatal(err)
			}
			copy, err := RegTSValue(d, tc.id)
			if err != nil {
				t.Fatal(err)
			}
			copyHash, err := group.Hash(ctx, copy)
			if err != nil || copyHash != hash || !SameDatum(v, copy) {
				t.Fatal("equal identity hash", copyHash, hash, err)
			}
			bytes, err := d.AppendBinaryWithEnvironment(ctx, nil, v, OutputEnvironment{})
			if err != nil || len(bytes) != 4 || binary.BigEndian.Uint32(bytes) != tc.id {
				t.Fatal("four-byte output", bytes, err)
			}
			decoded, err := d.BinaryInputWithEnvironment(ctx, binary.BigEndian.AppendUint32(nil, tc.id), -1, InputEnvironment{})
			if err != nil || !SameDatum(decoded, v) {
				t.Fatal("four-byte input", err)
			}
			previous = v
		}
		for _, tc := range []struct{ input, code, primary string }{{"08", "22P02", `invalid input syntax for type oid: "08"`}, {"4294967296", "22003", `value "4294967296" is out of range for type oid`}} {
			_, err := d.Input(ctx, tc.input, -1)
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != tc.code || diagnostic.Message != tc.primary {
				t.Fatal("numeric diagnostic", tc, err)
			}
		}
		if text, err := d.SQLLiteral(ctx, Null(d)); err != nil || text != "NULL" {
			t.Fatal("typed NULL SQL literal", text, err)
		}
	}
}

func TestRegTSCastsDomains(t *testing.T) {
	types := testTypes()
	ctx := context.Background()
	for _, alias := range []ID{Regconfig, Regdictionary} {
		d := types[alias]
		for _, source := range []ID{Oid, Int2, Int4, Int8} {
			for _, coercionContext := range []CoercionContext{Implicit, Explicit} {
				plan, err := PlanCoercion(types[source].Usage(), d.Usage(), coercionContext)
				if err != nil {
					t.Fatal("released input edge", source, alias, err)
				}
				var input Value
				if source == Oid {
					input, _ = OIDValue(types[Oid], 8)
				} else {
					input, _ = Integer(types[source], 8)
				}
				v, err := plan.Apply(ctx, input, InputEnvironment{}, OutputEnvironment{})
				if err != nil {
					t.Fatal(err)
				}
				id, err := v.RegTSOID()
				if err != nil || id != 8 {
					t.Fatal(id, err)
				}
			}
		}
		for _, target := range []ID{Oid, Int4, Int8} {
			for _, coercionContext := range []CoercionContext{Assignment, Explicit} {
				plan, err := PlanCoercion(d.Usage(), types[target].Usage(), coercionContext)
				if err != nil {
					t.Fatal("released output edge", target, err)
				}
				input, _ := RegTSValue(d, 4294967295)
				v, err := plan.Apply(ctx, input, InputEnvironment{}, OutputEnvironment{})
				if err != nil {
					t.Fatal(err)
				}
				if target == Oid {
					id, err := v.OID()
					if err != nil || id != 4294967295 {
						t.Fatal(id, err)
					}
				} else {
					n, err := v.Integer()
					want := int64(4294967295)
					if target == Int4 {
						want = -1
					}
					if err != nil || n != want {
						t.Fatal(n, want, err)
					}
				}
			}
		}
		for _, source := range []ID{Bool, Numeric, Regclass, Regtype, Regproc, Regrole, Regoper, Regoperator, Regconfig, Regdictionary} {
			if source == alias {
				continue
			}
			if _, err := PlanCoercion(types[source].Usage(), d.Usage(), Explicit); err == nil {
				t.Fatal("invented cross-alias edge", source, alias)
			}
		}
		for _, source := range []ID{Text, VarChar} {
			if _, err := PlanCoercion(types[source].Usage(), d.Usage(), Implicit); err == nil {
				t.Fatal("implicit string edge")
			}
			plan, err := PlanCoercion(types[source].Usage(), d.Usage(), Explicit)
			if err != nil {
				t.Fatal(err)
			}
			input, _ := types[source].Input(ctx, "010", -1)
			v, err := plan.Apply(ctx, input, InputEnvironment{}, OutputEnvironment{})
			id, _ := v.RegTSOID()
			if err != nil || id != 8 {
				t.Fatal(id, err)
			}
		}
		plan, err := PlanCoercion(types[Int4].Usage(), d.Usage(), Implicit)
		if err != nil {
			t.Fatal(err)
		}
		negative, _ := Integer(types[Int4], -1)
		v, err := plan.Apply(ctx, negative, InputEnvironment{}, OutputEnvironment{})
		id, _ := v.RegTSOID()
		if err != nil || id != 4294967295 {
			t.Fatal("signed int4 bits", id, err)
		}
		plan, err = PlanCoercion(types[Int8].Usage(), d.Usage(), Implicit)
		if err != nil {
			t.Fatal(err)
		}
		for _, n := range []int64{-1, 4294967296} {
			value, _ := Integer(types[Int8], n)
			_, err := plan.Apply(ctx, value, InputEnvironment{}, OutputEnvironment{})
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != "22003" || diagnostic.Message != "OID out of range" {
				t.Fatal("int8 range", err)
			}
		}
		domain, err := Domain(ID(91000+alias), "reg_ts_domain", "REG_TS_DOMAIN", d.Usage())
		if err != nil {
			t.Fatal(err)
		}
		wrapped, err := domain.Wrap(v)
		if err != nil || !domain.ContainsRegTS() {
			t.Fatal("domain identity", err)
		}
		id, err = wrapped.RegTSOID()
		if err != nil || id != 4294967295 {
			t.Fatal(id, err)
		}
		array, err := Array(95000, "domain_array", domain)
		if err != nil || array.Element() != domain || !array.Usage().Valid() {
			t.Fatal("alias-domain array authority", err)
		}
		values, err := ArrayValue(array, []ArrayDimension{{Lower: 1, Length: 2}}, []Value{wrapped, Null(domain)})
		if err != nil || values.ArrayElement(0).Type() != domain || !SameDatum(values.ArrayElement(0), wrapped) || !values.ArrayElement(1).IsNull() || values.ArrayElement(1).Type() != domain {
			t.Fatal("alias-domain array member ownership", err)
		}
	}
}
