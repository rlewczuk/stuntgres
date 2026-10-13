package sqltype

import (
	"context"
	"encoding/binary"
	"errors"
	"strconv"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestIdentifierScalarProviders(t *testing.T) {
	ctx := context.Background()
	types, fresh := testTypes(), testTypes()
	for _, id := range []ID{Xid, Cid, Xid8} {
		d := types[id]
		width := int16(4)
		max := "4294967295"
		if id == Xid8 {
			width = 8
			max = "18446744073709551615"
		}
		if !d.Concrete() || d.PostgreSQLTypeLength() != width || d.Usage().Typmod != -1 || d.Usage().Collation.ID != 0 {
			t.Fatal("identifier descriptor", d)
		}
		for _, text := range []string{"0", "1", max} {
			v, err := d.Input(ctx, text, -1)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = v.OID(); err == nil {
				t.Fatal("identifier adopted OID authority")
			}
			if _, err = v.Integer(); err == nil {
				t.Fatal("identifier adopted integer authority")
			}
			n, err := strconv.ParseUint(text, 10, 64)
			if err != nil {
				t.Fatal(err)
			}
			switch id {
			case Xid:
				got, err := v.XID()
				if err != nil || uint64(got) != n {
					t.Fatal(got, err)
				}
				if _, err = v.CID(); err == nil {
					t.Fatal("XID adopted CID")
				}
				if _, err = v.XID8(); err == nil {
					t.Fatal("XID adopted XID8")
				}
			case Cid:
				got, err := v.CID()
				if err != nil || uint64(got) != n {
					t.Fatal(got, err)
				}
				if _, err = v.XID(); err == nil {
					t.Fatal("CID adopted XID")
				}
				if _, err = v.XID8(); err == nil {
					t.Fatal("CID adopted XID8")
				}
			case Xid8:
				got, err := v.XID8()
				if err != nil || got != n {
					t.Fatal(got, err)
				}
				if _, err = v.XID(); err == nil {
					t.Fatal("XID8 adopted XID")
				}
				if _, err = v.CID(); err == nil {
					t.Fatal("XID8 adopted CID")
				}
			}
			output, err := d.Output(ctx, v)
			if err != nil || output != text {
				t.Fatal(output, err)
			}
			literal, err := d.SQLLiteral(ctx, v)
			if err != nil || literal != "'"+text+"'" {
				t.Fatal(literal, err)
			}
			reowned, err := v.Retag(fresh[id])
			if err != nil {
				t.Fatal(err)
			}
			if output, err = fresh[id].Output(ctx, reowned); err != nil || output != text {
				t.Fatal(output, err)
			}
			for _, foreign := range []ID{Oid, Int4, Xid, Cid, Xid8} {
				if foreign != id {
					if _, err = v.Retag(types[foreign]); err == nil {
						t.Fatal("foreign identifier Retag", id, foreign)
					}
				}
			}
			group, err := BindGrouping(d.Usage())
			if err != nil {
				t.Fatal(err)
			}
			other, err := d.Input(ctx, text, -1)
			if err != nil || !SameDatum(v, other) {
				t.Fatal(other, err)
			}
			equal, err := group.Equal(ctx, v, other)
			if err != nil || !equal {
				t.Fatal(equal, err)
			}
			hash, err := group.Hash(ctx, v)
			if err != nil {
				t.Fatal(err)
			}
			again, err := group.Hash(ctx, other)
			if err != nil || hash != again {
				t.Fatal(hash, again, err)
			}
			equal, err = group.Equal(ctx, Null(d), Null(d))
			if err != nil || !equal {
				t.Fatal("NULL grouping", equal, err)
			}
			wire, err := d.AppendBinaryWithEnvironment(ctx, nil, v, OutputEnvironment{})
			if err != nil || len(wire) != int(width) {
				t.Fatal(wire, err)
			}
			if width == 8 {
				if binary.BigEndian.Uint64(wire) != n {
					t.Fatal(wire, n)
				}
			} else if uint64(binary.BigEndian.Uint32(wire)) != n {
				t.Fatal(wire, n)
			}
			received, err := d.BinaryInputWithEnvironment(ctx, wire, -1, InputEnvironment{})
			if err != nil || !SameDatum(v, received) {
				t.Fatal(received, err)
			}
		}
		null := Null(d)
		switch id {
		case Xid:
			if _, err := null.XID(); err == nil {
				t.Fatal("NULL accessor")
			}
		case Cid:
			if _, err := null.CID(); err == nil {
				t.Fatal("NULL accessor")
			}
		case Xid8:
			if _, err := null.XID8(); err == nil {
				t.Fatal("NULL accessor")
			}
		}
		domain, err := Domain(ID(90000)+id, "identifier_domain", "identifier_domain", d.Usage())
		if err != nil {
			t.Fatal(err)
		}
		if _, err = Array(ID(100000)+id, "_identifier_domain", domain); err == nil {
			t.Fatal("domain array loophole", id)
		}
	}
	for _, foreign := range []*Descriptor{nil, types[Oid], types[Int4]} {
		if _, err := XIDValue(foreign, 1); err == nil {
			t.Fatal("foreign xid constructor")
		}
		if _, err := CIDValue(foreign, 1); err == nil {
			t.Fatal("foreign cid constructor")
		}
		if _, err := XID8Value(foreign, 1); err == nil {
			t.Fatal("foreign xid8 constructor")
		}
	}
}

func TestIdentifierInputErrorOrder(t *testing.T) {
	ctx := context.Background()
	types := testTypes()
	for _, id := range []ID{Xid, Cid, Xid8} {
		d := types[id]
		cases := []struct{ in, code string }{{"08", "22P02"}, {"0x", "22P02"}, {"0b10", "22P02"}, {"1_000", "22P02"}, {"\u00a01", "22P02"}, {"18446744073709551616x", "22003"}}
		if id != Xid8 {
			cases = append(cases, struct{ in, code string }{"4294967296x", "22P02"}, struct{ in, code string }{"4294967296", "22003"}, struct{ in, code string }{"-2147483649", "22003"})
		}
		for _, tc := range cases {
			_, err := d.Input(ctx, tc.in, -1)
			var diagnostic *sqlstate.Error
			message := `invalid input syntax for type ` + d.Name() + `: "` + tc.in + `"`
			if tc.code == "22003" {
				message = `value "` + tc.in + `" is out of range for type ` + d.Name()
			}
			if !errors.As(err, &diagnostic) || diagnostic.Code != tc.code || diagnostic.Message != message {
				t.Fatal(tc, err)
			}
		}
	}
}

func TestIdentifierOperatorAdmission(t *testing.T) {
	ctx := context.Background()
	types := testTypes()
	for _, left := range []ID{Xid, Cid, Xid8, Oid, Int4} {
		for _, right := range []ID{Xid, Cid, Xid8, Oid, Int4} {
			if !IsTransactionIdentifier(left) && !IsTransactionIdentifier(right) {
				continue
			}
			for operator := CompareEqual; operator <= CompareGreaterEqual; operator++ {
				want := left == Xid8 && right == Xid8 || left == Xid && (right == Xid || right == Int4) && (operator == CompareEqual || operator == CompareNotEqual) || left == Cid && right == Cid && operator == CompareEqual
				predicate, err := BindComparisonPredicate(operator, types[left], types[right], types[Bool], CollationRef{}, false)
				if (err == nil) != want {
					t.Fatal(left, right, operator, err)
				}
				if want {
					result, err := predicate.Evaluate(ctx, Null(types[left]), Null(types[right]), InputEnvironment{})
					if err != nil || !result.IsNull() || result.Type() != types[Bool] {
						t.Fatal(result, err)
					}
				}
			}
		}
	}
	upper, err := XID8Value(types[Xid8], uint64(1)<<63)
	if err != nil {
		t.Fatal(err)
	}
	max, err := XID8Value(types[Xid8], ^uint64(0))
	if err != nil {
		t.Fatal(err)
	}
	ordering, err := BindOrdering(types[Xid8].Usage())
	if err != nil || ordering.DefaultBtreeOpclass() != 10053 {
		t.Fatal(ordering, err)
	}
	if n, err := ordering.Compare(ctx, upper, max, InputEnvironment{}); err != nil || n != -1 {
		t.Fatal(n, err)
	}
	for _, id := range []ID{Xid, Cid} {
		if _, err := BindOrdering(types[id].Usage()); err == nil {
			t.Fatal("identifier ordering", id)
		}
	}
	plan, err := PlanCoercion(types[Xid8].Usage(), types[Xid].Usage(), Explicit)
	if err != nil {
		t.Fatal(err)
	}
	converted, err := plan.Apply(ctx, max, InputEnvironment{}, OutputEnvironment{})
	if err != nil {
		t.Fatal(err)
	}
	n, err := converted.XID()
	if err != nil || n != ^uint32(0) {
		t.Fatal(n, err)
	}
}
