package functions

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

func TestUUIDOsspNamespaceConstants(t *testing.T) {
	typ := formatUnitTypes()[sqltype.UUID]
	for _, tc := range []struct {
		name string
		want [16]byte
	}{
		{"uuid_nil", [16]byte{}},
		{"uuid_ns_dns", [16]byte{0x6b, 0xa7, 0xb8, 0x10, 0x9d, 0xad, 0x11, 0xd1, 0x80, 0xb4, 0, 0xc0, 0x4f, 0xd4, 0x30, 0xc8}},
		{"uuid_ns_url", [16]byte{0x6b, 0xa7, 0xb8, 0x11, 0x9d, 0xad, 0x11, 0xd1, 0x80, 0xb4, 0, 0xc0, 0x4f, 0xd4, 0x30, 0xc8}},
		{"uuid_ns_oid", [16]byte{0x6b, 0xa7, 0xb8, 0x12, 0x9d, 0xad, 0x11, 0xd1, 0x80, 0xb4, 0, 0xc0, 0x4f, 0xd4, 0x30, 0xc8}},
		{"uuid_ns_x500", [16]byte{0x6b, 0xa7, 0xb8, 0x14, 0x9d, 0xad, 0x11, 0xd1, 0x80, 0xb4, 0, 0xc0, 0x4f, 0xd4, 0x30, 0xc8}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n, err := NewUUIDGeneration(expr.Info{Usage: typ.Usage(), Property: expr.Properties{Volatility: expr.Immutable}}, tc.name, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			c := &uuidOsspUnitContext{uuidUnitContext: uuidUnitContext{Context: context.Background()}}
			for range 2 {
				v, err := n.Eval(c)
				if err != nil || v.IsNull() || v.Type() != typ {
					t.Fatal(v, err)
				}
				b, err := v.UUID()
				if err != nil || b != tc.want {
					t.Fatal(b, tc.want, err)
				}
			}
			if c.ready != 0 || c.entropy != 0 {
				t.Fatal("constant demanded session/entropy", c.ready, c.entropy)
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			c.Context = ctx
			if _, err := n.Eval(c); !errors.Is(err, context.Canceled) {
				t.Fatal("canceled constant", err)
			}
			if c.ready != 0 || c.entropy != 0 {
				t.Fatal("canceled constant demanded session/entropy")
			}
		})
	}
}

func TestUUIDOsspNamespaceConstruction(t *testing.T) {
	typ := formatUnitTypes()[sqltype.UUID]
	info := expr.Info{Usage: typ.Usage(), Property: expr.Properties{Volatility: expr.Immutable}}
	for _, tc := range []struct {
		name      string
		args      []expr.Expr
		timestamp *sqltype.Descriptor
	}{
		{"missing_uuid", nil, nil},
		{"uuid_nil", []expr.Expr{nil}, nil},
		{"uuid_ns_dns", nil, typ},
	} {
		if _, err := NewUUIDGeneration(info, tc.name, tc.args, tc.timestamp); err == nil {
			t.Fatal("invalid constant construction accepted", tc.name)
		}
	}
}
