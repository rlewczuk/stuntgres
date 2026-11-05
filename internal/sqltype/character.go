package sqltype

import (
	"context"
	"fmt"
	"math"
	"strings"
	"unicode/utf8"
)

const maximumCharacterLength int32 = 10485760

func IsCharacter(id ID) bool { return id == Text || id == BPChar || id == VarChar }

func sizeCharacter(v Value, typmod int32, explicit bool) (Value, error) {
	if typmod < 0 || v.typ.id == Text || v.null {
		return v, nil
	}
	limit := int(typmod - 4)
	count, boundary := 0, len(v.text)
	for offset := range v.text {
		if count == limit {
			boundary = offset
			break
		}
		count++
	}
	if boundary < len(v.text) {
		if !explicit {
			for i := boundary; i < len(v.text); i++ {
				if v.text[i] != ' ' {
					name := "character"
					if v.typ.id == VarChar {
						name = "character varying"
					}
					return Value{}, failure("22001", fmt.Sprintf("value too long for type %s(%d)", name, limit))
				}
			}
		}
		v.text = v.text[:boundary]
	} else if v.typ.id == BPChar && count < limit {
		var padded strings.Builder
		padded.Grow(len(v.text) + limit - count)
		padded.WriteString(v.text)
		for range limit - count {
			padded.WriteByte(' ')
		}
		v.text = padded.String()
	}
	return v, nil
}

func (u Usage) Length() (int64, bool) {
	u = u.BaseUsage()
	if u.Type != nil && isBitString(u.Type.id) {
		return int64(u.Typmod), true
	}
	if u.Type == nil || !IsCharacter(u.Type.id) {
		return 0, false
	}
	if u.Typmod < 0 {
		return math.MaxInt64, true
	}
	return int64(u.Typmod - 4), true
}

// Report the first invalid database-encoding sequence, with PostgreSQL's
// bounded byte window (including an invalid continuation or truncated tail).
func ValidateTextInput(text string) error {
	for i := 0; i < len(text); {
		if text[i] == 0 {
			return failure("22021", `invalid byte sequence for encoding "UTF8": 0x00`)
		}
		_, width := utf8.DecodeRuneInString(text[i:])
		if width == 1 && text[i] >= utf8.RuneSelf {
			return invalidTextSequence([]byte(text[i:min(i+4, len(text))]))
		}
		i += width
	}
	return nil
}

// validateTextBytes shares the public encoding diagnostic without converting
// invocation-owned decrypted bytes into an immutable string before validation.
func validateTextBytes(ctx context.Context, data []byte) error {
	for i := 0; i < len(data); {
		if i&4095 < 4 {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		if data[i] == 0 {
			return failure("22021", `invalid byte sequence for encoding "UTF8": 0x00`)
		}
		_, width := utf8.DecodeRune(data[i:])
		if width == 1 && data[i] >= utf8.RuneSelf {
			return invalidTextSequence(data[i:])
		}
		i += width
	}
	return ctx.Err()
}

func invalidTextSequence(data []byte) error {
	n := 1
	switch {
	case data[0] >= 0xc0 && data[0] < 0xe0:
		n = 2
	case data[0] >= 0xe0 && data[0] < 0xf0:
		n = 3
	case data[0] >= 0xf0 && data[0] < 0xf8:
		n = 4
	}
	n = min(n, len(data))
	var b strings.Builder
	b.WriteString(`invalid byte sequence for encoding "UTF8":`)
	for j := range n {
		fmt.Fprintf(&b, " 0x%02x", data[j])
	}
	return failure("22021", b.String())
}
