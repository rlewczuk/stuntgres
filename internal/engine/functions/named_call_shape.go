package functions

import (
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// NamedCallShape maps one checked declaration into written-operand order.
// It is a producer-free extension of CallShape, not a resolver or Bind retry.
type NamedCallShape func(CallTypeContext, parser.FunctionExpr, []Argument, []sqltype.Usage) (inputs, targets []sqltype.Usage, valid bool)

// namedSlots is shared by provider binding and applicability. The caller owns
// the roster and its required/default/variadic rules; this helper owns only
// unique input-slot assignment, never family admission or SQL type semantics.
func namedSlots(raw parser.FunctionExpr, labels []string, required int) (slots [7]int, valid bool) {
	if len(raw.Args) > len(labels) || len(labels) > 7 || len(raw.ArgNames) != 0 && len(raw.ArgNames) != len(raw.Args) {
		return slots, false
	}
	var supplied [7]bool
	for i := range raw.Args {
		slot := i
		if len(raw.ArgNames) > i && raw.ArgNames[i].Text != "" {
			slot = -1
			for j, label := range labels {
				if label == raw.ArgNames[i].Text {
					slot = j
					break
				}
			}
		}
		if slot < 0 || slot >= len(labels) || supplied[slot] {
			return slots, false
		}
		slots[i], supplied[slot] = slot, true
	}
	for slot := range required {
		if !supplied[slot] {
			return slots, false
		}
	}
	return slots, true
}

func declaredTypes(declared []sqltype.Usage, types []sqltype.ID) bool {
	if len(declared) != len(types) {
		return false
	}
	for i, typ := range types {
		if declared[i].Type == nil || declared[i].Type.ID() != typ {
			return false
		}
	}
	return true
}

func namedInputs(b CallTypeContext, operands []Argument, declared []sqltype.Usage, slots []int) ([]sqltype.Usage, []sqltype.Usage, bool) {
	storage := make([]sqltype.Usage, 2*len(slots))
	inputs, targets := storage[:len(slots):len(slots)], storage[len(slots):]
	for i, slot := range slots {
		if slot < 0 || slot >= len(declared) {
			return nil, nil, false
		}
		inputs[i], targets[i] = declared[slot], declared[slot]
		if !inputs[i].Type.Concrete() {
			targets[i] = operands[i].Usage
		}
	}
	return inputs, targets, knownCallEdges(b, operands, targets)
}

func fixedNamedInputs(b CallTypeContext, raw parser.FunctionExpr, operands []Argument, declared []sqltype.Usage, labels []string, required int) ([]sqltype.Usage, []sqltype.Usage, bool) {
	slots, ok := namedSlots(raw, labels, required)
	if !ok {
		return nil, nil, false
	}
	return namedInputs(b, operands, declared, slots[:len(raw.Args)])
}
