package functions

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/sqltype"
)

type uuidOsspUnitContext struct {
	uuidUnitContext
	ready, entropy int
}

func (c *uuidOsspUnitContext) FunctionUUIDReady() error { c.ready++; return c.Context.Err() }
func (c *uuidOsspUnitContext) FunctionUUIDEntropy(dst []byte) error {
	c.entropy++
	if len(dst) != 16 {
		panic("v4 must demand exactly 16 entropy bytes")
	}
	for i := range dst {
		dst[i] = 255
	}
	return nil
}

type uuidOsspUnitArgument struct{}

func (uuidOsspUnitArgument) ResultUsage() sqltype.Usage { panic("zero-arity leaf has no argument") }
func (uuidOsspUnitArgument) Eval(*uuidOsspUnitContext) (sqltype.Value, error) {
	panic("zero-arity leaf has no argument")
}

func TestUUIDOsspKernelDemand(t *testing.T) {
	typ := formatUnitTypes()[sqltype.UUID]
	n := UUIDGeneration[uuidOsspUnitArgument, *uuidOsspUnitContext, struct{}]{Info: Info[struct{}]{Usage: typ.Usage()}, Name: "uuidv4"}
	c := &uuidOsspUnitContext{uuidUnitContext: uuidUnitContext{Context: context.Background()}}
	// FunctionUUIDTime and FunctionInput remain panic probes from uuidUnitContext.
	if c.ready != 0 || c.entropy != 0 {
		t.Fatal("construction demanded random data")
	}
	for i := 1; i <= 2; i++ {
		value, err := n.Eval(c)
		if err != nil || value.IsNull() || value.Type() != typ {
			t.Fatal(value, err)
		}
		bytes, err := value.UUID()
		if err != nil || bytes != uuidUnitBytes(t, "ffffffffffff4fffbfffffffffffffff") || c.ready != i || c.entropy != i {
			t.Fatal(bytes, err, c.ready, c.entropy)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c.Context = ctx
	if _, err := n.Eval(c); !errors.Is(err, context.Canceled) || c.ready != 2 || c.entropy != 2 {
		t.Fatal("cancellation demanded authority/entropy", err, c.ready, c.entropy)
	}
}
