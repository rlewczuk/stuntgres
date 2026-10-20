package engine

import (
	"fmt"
	"io"
	"strings"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// A record spans arbitrary transport chunks. The raw record is kept for marker
// classification and diagnostics; no backslash decoding or legacy terminator.
func (c *copyCursor) readCSVLine() (present bool, err error) {
	if c.ended {
		return false, nil
	}
	c.line = c.line[:0]
	c.lineValid = false
	c.column = ""
	c.number++
	quoted, lastEscape := false, false
	distinctEscape := c.options.escape != c.options.quote
	for {
		b, readErr := c.reader.ReadByte()
		if readErr != nil {
			if readErr != io.EOF {
				return false, readErr
			}
			c.ended = true
			present = len(c.line) > 0
			break
		}
		// Framing follows raw-byte parity, not decoder pair consumption.
		// Equal QUOTE/ESCAPE disables special escape handling: every quote
		// toggles before separator detection, including accepted CR/LF quotes.
		if quoted && distinctEscape && b == c.options.escape {
			lastEscape = !lastEscape
		}
		if b == c.options.quote && !lastEscape {
			quoted = !quoted
		}
		if !distinctEscape || b != c.options.escape {
			lastEscape = false
		}
		if quoted {
			// PostgreSQL's released history increments quoted LF only after LF is
			// established; otherwise CR counts (including the initial unknown style).
			if b == '\n' && c.eol == 'n' || b == '\r' && c.eol != 'n' {
				c.number++
			}
		} else if b == '\r' || b == '\n' {
			style := byte('n')
			if b == '\r' {
				style = 'r'
				// Established CR ends the record immediately. A following LF
				// belongs to the next record (after this row's type conversion).
				if c.eol == 0 || c.eol == 'c' {
					peek, peekErr := c.reader.Peek(1)
					if peekErr == nil && peek[0] == '\n' {
						c.reader.ReadByte()
						style = 'c'
					} else if peekErr != nil && peekErr != io.EOF {
						return false, peekErr
					}
				}
			}
			if c.eol != 0 && c.eol != style {
				if b == '\r' {
					return false, c.lineError("unquoted carriage return found in data", "Use quoted CSV field to represent carriage return.")
				}
				return false, c.lineError("unquoted newline found in data", "Use quoted CSV field to represent newline.")
			}
			c.eol = style
			present = true
			break
		}
		c.line = append(c.line, b)
	}
	if err = sqltype.ValidateTextInput(string(c.line)); err != nil {
		return false, err
	}
	c.lineValid = true
	return present, nil
}

func (c *copyCursor) splitCSV(defaultMarkers bool) error {
	c.fields = c.fields[:0]
	c.decoded = c.decoded[:0]
	start, begin := 0, 0
	quoted, sawQuote := false, false
	for i := 0; i <= len(c.line); i++ {
		if i < len(c.line) {
			b := c.line[i]
			if quoted {
				if b == c.options.escape && i+1 < len(c.line) && (c.line[i+1] == c.options.escape || c.line[i+1] == c.options.quote) {
					i++
					c.decoded = append(c.decoded, c.line[i])
					continue
				}
				if b == c.options.quote {
					quoted = false
					continue
				}
				c.decoded = append(c.decoded, b)
				continue
			}
			if b == c.options.quote {
				quoted = true
				sawQuote = true
				continue
			}
			if b != c.options.delimiter {
				c.decoded = append(c.decoded, b)
				continue
			}
		}
		if quoted {
			return c.lineError("unterminated CSV quoted field", "")
		}
		raw := c.line[start:i]
		isNull := !sawQuote && string(raw) == c.options.null
		isDefault := defaultMarkers && c.options.hasDefault && string(raw) == c.options.def
		if isDefault && len(c.fields) < len(c.binding.Targets) {
			target := c.binding.Targets[len(c.fields)]
			if len(c.binding.Insert.Defaults) == 0 || c.binding.Insert.Defaults[target] == nil {
				d := c.lineError("unexpected default marker in COPY data", "").(*sqlstate.Error)
				d.Detail = fmt.Sprintf("Column \"%s\" has no default value.", c.binding.Relation.Columns[target].Name)
				return d
			}
		}
		c.fields = append(c.fields, copyField{start: begin, end: len(c.decoded), quoted: sawQuote, isNull: isNull, isDefault: isDefault})
		start = i + 1
		begin = len(c.decoded)
		sawQuote = false
	}
	for i := range c.fields {
		field := &c.fields[i]
		field.decoded = c.decoded[field.start:field.end]
	}
	if len(c.binding.Targets) == 0 && len(c.line) == 0 {
		c.fields = c.fields[:0]
	}
	return nil
}

func appendCopyCSV(dst []byte, text string, o copyOptions, force, single bool) []byte {
	quote := force || text == o.null || single && text == `\.` || strings.IndexByte(text, o.delimiter) >= 0 || strings.IndexByte(text, o.quote) >= 0 || strings.ContainsAny(text, "\r\n")
	if !quote {
		return append(dst, text...)
	}
	dst = append(dst, o.quote)
	for i := range len(text) {
		b := text[i]
		if b == o.quote || b == o.escape {
			dst = append(dst, o.escape)
		}
		dst = append(dst, b)
	}
	return append(dst, o.quote)
}
