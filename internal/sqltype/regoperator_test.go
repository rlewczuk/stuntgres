package sqltype

import (
	"context"
	"encoding/binary"
	"errors"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestRegOperatorScalarIdentity(t *testing.T) {
	types := testTypes()
	ctx := context.Background()
	for _, alias := range []ID{Regoper, Regoperator} {
		d := types[alias]
		if !d.Concrete() || !StoredScalarCastType(alias) || d.PostgreSQLTypeLength() != 4 {
			t.Fatal("operator scalar capability", alias)
		}
		array := types[d.AssociatedArray()]
		if array.Concrete() || array.Usage().Valid() {
			t.Fatal("operator array activated", alias)
		}
		if _, err := Null(d).OperatorOID(); err == nil {
			t.Fatal("NULL mistaken for OID zero")
		}
		order, err := BindOrdering(d.Usage())
		if err != nil {
			t.Fatal(err)
		}
		grouping, err := BindGrouping(d.Usage())
		if err != nil {
			t.Fatal(err)
		}
		var previous Value
		for i, id := range []uint32{0, 551, 2147483648, 4294967295} {
			v, err := OperatorValue(d, id)
			if err != nil {
				t.Fatal(err)
			}
			got, err := v.OperatorOID()
			if err != nil || got != id {
				t.Fatal(got, id, err)
			}
			if !SameDatum(v, v) {
				t.Fatal("identity equality")
			}
			if i > 0 {
				sign, err := order.Compare(ctx, previous, v, InputEnvironment{})
				if err != nil || sign != -1 {
					t.Fatal("unsigned order", sign, err)
				}
			}
			if equal, err := grouping.Equal(ctx, v, v); err != nil || !equal {
				t.Fatal(equal, err)
			}
			if _, err := grouping.Hash(ctx, v); err != nil {
				t.Fatal(err)
			}
			bytes := binary.BigEndian.AppendUint32(nil, id)
			decoded, err := d.BinaryInputWithEnvironment(ctx, bytes, -1, InputEnvironment{})
			if err != nil || !SameDatum(v, decoded) {
				t.Fatal("four-byte identity input", err)
			}
			encoded, err := d.AppendBinaryWithEnvironment(ctx, nil, v, OutputEnvironment{})
			if err != nil || len(encoded) != 4 || binary.BigEndian.Uint32(encoded) != id {
				t.Fatal("four-byte identity output", encoded, err)
			}
			previous = v
		}
		if !d.OutputSessionDependent() {
			t.Fatal("operator output marked immutable")
		}
		negative, _ := Integer(types[Int4], -1)
		plan, err := PlanCoercion(types[Int4].Usage(), d.Usage(), Implicit)
		if err != nil {
			t.Fatal(err)
		}
		v, err := plan.Apply(ctx, negative, InputEnvironment{}, OutputEnvironment{})
		if err != nil {
			t.Fatal(err)
		}
		id, _ := v.OperatorOID()
		if id != 4294967295 {
			t.Fatal(id)
		}
		other := Regoper
		if alias == Regoper {
			other = Regoperator
		}
		plan, err = PlanCoercion(d.Usage(), types[other].Usage(), Implicit)
		if err != nil {
			t.Fatal(err)
		}
		v, err = plan.Apply(ctx, v, InputEnvironment{}, OutputEnvironment{})
		if err != nil {
			t.Fatal(err)
		}
		id, _ = v.OperatorOID()
		if id != 4294967295 {
			t.Fatal(id)
		}
		for _, source := range []ID{Bool, Numeric, Regtype, Regclass, Regproc} {
			if _, err := PlanCoercion(types[source].Usage(), d.Usage(), Explicit); err == nil {
				t.Fatal("invented alias edge", source, alias)
			}
		}
		if _, err := PlanCoercion(types[Text].Usage(), d.Usage(), Implicit); err == nil {
			t.Fatal("implicit text alias edge")
		}
		long, _ := Integer(types[Int8], -1)
		plan, err = PlanCoercion(types[Int8].Usage(), d.Usage(), Implicit)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := plan.Apply(ctx, long, InputEnvironment{}, OutputEnvironment{}); err == nil {
			t.Fatal("negative bigint accepted")
		}
	}
}

func TestRegOperatorReleasedCastEdges(t *testing.T) {
	types := testTypes()
	ctx := context.Background()
	for _, alias := range []ID{Regoper, Regoperator} {
		other := Regoper
		if alias == Regoper {
			other = Regoperator
		}
		for _, edge := range []struct {
			source, target ID
			minimum        CoercionContext
		}{
			{Int2, alias, Implicit}, {Int4, alias, Implicit}, {Int8, alias, Implicit},
			{Oid, alias, Implicit}, {alias, Oid, Implicit}, {alias, other, Implicit},
			{alias, Int4, Assignment}, {alias, Int8, Assignment},
		} {
			for _, mode := range []CoercionContext{Implicit, Assignment, Explicit} {
				plan, err := PlanCoercion(types[edge.source].Usage(), types[edge.target].Usage(), mode)
				if mode < edge.minimum {
					if err == nil {
						t.Fatal("cast context widened", edge, mode)
					}
					continue
				}
				if err != nil {
					t.Fatal(edge, mode, err)
				}
				var input Value
				switch edge.source {
				case Int2, Int4, Int8:
					input, err = Integer(types[edge.source], 551)
				case Oid:
					input, err = OIDValue(types[Oid], 4294967295)
				default:
					input, err = OperatorValue(types[edge.source], 4294967295)
				}
				if err != nil {
					t.Fatal(err)
				}
				got, err := plan.Apply(ctx, input, InputEnvironment{}, OutputEnvironment{})
				if err != nil {
					t.Fatal(edge, mode, err)
				}
				switch edge.target {
				case Oid:
					id, err := got.OID()
					if err != nil || id != 4294967295 {
						t.Fatal("OID carrier", id, err)
					}
				case Int4, Int8:
					want := int64(4294967295)
					if edge.target == Int4 {
						want = -1
					}
					if got.integer != want {
						t.Fatal("signed output", got.integer, want)
					}
				default:
					want := uint32(4294967295)
					if edge.source == Int2 || edge.source == Int4 || edge.source == Int8 {
						want = 551
					}
					id, err := got.OperatorOID()
					if err != nil || id != want {
						t.Fatal("alias carrier", id, want, err)
					}
				}
			}
		}
		for _, n := range []int64{-1, 4294967296} {
			input, _ := Integer(types[Int8], n)
			plan, err := PlanCoercion(types[Int8].Usage(), types[alias].Usage(), Explicit)
			if err != nil {
				t.Fatal(err)
			}
			_, err = plan.Apply(ctx, input, InputEnvironment{}, OutputEnvironment{})
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != "22003" || diagnostic.Message != "OID out of range" {
				t.Fatal("signed bigint range diagnostic", n, err)
			}
		}
		for _, target := range []ID{Int2, Regtype, Regclass, Regproc, Bool, Numeric} {
			if _, err := PlanCoercion(types[alias].Usage(), types[target].Usage(), Explicit); err == nil {
				t.Fatal("invented outgoing edge", alias, target)
			}
		}
	}
}
