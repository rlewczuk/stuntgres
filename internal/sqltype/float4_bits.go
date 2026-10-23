package sqltype

import (
	"fmt"
	"math"
)

// Float4FromBits imports an IEEE binary32 word without narrowing a widened NaN.
// The word remains authoritative through immutable copies, descriptor reownership,
// binary transport and binary casts. Arithmetic creates a fresh value instead.
func Float4FromBits(d *Descriptor, bits uint32) (Value, error) {
	if d == nil || d.id != Float4 || d.IsDomain() {
		return Value{}, fmt.Errorf("invalid float4 descriptor")
	}
	return float4FromBits(d, bits), nil
}

func float4FromBits(d *Descriptor, bits uint32) Value {
	return Value{typ: d, floating: float64(math.Float32frombits(bits)), float4Raw: bits, float4RawValid: true}
}

// Float4Bits returns the exact REAL word, including signaling NaNs and their
// payloads. Stored-literal renderers can use this word for an ordinary SQL binary
// cast bootstrap rather than relying on lossy NaN text output.
func (v Value) Float4Bits() (uint32, error) {
	if v.typ == nil || v.null || v.typ.BaseType().id != Float4 {
		return 0, fmt.Errorf("not a float4 value")
	}
	return v.float4Bits(), nil
}

// Float4NeedsRawReplay reports whether canonical REAL text would lose the word.
// Finite values (including subnormals and signed zero) and infinities roundtrip
// through SQLLiteral. NaN text reconstructs only the canonical positive quiet NaN.
func (v Value) Float4NeedsRawReplay() bool {
	if v.typ == nil || v.null || v.typ.BaseType().id != Float4 {
		return false
	}
	bits := v.float4Bits()
	return bits&0x7fffffff > 0x7f800000 && bits != 0x7fc00000
}

func (v Value) float4Bits() uint32 {
	if v.float4RawValid {
		return v.float4Raw
	}
	return math.Float32bits(float32(v.floating))
}
