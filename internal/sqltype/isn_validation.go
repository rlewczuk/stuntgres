package sqltype

import (
	"context"
	"fmt"
)

type ISNOperation uint8

const (
	ISNIsValid ISNOperation = iota
	ISNMakeValid
	ISNWeakGet
	ISNWeakSet
)

// IsValidISN reads the independent invalid-input bit of an admitted scalar.
// Exact usage, including descriptor generation, is checked before strict NULL.
func IsValidISN(v Value, formal Usage, boolean *Descriptor) (Value, error) {
	if !validISNValidationDatum(v, formal) || boolean == nil || boolean.ID() != Bool {
		return Value{}, fmt.Errorf("invalid ISN validity operands")
	}
	if v.IsNull() {
		return Null(boolean), nil
	}
	return Boolean(boolean, v.integer&1 == 0), nil
}

// MakeValidISN returns a detached inline copy with its flag cleared. Input has
// already corrected the check digit; repairing never rescans or mutates v.
func MakeValidISN(v Value, formal Usage) (Value, error) {
	if !validISNValidationDatum(v, formal) {
		return Value{}, fmt.Errorf("invalid ISN repair operand")
	}
	v.integer &^= 1
	return v, nil
}

// Constructors and import own the checksum invariant. This admitted inline
// seam checks generation, framing, packed bounds and scalar family without
// rescanning decimal digits or recomputing a checksum.
func validISNValidationDatum(v Value, formal Usage) bool {
	if formal.Type == nil || v.typ != formal.Type || !formal.Type.IsISN() || !formal.Valid() {
		return false
	}
	if v.null {
		return true
	}
	if v.integer < 0 || v.integer > 19999999999999 {
		return false
	}
	n := v.integer >> 1
	switch isnRole(formal.Type) {
	case 0:
		return true
	case 1, 2:
		return n >= 9780000000000 && n <= 9789999999999 || n >= 9791000000000 && n <= 9799999999999
	case 3, 4:
		return n >= 9790000000000 && n <= 9790999999999
	case 5, 6:
		return n >= 9770000000000 && n <= 9779999999999
	case 7:
		return v.integer <= 1999999999999
	}
	return false
}

// ISNInputLifecycle is borrowed only by an operation-local input evaluator.
// Isolated codec use without one consumes the detached IsnWeak environment.
type ISNInputLifecycle interface {
	PrepareIsnInput(context.Context, *Descriptor) (bool, error)
}

func ContainsISNInput(d *Descriptor) bool {
	if d == nil {
		return false
	}
	d = d.BaseType()
	if d.IsISN() {
		return true
	}
	if d.IsArray() {
		return ContainsISNInput(d.Element())
	}
	if d.IsComposite() {
		for i := range d.CompositeLen() {
			if ContainsISNInput(d.CompositeMember(i).Usage.Type) {
				return true
			}
		}
	}
	return false
}
