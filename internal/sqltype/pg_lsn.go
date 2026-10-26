package sqltype

import "fmt"

// PGLSNValue stores unsigned WAL positions inline, preserving all 64 bits.
func PGLSNValue(d *Descriptor, n uint64) (Value, error) {
	if d == nil || d.id != PgLSN {
		return Value{}, fmt.Errorf("invalid pg_lsn descriptor")
	}
	return Value{typ: d, integer: int64(n)}, nil
}

func (v Value) PGLSN() (uint64, error) {
	if v.null || v.typ == nil || v.typ.id != PgLSN {
		return 0, fmt.Errorf("not a pg_lsn value")
	}
	return uint64(v.integer), nil
}

func (d *Descriptor) pgLSNInput(text string) (Value, error) {
	var words [2]uint32
	word, digits := 0, 0
	valid := len(text) >= 3 && len(text) <= 17
	for i := 0; valid && i < len(text); i++ {
		c := text[i]
		if c == '/' && word == 0 && digits > 0 {
			word, digits = 1, 0
			continue
		}
		var digit byte
		switch {
		case c >= '0' && c <= '9':
			digit = c - '0'
		case c >= 'a' && c <= 'f':
			digit = c - 'a' + 10
		case c >= 'A' && c <= 'F':
			digit = c - 'A' + 10
		default:
			valid = false
		}
		digits++
		if digits > 8 {
			valid = false
		}
		words[word] = words[word]<<4 | uint32(digit)
	}
	if !valid || word != 1 || digits == 0 {
		return Value{}, failure("22P02", "invalid input syntax for type pg_lsn: \""+text+"\"")
	}
	return PGLSNValue(d, uint64(words[0])<<32|uint64(words[1]))
}

func pgLSNOutput(n uint64) string {
	const hex = "0123456789ABCDEF"
	var buf [17]byte
	pos := len(buf)
	for word := range 2 {
		x := uint32(n)
		for {
			pos--
			buf[pos] = hex[x&15]
			x >>= 4
			if x == 0 {
				break
			}
		}
		if word == 0 {
			pos--
			buf[pos] = '/'
			n >>= 32
		}
	}
	return string(buf[pos:])
}
