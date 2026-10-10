package sqltype

func StringBaseConversion(name string) bool {
	return name == "to_bin" || name == "to_oct" || name == "to_hex"
}

func (p StringPlan) evaluateBaseConversion(value Value) (Value, error) {
	n, err := value.Integer()
	if err != nil {
		return Value{}, err
	}
	bits := uint64(n)
	if p.formal[0].Type.ID() == Int4 {
		bits = uint64(uint32(n))
	}
	shift := uint(4)
	if p.name == "to_bin" {
		shift = 1
	} else if p.name == "to_oct" {
		shift = 3
	}
	const digits = "0123456789abcdef"
	var scratch [64]byte
	start := len(scratch)
	mask := uint64(1<<shift) - 1
	for {
		start--
		scratch[start] = digits[bits&mask]
		bits >>= shift
		if bits == 0 {
			break
		}
	}
	return String(p.result.Type, string(scratch[start:])), nil
}
