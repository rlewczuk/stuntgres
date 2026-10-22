package sqltype

import (
	"context"
	"fmt"
)

// NetworkSubnetOperator is a network-prefix predicate, independent of host bits.
type NetworkSubnetOperator uint8

const (
	NetworkSubnetSub NetworkSubnetOperator = iota
	NetworkSubnetSubeq
	NetworkSubnetSup
	NetworkSubnetSupeq
	NetworkSubnetOverlap
)

func NetworkSubnetKind(name string) (NetworkSubnetOperator, bool) {
	switch name {
	case "<<":
		return NetworkSubnetSub, true
	case "<<=":
		return NetworkSubnetSubeq, true
	case ">>":
		return NetworkSubnetSup, true
	case ">>=":
		return NetworkSubnetSupeq, true
	case "&&":
		return NetworkSubnetOverlap, true
	}
	return 0, false
}
func (o NetworkSubnetOperator) Name() string {
	switch o {
	case NetworkSubnetSub:
		return "<<"
	case NetworkSubnetSubeq:
		return "<<="
	case NetworkSubnetSup:
		return ">>"
	case NetworkSubnetSupeq:
		return ">>="
	case NetworkSubnetOverlap:
		return "&&"
	}
	return ""
}

// NetworkSubnetPlan retains distinct input and Boolean result authorities.
type NetworkSubnetPlan struct {
	operation     NetworkSubnetOperator
	input, result *Descriptor
}

func BindNetworkSubnet(op NetworkSubnetOperator, result, left, right Usage) (NetworkSubnetPlan, error) {
	if op.Name() == "" || left.Type == nil || left.Type.id != Inet || left != left.Type.Usage() || right != left || result.Type == nil || result.Type != left.Type.networkBoolean || result != result.Type.Usage() {
		return NetworkSubnetPlan{}, fmt.Errorf("invalid network subnet signature")
	}
	return NetworkSubnetPlan{op, left.Type, result.Type}, nil
}
func (p NetworkSubnetPlan) Operation() NetworkSubnetOperator { return p.operation }
func (p NetworkSubnetPlan) Name() string                     { return p.operation.Name() }
func (p NetworkSubnetPlan) Evaluate(ctx context.Context, values [2]Value) (Value, error) {
	if p.input == nil || p.result == nil || p.operation.Name() == "" || values[0].typ != p.input || values[1].typ != p.input {
		return Value{}, fmt.Errorf("invalid network subnet values")
	}
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if values[0].null || values[1].null {
		return Null(p.result), nil
	}
	a, b := &values[0].network, &values[1].network
	if !a.valid(false) || !b.valid(false) {
		return Value{}, fmt.Errorf("invalid network datum")
	}
	match := false
	if a.family == b.family {
		width := min(a.prefix, b.prefix)
		switch p.operation {
		case NetworkSubnetSub:
			match = a.prefix > b.prefix
		case NetworkSubnetSubeq:
			match = a.prefix >= b.prefix
		case NetworkSubnetSup:
			match = a.prefix < b.prefix
		case NetworkSubnetSupeq:
			match = a.prefix <= b.prefix
		case NetworkSubnetOverlap:
			match = true
		}
		if match {
			match = networkPrefixEqual(a, b, width)
		}
	}
	return Value{typ: p.result, boolean: match}, nil
}
func networkPrefixEqual(a, b *networkDatum, bits uint8) bool {
	whole := int(bits / 8)
	for i := range whole {
		if a.address[i] != b.address[i] {
			return false
		}
	}
	if rest := bits % 8; rest != 0 {
		mask := byte(0xff << (8 - rest))
		return a.address[whole]&mask == b.address[whole]&mask
	}
	return true
}
