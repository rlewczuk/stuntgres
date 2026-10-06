package sqltype

import "fmt"

// UUIDValue stores the bytes in PostgreSQL's textual (network) order.
func UUIDValue(d *Descriptor, bytes [16]byte) (Value, error) {
	if d == nil || d.id != UUID {
		return Value{}, fmt.Errorf("invalid UUID descriptor")
	}
	return Value{typ: d, uuid: bytes}, nil
}

func (v Value) UUID() ([16]byte, error) {
	if v.null || v.typ == nil || v.typ.id != UUID {
		return [16]byte{}, fmt.Errorf("not a UUID value")
	}
	return v.uuid, nil
}

func (d *Descriptor) uuidInput(text string) (Value, error) {
	original := text
	braced := len(text) > 0 && text[0] == '{'
	if braced {
		text = text[1:]
	}
	var bytes [16]byte
	for i := range bytes {
		if len(text) < 2 {
			return Value{}, uuidSyntax(original)
		}
		hi, okHi := digit(text[0], 16)
		lo, okLo := digit(text[1], 16)
		if !okHi || !okLo {
			return Value{}, uuidSyntax(original)
		}
		bytes[i] = byte(hi<<4 | lo)
		text = text[2:]
		if i < 15 && i%2 == 1 && len(text) > 0 && text[0] == '-' {
			text = text[1:]
		}
	}
	if braced {
		if len(text) == 0 || text[0] != '}' {
			return Value{}, uuidSyntax(original)
		}
		text = text[1:]
	}
	if text != "" {
		return Value{}, uuidSyntax(original)
	}
	return UUIDValue(d, bytes)
}

func uuidSyntax(text string) error {
	return failure("22P02", fmt.Sprintf("invalid input syntax for type uuid: \"%s\"", text))
}

func uuidOutput(bytes [16]byte) string {
	const hex = "0123456789abcdef"
	var out [36]byte
	j := 0
	for i, b := range bytes {
		if i == 4 || i == 6 || i == 8 || i == 10 {
			out[j] = '-'
			j++
		}
		out[j], out[j+1] = hex[b>>4], hex[b&15]
		j += 2
	}
	return string(out[:])
}
