package expr

import (
	"fmt"
	"stuntgres/internal/sqltype"
	"testing"
)

type captureTestOperand struct{ Info }

func (*captureTestOperand) Eval(Context) (sqltype.Value, error) { panic("capture evaluated operand") }

type captureTestIndexed struct {
	args  [2]Expr
	slots [2]uint8
	count int
}

func (e captureTestIndexed) Capture(CaptureKind) (CaptureOperands, bool, error) {
	return CaptureOperands{Count: e.count, Indexed: true}, true, nil
}

func (e captureTestIndexed) CaptureOperand(written int) (Expr, error) {
	if e.count < 0 || e.count > len(e.slots) || written < 0 || written >= e.count || int(e.slots[written]) >= len(e.args) {
		return nil, fmt.Errorf("invalid test written slot")
	}
	return e.args[e.slots[written]], nil
}

type captureTestPromoted struct{ captureTestIndexed }

// No indexed method: even a zero-count indexed descriptor must reject this source.
type captureTestPlain struct{}

func (captureTestPlain) Capture(CaptureKind) (CaptureOperands, bool, error) {
	return CaptureOperands{}, true, nil
}

func TestCaptureOperandsWrittenSlots(t *testing.T) {
	a, b := &captureTestOperand{}, &captureTestOperand{}
	node := captureTestIndexed{args: [2]Expr{a, b}, slots: [2]uint8{1, 0}, count: 2}
	before := node
	for _, source := range []Capturable{node, &node, captureTestPromoted{node}, &captureTestPromoted{node}} {
		operands, _, _ := source.Capture(CaptureFunction)
		if err := operands.Check(source, 2); err != nil {
			t.Fatal(err)
		}
		for i, want := range []Expr{b, a} {
			got, err := operands.At(source, i)
			if err != nil || got != want {
				t.Fatal(i, got, err)
			}
		}
		if err := operands.Check(source, 1); err == nil {
			t.Fatal("wrong raw arity accepted")
		}
		for _, index := range []int{-1, 2} {
			if _, err := operands.At(source, index); err == nil {
				t.Fatal("invalid index accepted", index)
			}
		}
	}
	if node != before {
		t.Fatal("mutated indexed storage")
	}
	args := []Expr{a, b}
	for _, operands := range []CaptureOperands{{Args: args, Count: 2}, {Inline: [2]Expr{a, b}, Count: 2}} {
		if err := operands.Check(nil, 2); err != nil {
			t.Fatal(err)
		}
		for i, want := range args {
			if got, err := operands.At(nil, i); err != nil || got != want {
				t.Fatal(i, got, err)
			}
		}
	}
	if args[0] != a || args[1] != b {
		t.Fatal("mutated borrowed storage")
	}
	for _, bad := range []CaptureOperands{
		{Count: -1}, {Count: 1}, {Count: 3}, {Args: args, Count: 3}, {Args: []Expr{nil}, Count: 1},
		{Indexed: true, Args: []Expr{}, Count: 0}, {Indexed: true, Args: args, Count: 2},
		{Indexed: true, Inline: [2]Expr{a}, Count: 2}, {Indexed: true, Inline: [2]Expr{nil, b}, Count: 0},
	} {
		if err := bad.Check(node, bad.Count); err == nil {
			t.Fatal("malformed operands accepted", bad)
		}
	}
	for _, source := range []Capturable{nil, captureTestPlain{}} {
		for _, count := range []int{0, 1} {
			bad := CaptureOperands{Indexed: true, Count: count}
			if err := bad.Check(source, count); err == nil {
				t.Fatal("missing indexed accessor accepted", count)
			}
			if _, err := bad.At(source, 0); err == nil {
				t.Fatal("missing indexed accessor accepted by At")
			}
		}
	}
	for _, source := range []captureTestIndexed{
		{args: node.args, slots: [2]uint8{2}, count: 1},
		{args: [2]Expr{nil, b}, count: 1},
		{args: node.args, count: 3},
		{args: node.args, count: -1},
	} {
		operands, _, _ := source.Capture(CaptureFunction)
		if err := operands.Check(source, source.count); err == nil {
			t.Fatal("malformed indexed node accepted", source)
		}
	}
	if err := (CaptureOperands{}).Check(nil, 0); err != nil {
		t.Fatal("zero is an explicit count", err)
	}
	if err := (CaptureOperands{Indexed: true}).Check(captureTestIndexed{}, 0); err != nil {
		t.Fatal("indexed zero rejected", err)
	}
}
