package sqltype

import (
	"context"
	"strings"

	"stuntgres/internal/sqlstate"
)

const armorAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
const armorBegin = "-----BEGIN PGP MESSAGE-----\n"
const armorEnd = "\n-----END PGP MESSAGE-----\n"

func corruptArmor() error {
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "39000", Message: "Corrupt ascii-armor"}
}
func armorError(code, message string) error {
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message}
}

func armorCRC(ctx context.Context, data string) (uint32, error) {
	crc := uint32(0xb704ce)
	for i := range len(data) {
		if i&4095 == 0 {
			if err := ctx.Err(); err != nil {
				return 0, err
			}
		}
		crc ^= uint32(data[i]) << 16
		for range 8 {
			crc <<= 1
			if crc&0x1000000 != 0 {
				crc ^= 0x1864cfb
			}
		}
	}
	return crc & 0xffffff, ctx.Err()
}

func armorHeader(ctx context.Context, value Value, key bool) (string, error) {
	kind := "value"
	if key {
		kind = "key"
	}
	if value.IsNull() {
		return "", armorError("22004", "null value not allowed for header "+kind)
	}
	s, err := value.Text()
	if err != nil {
		return "", err
	}
	for i := range len(s) {
		if i&4095 == 0 {
			if err := ctx.Err(); err != nil {
				return "", err
			}
		}
		if s[i] >= 128 {
			return "", armorError("22023", "header "+kind+" must not contain non-ASCII characters")
		}
	}
	if key && strings.Contains(s, ": ") {
		return "", armorError("22023", "header key must not contain \": \"")
	}
	if strings.Contains(s, "\n") {
		return "", armorError("22023", "header "+kind+" must not contain newlines")
	}
	return s, nil
}

func (p PgcryptoPlan) evaluateArmor(ctx context.Context, values []Value) (Value, error) {
	data, err := values[0].Binary()
	if err != nil {
		return Value{}, err
	}
	var keys, vals Value
	size := len(armorBegin) + len(armorEnd) + 6 + (len(data)+2)/3*4 + (len(data)+56)/57
	if len(values) == 3 {
		keys, vals = values[1], values[2]
		if keys.ArrayRank() > 1 || keys.ArrayRank() != vals.ArrayRank() {
			return Value{}, armorError("2202E", "wrong number of array subscripts")
		}
		if keys.ArrayLen() != vals.ArrayLen() {
			return Value{}, armorError("2202E", "mismatched array dimensions")
		}
		for i := range keys.ArrayLen() {
			if err := ctx.Err(); err != nil {
				return Value{}, err
			}
			key, err := armorHeader(ctx, keys.ArrayElement(i), true)
			if err != nil {
				return Value{}, err
			}
			value, err := armorHeader(ctx, vals.ArrayElement(i), false)
			if err != nil {
				return Value{}, err
			}
			size += len(key) + len(value) + 3
		}
	}
	crc, err := armorCRC(ctx, data)
	if err != nil {
		return Value{}, err
	}
	var out strings.Builder
	out.Grow(size)
	out.WriteString(armorBegin)
	for i := range keys.ArrayLen() {
		key, _ := keys.ArrayElement(i).Text()
		value, _ := vals.ArrayElement(i).Text()
		out.WriteString(key)
		out.WriteString(": ")
		out.WriteString(value)
		out.WriteByte('\n')
	}
	out.WriteByte('\n')
	column := 0
	for i := 0; i < len(data); i += 3 {
		if i%4095 == 0 {
			if err := ctx.Err(); err != nil {
				return Value{}, err
			}
		}
		word := uint32(data[i]) << 16
		if i+1 < len(data) {
			word |= uint32(data[i+1]) << 8
		}
		if i+2 < len(data) {
			word |= uint32(data[i+2])
		}
		out.WriteByte(armorAlphabet[word>>18])
		out.WriteByte(armorAlphabet[word>>12&63])
		if i+1 < len(data) {
			out.WriteByte(armorAlphabet[word>>6&63])
		} else {
			out.WriteByte('=')
		}
		if i+2 < len(data) {
			out.WriteByte(armorAlphabet[word&63])
		} else {
			out.WriteByte('=')
		}
		column += 4
		if column == 76 {
			out.WriteByte('\n')
			column = 0
		}
	}
	if column != 0 {
		out.WriteByte('\n')
	}
	out.WriteByte('=')
	out.WriteByte(armorAlphabet[crc>>18])
	out.WriteByte(armorAlphabet[crc>>12&63])
	out.WriteByte(armorAlphabet[crc>>6&63])
	out.WriteByte(armorAlphabet[crc&63])
	out.WriteString(armorEnd)
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	return String(p.result.Type, out.String()), nil
}

// armorMarker implements the released line-start marker grammar, including
// generic labels and the intentionally independent BEGIN and END labels.
func armorMarker(ctx context.Context, text string, from int, end bool) (int, int, error) {
	if err := ctx.Err(); err != nil {
		return 0, 0, err
	}
	marker := "-----BEGIN"
	if end {
		marker = "-----END"
	}
	for i := from; i < len(text); i++ {
		if i&4095 == 0 {
			if err := ctx.Err(); err != nil {
				return 0, 0, err
			}
		}
		if i != from && text[i-1] != '\n' || !strings.HasPrefix(text[i:], marker) {
			continue
		}
		p := i + len(marker)
		for p < len(text) && text[p] != '-' {
			if p&4095 == 0 {
				if err := ctx.Err(); err != nil {
					return 0, 0, err
				}
			}
			if text[p] < ' ' {
				return 0, 0, corruptArmor()
			}
			p++
		}
		if !strings.HasPrefix(text[p:], "-----") {
			return 0, 0, corruptArmor()
		}
		p += 5
		if p < len(text) {
			if text[p] != '\r' && text[p] != '\n' {
				return 0, 0, corruptArmor()
			}
			if text[p] == '\r' {
				p++
			}
			if p < len(text) && text[p] == '\n' {
				p++
			}
		}
		return i, p, nil
	}
	return 0, 0, corruptArmor()
}

func armorRegion(ctx context.Context, text string) (int, int, int, error) {
	_, start, err := armorMarker(ctx, text, 0, false)
	if err != nil {
		return 0, 0, 0, err
	}
	footer, _, err := armorMarker(ctx, text, start, true)
	if err != nil {
		return 0, 0, 0, err
	}
	p := start
	for p < footer && text[p] != '\n' && text[p] != '\r' {
		for p < footer && text[p] != '\n' {
			if p&4095 == 0 {
				if err := ctx.Err(); err != nil {
					return 0, 0, 0, err
				}
			}
			p++
		}
		if p == footer {
			return 0, 0, 0, corruptArmor()
		}
		p++
	}
	return start, p, footer, nil
}

// PgcryptoArmorHeaderRegion validates every header before a source may emit a
// row. The returned bounds borrow the immutable input; body and CRC are ignored.
func PgcryptoArmorHeaderRegion(ctx context.Context, text string) (int, int, error) {
	start, stop, _, err := armorRegion(ctx, text)
	if err != nil {
		return 0, 0, err
	}
	for p := start; p < stop; {
		if err := ctx.Err(); err != nil {
			return 0, 0, err
		}
		n := strings.IndexByte(text[p:stop], '\n')
		if n < 0 {
			return 0, 0, corruptArmor()
		}
		line := text[p : p+n]
		line = strings.TrimSuffix(line, "\r")
		if !strings.Contains(line, ": ") {
			return 0, 0, corruptArmor()
		}
		p += n + 1
	}
	return start, stop, nil
}

// armorDecode preserves PostgreSQL's historical padding state. Exactly one
// output is supplied: the owned body builder or the three-byte checksum buffer.
func armorDecode(ctx context.Context, text string, body *strings.Builder, checksum *[3]byte) (int, error) {
	var word uint32
	pos, end, count := 0, 0, 0
	for i := range len(text) {
		if i&4095 == 0 {
			if err := ctx.Err(); err != nil {
				return 0, err
			}
		}
		c, b := text[i], uint32(0)
		switch {
		case c >= 'A' && c <= 'Z':
			b = uint32(c - 'A')
		case c >= 'a' && c <= 'z':
			b = uint32(c - 'a' + 26)
		case c >= '0' && c <= '9':
			b = uint32(c - '0' + 52)
		case c == '+':
			b = 62
		case c == '/':
			b = 63
		case c == '=':
			if end == 0 {
				if pos == 2 {
					end = 1
				} else if pos == 3 {
					end = 2
				} else {
					return 0, corruptArmor()
				}
			}
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			continue
		default:
			return 0, corruptArmor()
		}
		word = word<<6 | b
		pos++
		if pos == 4 {
			size := 3
			if end != 0 {
				size = end
			}
			bytes := [3]byte{byte(word >> 16), byte(word >> 8), byte(word)}
			if body != nil {
				body.Write(bytes[:size])
			} else {
				if count+size > len(checksum) {
					return 0, corruptArmor()
				}
				copy(checksum[count:], bytes[:size])
			}
			count += size
			word, pos = 0, 0
		}
	}
	if pos != 0 {
		return 0, corruptArmor()
	}
	return count, ctx.Err()
}

func PgcryptoDearmor(ctx context.Context, text string) (string, error) {
	_, start, footer, err := armorRegion(ctx, text)
	if err != nil {
		return "", err
	}
	crcAt := footer
	for crcAt >= start && text[crcAt] != '=' {
		if crcAt&4095 == 0 {
			if err := ctx.Err(); err != nil {
				return "", err
			}
		}
		crcAt--
	}
	if crcAt <= start || crcAt+5 > len(text) {
		return "", corruptArmor()
	}
	var checksum [3]byte
	n, err := armorDecode(ctx, text[crcAt+1:crcAt+5], nil, &checksum)
	if err != nil {
		return "", err
	}
	if n != 3 {
		return "", corruptArmor()
	}
	var body strings.Builder
	body.Grow((crcAt - start) * 3 / 4)
	_, err = armorDecode(ctx, text[start:crcAt-1], &body, nil)
	if err != nil {
		return "", err
	}
	result := body.String()
	crc, err := armorCRC(ctx, result)
	if err != nil {
		return "", err
	}
	if crc != uint32(checksum[0])<<16|uint32(checksum[1])<<8|uint32(checksum[2]) {
		return "", corruptArmor()
	}
	return result, nil
}
