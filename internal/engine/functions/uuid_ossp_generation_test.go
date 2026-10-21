package functions

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/expr"
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

func TestUUIDOsspKernelDemand(t *testing.T) {
	typ := formatUnitTypes()[sqltype.UUID]
	n := UUIDGeneration{Info: expr.Info{Usage: typ.Usage()}, Name: "uuidv4"}
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

type uuidOsspTimeContext struct {
	uuidOsspUnitContext
	draws     int
	multicast bool
}

func (c *uuidOsspTimeContext) FunctionUUIDV1(multicast bool) ([16]byte, error) {
	c.draws++
	c.multicast = multicast
	return [16]byte{6: 0x10, 8: 0x80}, nil
}
func TestUUIDOsspTimeKernelDemand(t *testing.T) {
	typ := formatUnitTypes()[sqltype.UUID]
	for _, name := range []string{"uuid_generate_v1", "uuid_generate_v1mc"} {
		c := &uuidOsspTimeContext{uuidOsspUnitContext: uuidOsspUnitContext{uuidUnitContext: uuidUnitContext{Context: context.Background()}}}
		info := expr.Info{Usage: typ.Usage()}
		n, err := NewUUIDGeneration(info, name, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := n.Rebuild(info, nil); err != nil {
			t.Fatal(err)
		}
		if _, err := n.Render(); err != nil {
			t.Fatal(err)
		}
		if err := n.replayFault(); err != nil {
			t.Fatal(err)
		}
		if c.ready != 0 || c.draws != 0 || c.entropy != 0 {
			t.Fatal("construction demanded capability")
		}
		value, err := n.Eval(c)
		if err != nil || value.Type() != typ || value.IsNull() || c.draws != 1 || c.ready != 1 || c.entropy != 0 || c.multicast != (name == "uuid_generate_v1mc") {
			t.Fatal(value, err, c)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		c.Context = ctx
		if _, err := n.Eval(c); !errors.Is(err, context.Canceled) || c.draws != 1 || c.ready != 1 {
			t.Fatal(err, c)
		}
		if _, err := NewUUIDGeneration(info, name, []expr.Expr{nil}, nil); err == nil {
			t.Fatal("accepted argument")
		}
		if _, err := NewUUIDGeneration(info, name, nil, typ); err == nil {
			t.Fatal("accepted timestamp")
		}
	}
	if _, err := NewUUIDGeneration(expr.Info{Usage: typ.Usage()}, "uuid_generate_v1x", nil, nil); err == nil {
		t.Fatal("accepted selector")
	}
}
