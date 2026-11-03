package sqltype

import (
	"fmt"
	"strconv"
)

func unsignedNumeric(d *Descriptor, n uint64, negative bool) Value {
	if n == 0 {
		return Value{typ: d}
	}
	return normalizeNumeric(Value{typ: d, text: strconv.FormatUint(n, 10), negative: negative})
}

// NumericUint64Value constructs an exact nonnegative NUMERIC without a signed
// narrowing or text input round trip. d must supply concrete numeric authority.
func NumericUint64Value(d *Descriptor, n uint64) (Value, error) {
	if d == nil || d.id != Numeric || !d.Concrete() || d.IsDomain() {
		return Value{}, fmt.Errorf("invalid numeric descriptor")
	}
	return unsignedNumeric(d, n, false), nil
}
