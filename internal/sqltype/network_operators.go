package sqltype

import (
	"context"
	"fmt"
	"stuntgres/internal/sqlstate"
)

// NetworkBitwise is an immutable INET signature. CIDR coercion belongs to the
// binder; its result must not remask host bits or retain CIDR authority.
type NetworkBitwise struct {
	operator    BitwiseOperator
	left, right *Descriptor
}

func BindNetworkBitwise(operator BitwiseOperator, left, right *Descriptor) (NetworkBitwise, error) {
	valid := left != nil && left.id == Inet
	switch operator {
	case BitwiseNot:
		valid = valid && right == nil
	case BitwiseAnd, BitwiseOr:
		valid = valid && right == left
	default:
		valid = false
	}
	if !valid {
		return NetworkBitwise{}, fmt.Errorf("invalid network bitwise signature")
	}
	return NetworkBitwise{operator, left, right}, nil
}

func (p NetworkBitwise) Unary(ctx context.Context, a Value) (Value, error) {
	if p.left == nil || p.operator != BitwiseNot || p.right != nil || a.typ != p.left {
		return Value{}, fmt.Errorf("invalid unary network bitwise values")
	}
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if a.null {
		return Null(p.left), nil
	}
	if !a.network.valid(false) {
		return Value{}, fmt.Errorf("invalid network datum")
	}
	n := a.network
	for i := range networkWidth(n) {
		n.address[i] = ^n.address[i]
	}
	return Value{typ: p.left, network: n}, nil
}

func (p NetworkBitwise) Binary(ctx context.Context, a, b Value) (Value, error) {
	if p.left == nil || p.right == nil || (p.operator != BitwiseAnd && p.operator != BitwiseOr) || a.typ != p.left || b.typ != p.right {
		return Value{}, fmt.Errorf("invalid binary network bitwise values")
	}
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if a.null || b.null {
		return Null(p.left), nil
	}
	if !a.network.valid(false) || !b.network.valid(false) {
		return Value{}, fmt.Errorf("invalid network datum")
	}
	if a.network.family != b.network.family {
		if p.operator == BitwiseAnd {
			return Value{}, failure("22023", "cannot AND inet values of different sizes")
		}
		return Value{}, failure("22023", "cannot OR inet values of different sizes")
	}
	n := a.network
	n.prefix = max(n.prefix, b.network.prefix)
	for i := range networkWidth(n) {
		if p.operator == BitwiseAnd {
			n.address[i] &= b.network.address[i]
		} else {
			n.address[i] |= b.network.address[i]
		}
	}
	return Value{typ: p.left, network: n}, nil
}

func networkWidth(n networkDatum) int {
	if n.family == 4 {
		return 4
	}
	return 16
}

// Follow REL_18_0's signed bytewise addition, including sign extension. The
// final carry distinguishes a legal negative addend from address underflow.
func networkOffset(n networkDatum, addend int64) (networkDatum, bool) {
	var carry uint16
	for i := networkWidth(n) - 1; i >= 0; i-- {
		carry += uint16(n.address[i]) + uint16(byte(addend))
		n.address[i] = byte(carry)
		carry >>= 8
		addend >>= 8
	}
	return n, addend == 0 && carry == 0 || addend == -1 && carry == 1
}

// PostgreSQL18 checks upper bytes against the low INT8 sign extension, but
// does not check the final IPv6 carry. Keep the released modular edge behavior.
func networkDifference(a, b networkDatum) (int64, bool) {
	width := networkWidth(a)
	carry := uint16(1)
	var result uint64
	for i := width - 1; i >= 0; i-- {
		carry += uint16(a.address[i]) + uint16(^b.address[i])
		v := byte(carry)
		carry >>= 8
		if width-1-i < 8 {
			result |= uint64(v) << (8 * (width - 1 - i))
		} else {
			extension := byte(0)
			if int64(result) < 0 {
				extension = 0xff
			}
			if v != extension {
				return 0, false
			}
		}
	}
	if width == 4 && carry == 0 {
		result |= 0xffffffff00000000
	}
	return int64(result), true
}

func (p Arithmetic) networkBinary(a, b Value) (Value, error) {
	if a.typ.id == Inet && b.typ.id == Inet {
		if !a.network.valid(false) || !b.network.valid(false) {
			return Value{}, fmt.Errorf("invalid network datum")
		}
		if a.network.family != b.network.family {
			return Value{}, failure("22023", "cannot subtract inet values of different sizes")
		}
		n, ok := networkDifference(a.network, b.network)
		if !ok {
			return Value{}, failure("22003", "result is out of range")
		}
		return Value{typ: p.result, integer: n}, nil
	}
	n, addend := a.network, b.integer
	commuted := a.typ.id == Int8
	if commuted {
		n, addend = b.network, a.integer
	}
	if !n.valid(false) {
		return Value{}, fmt.Errorf("invalid network datum")
	}
	if p.operator == ArithmeticSubtract {
		// The pinned release negates INT64_MIN unchecked; Go's defined signed
		// negation preserves that observed behavior without a special case.
		addend = -addend
	}
	n, ok := networkOffset(n, addend)
	if !ok {
		d := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22003", Message: "result is out of range"}
		if commuted {
			d.Context = "SQL function \"int8pl_inet\" statement 1"
		}
		return Value{}, d
	}
	return Value{typ: p.result, network: n}, nil
}
