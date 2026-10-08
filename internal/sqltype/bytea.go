package sqltype

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Binary owns an immutable raw-octet string, without textual validation or copying.
func Binary(d *Descriptor, raw string) (Value, error) {
	if d == nil || d.id != Bytea {
		return Value{}, fmt.Errorf("invalid binary descriptor")
	}
	return Value{typ: d, text: raw}, nil
}
func (v Value) Binary() (string, error) {
	if v.typ == nil || v.typ.id != Bytea || v.null {
		return "", fmt.Errorf("not a binary value")
	}
	return v.text, nil
}

type ByteaOutput uint8

const (
	ByteaHex ByteaOutput = iota
	ByteaEscape
)

// RegclassFormatter is supplied only for execution-owned string output. The
// concrete datum remains an OID; catalog presentation belongs to its statement.
type RegclassFormatter interface {
	FormatRegclass(uint32) (string, error)
}

type OutputEnvironment struct {
	ByteaOutput   ByteaOutput
	Zone          *Zone
	DateStyle     DateStyle
	IntervalStyle IntervalStyle
	Regclass      RegclassFormatter
}

func hexNibble(text string) (byte, error) {
	c := text[0]
	switch {
	case c >= '0' && c <= '9':
		return c - '0', nil
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, nil
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, nil
	}
	// Input validation guarantees UTF8; report the complete offending character.
	_, size := utf8.DecodeRuneInString(text)
	return 0, failure("22023", fmt.Sprintf("invalid hexadecimal digit: \"%s\"", text[:size]))
}
func (d *Descriptor) byteaInput(text string) (Value, error) {
	var b strings.Builder
	b.Grow(len(text))
	if strings.HasPrefix(text, `\x`) {
		for i := 2; i < len(text); {
			if text[i] == ' ' || text[i] == '\t' || text[i] == '\n' || text[i] == '\r' {
				i++
				continue
			}
			hi, err := hexNibble(text[i:])
			if err != nil {
				return Value{}, err
			}
			i++
			if i == len(text) {
				return Value{}, failure("22023", "invalid hexadecimal data: odd number of digits")
			}
			lo, err := hexNibble(text[i:])
			if err != nil {
				return Value{}, err
			}
			i++
			b.WriteByte(hi<<4 | lo)
		}
	} else {
		for i := 0; i < len(text); i++ {
			if text[i] != '\\' {
				b.WriteByte(text[i])
				continue
			}
			if i+1 < len(text) && text[i+1] == '\\' {
				b.WriteByte('\\')
				i++
				continue
			}
			if i+3 >= len(text) || text[i+1] < '0' || text[i+1] > '3' || text[i+2] < '0' || text[i+2] > '7' || text[i+3] < '0' || text[i+3] > '7' {
				return Value{}, failure("22P02", "invalid input syntax for type bytea")
			}
			b.WriteByte((text[i+1]-'0')<<6 | (text[i+2]-'0')<<3 | (text[i+3] - '0'))
			i += 3
		}
	}
	return Binary(d, b.String())
}
func byteaHex(raw string, quoted bool) string {
	const digits = "0123456789abcdef"
	var b strings.Builder
	size := 2 + len(raw)*2
	if quoted {
		size += 2
	}
	b.Grow(size)
	if quoted {
		b.WriteByte('\'')
	}
	b.WriteString(`\x`)
	for i := range len(raw) {
		b.WriteByte(digits[raw[i]>>4])
		b.WriteByte(digits[raw[i]&15])
	}
	if quoted {
		b.WriteByte('\'')
	}
	return b.String()
}
func byteaOutput(raw string, mode ByteaOutput) (string, error) {
	if mode == ByteaHex {
		return byteaHex(raw, false), nil
	}
	if mode != ByteaEscape {
		return "", fmt.Errorf("invalid bytea output mode")
	}
	size := 0
	for i := range len(raw) {
		c := raw[i]
		switch {
		case c == '\\':
			size += 2
		case c < 32 || c > 126:
			size += 4
		default:
			size++
		}
	}
	var b strings.Builder
	b.Grow(size)
	for i := range len(raw) {
		c := raw[i]
		switch {
		case c == '\\':
			b.WriteString(`\\`)
		case c < 32 || c > 126:
			b.WriteByte('\\')
			b.WriteByte('0' + (c >> 6))
			b.WriteByte('0' + ((c >> 3) & 7))
			b.WriteByte('0' + (c & 7))
		default:
			b.WriteByte(c)
		}
	}
	return b.String(), nil
}
