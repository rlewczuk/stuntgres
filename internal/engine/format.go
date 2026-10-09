package engine

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

const formatHint = `For a single "%" use "%%".`

func formatSpecifierError(message string) error {
	d := sqlError("22023", message).(*sqlstate.Error)
	d.Hint = formatHint
	return d
}

func formatDigits(ctx context.Context, pattern string, at int) (int, int, error) {
	n := 0
	for at < len(pattern) && pattern[at] >= '0' && pattern[at] <= '9' {
		if at&255 == 0 {
			if err := ctx.Err(); err != nil {
				return 0, at, err
			}
		}
		digit := int(pattern[at] - '0')
		if n > (2147483647-digit)/10 {
			return 0, at, sqlError("22003", "number is out of range")
		}
		n = n*10 + digit
		at++
	}
	return n, at, nil
}

// formatValues borrows immutable array payloads without flattening or copying.
type formatValues struct {
	ordinary []sqltype.Value
	array    sqltype.Value
}

func (v formatValues) len() int {
	if v.array.Type() != nil {
		return v.array.ArrayLen()
	}
	return len(v.ordinary)
}

func (v formatValues) at(i int) sqltype.Value {
	if v.array.Type() != nil {
		return v.array.ArrayElement(i)
	}
	return v.ordinary[i]
}

// Format grammar is runtime data. Positions are one-based; the cursor tracks
// the last consumed argument, not the highest explicit position seen.
func formatText(ctx context.Context, pattern string, values formatValues, int4 *sqltype.Descriptor, output sqltype.OutputEnvironment) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if int4 == nil || int4.ID() != sqltype.Int4 {
		return "", fmt.Errorf("stuntgres: invalid format width descriptor")
	}
	var out strings.Builder
	next, start, at := 0, 0, 0
	argument := func(position int) (sqltype.Value, error) {
		index := next
		if position > 0 {
			index = position - 1
		}
		if index >= values.len() {
			return sqltype.Value{}, sqlError("22023", "too few arguments for format()")
		}
		next = index + 1
		return values.at(index), nil
	}
	for at < len(pattern) {
		if at&255 == 0 {
			if err := ctx.Err(); err != nil {
				return "", err
			}
		}
		if pattern[at] != '%' {
			at++
			continue
		}
		if err := formatWrite(ctx, &out, pattern[start:at]); err != nil {
			return "", err
		}
		at++
		if at == len(pattern) {
			return "", formatSpecifierError("unterminated format() type specifier")
		}
		if pattern[at] == '%' {
			if err := formatWrite(ctx, &out, "%"); err != nil {
				return "", err
			}
			at++
			start = at
			continue
		}
		valuePosition, widthPosition, width := 0, 0, 0
		left, dynamic, literalWidth := false, false, false
		var err error
		if pattern[at] >= '0' && pattern[at] <= '9' {
			width, at, err = formatDigits(ctx, pattern, at)
			if err != nil {
				return "", err
			}
			if at == len(pattern) {
				return "", formatSpecifierError("unterminated format() type specifier")
			}
			if pattern[at] == '$' {
				valuePosition, width = width, 0
				if valuePosition == 0 {
					return "", sqlError("22023", "format specifies argument 0, but arguments are numbered from 1")
				}
				at++
			} else {
				literalWidth = true
			}
		}
		if !literalWidth {
			for at < len(pattern) && pattern[at] == '-' {
				if at&255 == 0 {
					if err := ctx.Err(); err != nil {
						return "", err
					}
				}
				left = true
				at++
			}
			if at < len(pattern) && pattern[at] == '*' {
				dynamic = true
				at++
				if at < len(pattern) && pattern[at] >= '0' && pattern[at] <= '9' {
					widthPosition, at, err = formatDigits(ctx, pattern, at)
					if err != nil {
						return "", err
					}
					if at == len(pattern) {
						return "", formatSpecifierError("unterminated format() type specifier")
					}
					if pattern[at] != '$' {
						return "", sqlError("22023", `width argument position must be ended by "$"`)
					}
					if widthPosition == 0 {
						return "", sqlError("22023", "format specifies argument 0, but arguments are numbered from 1")
					}
					at++
				}
			} else if at < len(pattern) && pattern[at] >= '0' && pattern[at] <= '9' {
				width, at, err = formatDigits(ctx, pattern, at)
				if err != nil {
					return "", err
				}
			}
		}
		if at == len(pattern) {
			return "", formatSpecifierError("unterminated format() type specifier")
		}
		verb, size := utf8.DecodeRuneInString(pattern[at:])
		if verb != 's' && verb != 'I' && verb != 'L' {
			return "", formatSpecifierError(fmt.Sprintf(`unrecognized format() type specifier "%c"`, verb))
		}
		at += size
		// Validate the verb before width conversion or argument access.
		if dynamic {
			value, err := argument(widthPosition)
			if err != nil {
				return "", err
			}
			width, err = formatWidth(ctx, value, int4, output)
			if err != nil {
				return "", err
			}
		}
		value, err := argument(valuePosition)
		if err != nil {
			return "", err
		}
		if err := formatField(ctx, &out, value, verb, width, left, output); err != nil {
			return "", err
		}
		start = at
	}
	if start == 0 {
		return pattern, ctx.Err()
	}
	if err := formatWrite(ctx, &out, pattern[start:]); err != nil {
		return "", err
	}
	return out.String(), ctx.Err()
}

func formatWidth(ctx context.Context, value sqltype.Value, int4 *sqltype.Descriptor, output sqltype.OutputEnvironment) (int, error) {
	if value.IsNull() {
		return 0, nil
	}
	if value.Type() == nil {
		return 0, fmt.Errorf("stuntgres: untyped format width")
	}
	if value.Type().BaseType().ID() == sqltype.Int2 || value.Type().BaseType().ID() == sqltype.Int4 {
		base, err := value.BaseValue()
		if err != nil {
			return 0, err
		}
		n, err := base.Integer()
		return int(n), err
	}
	text, err := value.Type().OutputWithEnvironment(ctx, value, output)
	if err != nil {
		return 0, err
	}
	converted, err := int4.Input(ctx, text, -1)
	if err != nil {
		return 0, err
	}
	n, err := converted.Integer()
	return int(n), err
}

func formatField(ctx context.Context, out *strings.Builder, value sqltype.Value, verb rune, width int, left bool, output sqltype.OutputEnvironment) error {
	text := ""
	quote, escape := byte(0), false
	if value.IsNull() {
		if verb == 'I' {
			return sqlError("22004", "null values cannot be formatted as an SQL identifier")
		}
		if verb == 'L' {
			text = "NULL"
		}
	} else {
		if value.Type() == nil {
			return fmt.Errorf("stuntgres: untyped format value")
		}
		var err error
		text, err = value.Type().OutputWithEnvironment(ctx, value, output)
		if err != nil {
			return err
		}
		if verb == 'I' {
			quoted, err := parser.IdentifierNeedsQuotesContext(ctx, text)
			if err != nil {
				return err
			}
			if quoted {
				quote = '"'
			}
		} else if verb == 'L' {
			quote = '\''
		}
	}
	// Identifier NULL wins over INT_MIN padding overflow.
	if width == -2147483648 {
		return sqlError("22003", "number is out of range")
	}
	if width < 0 {
		left = true
		width = -width
	}
	characters, extra := 0, 0
	if width != 0 || quote != 0 {
		for i := range len(text) {
			if i&255 == 0 {
				if err := ctx.Err(); err != nil {
					return err
				}
			}
			c := text[i]
			if c&0xc0 != 0x80 {
				characters++
			}
			if quote != 0 && (c == quote || quote == '\'' && c == '\\') {
				extra++
				if c == '\\' {
					escape = true
				}
			}
		}
		characters += extra
	}
	if quote != 0 {
		characters += 2
		extra += 2
	}
	if escape {
		characters++
		extra++
	}
	padding := max(0, width-characters)
	size, err := formatSize(len(text), extra)
	if err != nil {
		return err
	}
	size, err = formatSize(size, padding)
	if err != nil {
		return err
	}
	if _, err := formatSize(out.Len(), size); err != nil {
		return err
	}
	if !left {
		if err := formatPadding(ctx, out, padding); err != nil {
			return err
		}
	}
	if escape {
		out.WriteByte('E')
	}
	if quote == 0 {
		if err := formatWrite(ctx, out, text); err != nil {
			return err
		}
	} else {
		out.WriteByte(quote)
		start := 0
		for i := range len(text) {
			if i&255 == 0 {
				if err := ctx.Err(); err != nil {
					return err
				}
			}
			c := text[i]
			if c == quote || quote == '\'' && c == '\\' {
				if err := formatWrite(ctx, out, text[start:i+1]); err != nil {
					return err
				}
				out.WriteByte(c)
				start = i + 1
			}
		}
		if err := formatWrite(ctx, out, text[start:]); err != nil {
			return err
		}
		out.WriteByte(quote)
	}
	if left {
		return formatPadding(ctx, out, padding)
	}
	return ctx.Err()
}

func formatSize(size, additional int) (int, error) {
	if additional > int(^uint(0)>>1)-size {
		return 0, sqlError("54000", "requested length too large")
	}
	return size + additional, nil
}

func formatWrite(ctx context.Context, out *strings.Builder, text string) error {
	if _, err := formatSize(out.Len(), len(text)); err != nil {
		return err
	}
	for len(text) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		n := min(4096, len(text))
		out.WriteString(text[:n])
		text = text[n:]
	}
	return ctx.Err()
}

func formatPadding(ctx context.Context, out *strings.Builder, n int) error {
	const spaces = "                                                                "
	for n > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		count := min(n, len(spaces))
		out.WriteString(spaces[:count])
		n -= count
	}
	return ctx.Err()
}
