package sqltype

import (
	"bytes"
	"context"
	"errors"
	"math"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestRegObjectAliasDatum(t *testing.T) {
	types, ctx := testTypes(), context.Background()
	for _, pair := range []struct{ scalar, array ID }{{Regcollation, 4192}, {Regnamespace, 4090}} {
		d := types[pair.scalar]
		if !d.Concrete() || d.PostgreSQLTypeLength() != 4 || d.AssociatedArray() != pair.array || types[pair.array].Element() != d || !types[pair.array].Concrete() {
			t.Fatal("alias identity/codec inventory", pair)
		}
		if _, err := RegObjectValue(types[Oid], 1); err == nil {
			t.Fatal("ordinary OID accepted as alias")
		}
		if _, err := Null(d).ObjectOID(); err == nil {
			t.Fatal("NULL accepted as OID payload")
		}
		order, err := BindOrdering(d.Usage())
		if err != nil {
			t.Fatal(err)
		}
		group, err := BindGrouping(d.Usage())
		if err != nil {
			t.Fatal(err)
		}
		previous, _ := RegObjectValue(d, 0)
		for _, id := range []uint32{0, 950, 2147483648, math.MaxUint32} {
			value, err := RegObjectValue(d, id)
			if err != nil {
				t.Fatal(err)
			}
			got, err := value.ObjectOID()
			if err != nil || got != id || value.text != "" || value.tree != nil {
				t.Fatal("noncanonical inline alias", value, err)
			}
			cmp, err := order.Compare(ctx, previous, value, InputEnvironment{})
			if err != nil || id == 0 && cmp != 0 || id != 0 && cmp >= 0 {
				t.Fatal("unsigned alias ordering", cmp, err)
			}
			equal, err := group.Equal(ctx, previous, value)
			if err != nil || equal != (id == 0) {
				t.Fatal("alias grouping equality", equal, err)
			}
			previous = value
		}
	}
}

func TestRegObjectAliasCodec(t *testing.T) {
	types, ctx := testTypes(), context.Background()
	for _, alias := range []ID{Regcollation, Regnamespace} {
		d := types[alias]
		for _, fact := range []struct {
			text string
			oid  uint32
		}{{"-", 0}, {"0", 0}, {"010", 8}, {"4294967295", math.MaxUint32}} {
			value, err := d.Input(ctx, fact.text, -1)
			if err != nil {
				t.Fatal(fact.text, err)
			}
			id, err := value.ObjectOID()
			if err != nil || id != fact.oid {
				t.Fatal("alias numeric input", fact, id, err)
			}
		}
		for _, fact := range []struct{ text, state, message string }{{"08", "22P02", `invalid input syntax for type oid: "08"`}, {"4294967296", "22003", `value "4294967296" is out of range for type oid`}} {
			_, err := d.Input(ctx, fact.text, -1)
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != fact.state || diagnostic.Message != fact.message {
				t.Fatal("oidin diagnostic", fact, err)
			}
		}
		value, err := d.BinaryInputWithEnvironment(ctx, []byte{255, 255, 255, 255}, -1, InputEnvironment{})
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := d.AppendBinaryWithEnvironment(ctx, nil, value, OutputEnvironment{})
		if err != nil || !bytes.Equal(encoded, []byte{255, 255, 255, 255}) {
			t.Fatal("four-byte alias transport", encoded, err)
		}
		for _, fact := range []struct {
			source, target ID
			minimum        CoercionContext
		}{
			{Int2, alias, Implicit}, {Int4, alias, Implicit}, {Int8, alias, Implicit}, {Oid, alias, Implicit}, {alias, Oid, Implicit},
			{alias, Int4, Assignment}, {alias, Int8, Assignment}, {alias, Text, Assignment}, {Text, alias, Explicit}, {VarChar, alias, Explicit},
			{alias, BPChar, Assignment}, {alias, VarChar, Assignment}, {alias, Name, Assignment},
			{BPChar, alias, Explicit}, {Name, alias, Explicit},
		} {
			for mode := Implicit; mode <= Explicit; mode++ {
				_, err := PlanCoercion(types[fact.source].Usage(), types[fact.target].Usage(), mode)
				if (err == nil) != (mode >= fact.minimum) {
					t.Fatal("alias cast context", fact, mode, err)
				}
			}
		}
		for _, source := range []ID{Bool, Regclass, Regtype, Regrole, Regcollation, Regnamespace} {
			if source == alias {
				continue
			}
			if _, err := PlanCoercion(types[source].Usage(), d.Usage(), Explicit); err == nil {
				t.Fatal("unrelated alias direct cast admitted", source, alias)
			}
		}
	}
}
