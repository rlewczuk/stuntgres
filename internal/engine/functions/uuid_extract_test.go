package functions

import (
	"context"
	"errors"
	"fmt"
	"stuntgres/internal/sqltype"
	"testing"
)

func uuidExtractUnit(t *testing.T, op UUIDExtractKind, text string) (UUIDExtract[stringUnitExpression, *stringUnitContext, struct{}], *stringUnitContext, *[]int) {
	t.Helper()
	types := inspectCarrierTypes()
	value, err := types[sqltype.UUID].Input(context.Background(), text, -1)
	if err != nil {
		t.Fatal(err)
	}
	order := []int{}
	arg := stringUnitArgument{Info: Info[struct{}]{Usage: types[sqltype.UUID].Usage()}, value: value, order: &order}
	node, err := NewUUIDExtract[stringUnitExpression, *stringUnitContext](Info[struct{}]{Usage: types[op.Result()].Usage()}, op, arg)
	if err != nil {
		t.Fatal(err)
	}
	return node, &stringUnitContext{Context: context.Background()}, &order
}

func TestUUIDExtractVectors(t *testing.T) {
	for _, tc := range []struct {
		uuid   string
		micros int64
	}{
		{"c232ab00-9414-11ec-b3c8-9f6bdeced846", 698872942000000},
		{"017f22e2-79b0-7cc3-98c4-dc0c0c07398f", 698872942000000},
		{"00000000-0000-1000-8000-000000000000", -13165977600000000},
		{"00000009-0000-1000-8000-000000000000", -13165977600000000},
		{"0000000a-0000-1000-8000-000000000000", -13165977599999999},
		{"13813fff-1dd2-11b2-8000-000000000000", -946684800000001},
		{"13814009-1dd2-11b2-8000-000000000000", -946684800000000},
		{"1381400a-1dd2-11b2-8000-000000000000", -946684799999999},
		{"ffffffff-ffff-1fff-bfff-ffffffffffff", 102126172860684697},
		{"00000000-0000-7fff-bfff-ffffffffffff", -946684800000000},
		{"00000000-0001-7fff-8000-000000000000", -946684799999000},
		{"ffffffff-ffff-7fff-bfff-ffffffffffff", 280528291910655000},
	} {
		node, c, _ := uuidExtractUnit(t, UUIDExtractTimestamp, tc.uuid)
		v, err := node.Eval(c)
		if err != nil {
			t.Fatal(err)
		}
		n, err := v.Timestamp()
		if err != nil || n != tc.micros {
			t.Fatal(tc.uuid, n, tc.micros, err)
		}
	}
}

func TestUUIDExtractVariantMatrix(t *testing.T) {
	for _, version := range []int{1, 7} {
		for variant := range 256 {
			text := fmt.Sprintf("00000000-0000-%x000-%02x00-000000000000", version, variant)
			for _, op := range []UUIDExtractKind{UUIDExtractTimestamp, UUIDExtractVersion} {
				node, c, _ := uuidExtractUnit(t, op, text)
				v, err := node.Eval(c)
				if err != nil || v.IsNull() != (variant&0xc0 != 0x80) || v.Type() != node.Usage.Type {
					t.Fatal(text, op, v, err)
				}
			}
		}
	}
}

func TestUUIDExtractVersionMatrix(t *testing.T) {
	for version := range 16 {
		text := fmt.Sprintf("00000000-0000-%x000-8000-000000000000", version)
		node, c, _ := uuidExtractUnit(t, UUIDExtractVersion, text)
		v, err := node.Eval(c)
		if err != nil {
			t.Fatal(err)
		}
		n, err := v.Integer()
		if err != nil || n != int64(version) {
			t.Fatal(text, n, err)
		}
		node, c, _ = uuidExtractUnit(t, UUIDExtractTimestamp, text)
		v, err = node.Eval(c)
		if err != nil || v.IsNull() != (version != 1 && version != 7) {
			t.Fatal(text, v, err)
		}
	}
}

func TestUUIDExtractStrictDemand(t *testing.T) {
	for _, op := range []UUIDExtractKind{UUIDExtractTimestamp, UUIDExtractVersion} {
		node, c, order := uuidExtractUnit(t, op, "00000000-0000-1000-8000-000000000000")
		v, err := node.Eval(c)
		if err != nil || len(*order) != 1 || v.Type() != node.Usage.Type {
			t.Fatal(v, err, *order)
		}
		arg := node.Arg.(stringUnitArgument)
		arg.value = sqltype.Null(arg.Usage.Type)
		node.Arg = arg
		*order = nil
		v, err = node.Eval(c)
		if err != nil || !v.IsNull() || v.Type() != node.Usage.Type || len(*order) != 1 {
			t.Fatal(v, err, *order)
		}
		failure := errors.New("uuid child error")
		arg.err = failure
		node.Arg = arg
		if _, err := node.Eval(c); !errors.Is(err, failure) {
			t.Fatal(err)
		}
		arg.literal = true
		node.Arg = arg
		*order = nil
		v, err = node.Eval(c)
		if err != nil || !v.IsNull() || len(*order) != 0 {
			t.Fatal(v, err, *order)
		}
	}
}

func TestUUIDExtractCancellation(t *testing.T) {
	node, c, order := uuidExtractUnit(t, UUIDExtractVersion, "00000000-0000-1000-8000-000000000000")
	ctx, cancel := context.WithCancel(c.Context)
	cancel()
	c.Context = ctx
	if _, err := node.Eval(c); !errors.Is(err, context.Canceled) || len(*order) != 0 {
		t.Fatal(err, *order)
	}
}

func TestUUIDExtractProperties(t *testing.T) {
	node, _, _ := uuidExtractUnit(t, UUIDExtractVersion, "00000000-0000-1000-8000-000000000000")
	if UUIDExtractIntrinsicRule() != ExactImmutable || node.Validate() != nil {
		t.Fatal("invalid intrinsic carrier")
	}
	bad := node
	bad.Kind = 0
	if bad.Validate() == nil {
		t.Fatal("invalid operation accepted")
	}
	bad = node
	bad.Arg = nil
	if bad.Validate() == nil {
		t.Fatal("missing argument accepted")
	}
	bad = node
	bad.Usage = inspectCarrierTypes()[sqltype.Text].Usage()
	if bad.Validate() == nil {
		t.Fatal("wrong result accepted")
	}
}
