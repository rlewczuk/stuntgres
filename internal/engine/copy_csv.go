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
func (r *copyLineReader) readCSVLine() (present bool, err error) {
	if r.ended {
		return false, nil
	}
	r.line = r.line[:0]
	r.lineValid = false
	r.number++
	quoted, lastEscape := false, false
	distinctEscape := r.options.escape != r.options.quote
	for {
		b, readErr := r.reader.ReadByte()
		if readErr != nil {
			if readErr != io.EOF {
				return false, readErr
			}
			r.ended = true
			present = len(r.line) > 0
			break
		}
		// Framing follows raw-byte parity, not decoder pair consumption.
		// Equal QUOTE/ESCAPE disables special escape handling: every quote
		// toggles before separator detection, including accepted CR/LF quotes.
		if quoted && distinctEscape && b == r.options.escape {
			lastEscape = !lastEscape
		}
		if b == r.options.quote && !lastEscape {
			quoted = !quoted
		}
		if !distinctEscape || b != r.options.escape {
			lastEscape = false
		}
		if quoted {
			// PostgreSQL's released history increments quoted LF only after LF is
			// established; otherwise CR counts (including the initial unknown style).
			if b == '\n' && r.eol == 'n' || b == '\r' && r.eol != 'n' {
				r.number++
			}
		} else if b == '\r' || b == '\n' {
			style := byte('n')
			if b == '\r' {
				style = 'r'
				// Established CR ends the record immediately. A following LF
				// belongs to the next record (after this row's type conversion).
				if r.eol == 0 || r.eol == 'c' {
					peek, peekErr := r.reader.Peek(1)
					if peekErr == nil && peek[0] == '\n' {
						r.reader.ReadByte()
						style = 'c'
					} else if peekErr != nil && peekErr != io.EOF {
						return false, peekErr
					}
				}
			}
			if r.eol != 0 && r.eol != style {
				if b == '\r' {
					return false, r.lineError("unquoted carriage return found in data", "Use quoted CSV field to represent carriage return.")
				}
				return false, r.lineError("unquoted newline found in data", "Use quoted CSV field to represent newline.")
			}
			r.eol = style
			present = true
			break
		}
		r.line = append(r.line, b)
	}
	if err = sqltype.ValidateTextInput(string(r.line)); err != nil {
		return false, err
	}
	r.lineValid = true
	return present, nil
}

func (r *copyLineReader) splitCSV(defaultMarkers bool) error {
	r.fields = r.fields[:0]
	r.decoded = r.decoded[:0]
	start, begin := 0, 0
	quoted, sawQuote := false, false
	for i := 0; i <= len(r.line); i++ {
		if i < len(r.line) {
			b := r.line[i]
			if quoted {
				if b == r.options.escape && i+1 < len(r.line) && (r.line[i+1] == r.options.escape || r.line[i+1] == r.options.quote) {
					i++
					r.decoded = append(r.decoded, r.line[i])
					continue
				}
				if b == r.options.quote {
					quoted = false
					continue
				}
				r.decoded = append(r.decoded, b)
				continue
			}
			if b == r.options.quote {
				quoted = true
				sawQuote = true
				continue
			}
			if b != r.options.delimiter {
				r.decoded = append(r.decoded, b)
				continue
			}
		}
		if quoted {
			return r.lineError("unterminated CSV quoted field", "")
		}
		raw := r.line[start:i]
		isNull := !sawQuote && string(raw) == r.options.null
		isDefault := defaultMarkers && r.options.hasDefault && string(raw) == r.options.def
		if isDefault && len(r.fields) < len(r.targets) {
			if target := r.targets[len(r.fields)]; !target.hasDefault {
				d := r.lineError("unexpected default marker in COPY data", "").(*sqlstate.Error)
				d.Detail = fmt.Sprintf("Column \"%s\" has no default value.", target.name)
				return d
			}
		}
		r.fields = append(r.fields, copyField{start: begin, end: len(r.decoded), quoted: sawQuote, isNull: isNull, isDefault: isDefault})
		start = i + 1
		begin = len(r.decoded)
		sawQuote = false
	}
	for i := range r.fields {
		field := &r.fields[i]
		field.decoded = r.decoded[field.start:field.end]
	}
	if len(r.targets) == 0 && len(r.line) == 0 {
		r.fields = r.fields[:0]
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
