package copyfmt

import (
	"fmt"
	"io"
	"strings"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// A record spans arbitrary transport chunks. The raw record is kept for marker
// classification and diagnostics; no backslash decoding or legacy terminator.
func (r *LineReader) readCSVLine() (present bool, err error) {
	if r.ended {
		return false, nil
	}
	r.line = r.line[:0]
	r.lineValid = false
	r.number++
	quoted, lastEscape := false, false
	distinctEscape := r.options.Escape != r.options.Quote
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
		if quoted && distinctEscape && b == r.options.Escape {
			lastEscape = !lastEscape
		}
		if b == r.options.Quote && !lastEscape {
			quoted = !quoted
		}
		if !distinctEscape || b != r.options.Escape {
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
					return false, LineError("unquoted carriage return found in data", "Use quoted CSV field to represent carriage return.")
				}
				return false, LineError("unquoted newline found in data", "Use quoted CSV field to represent newline.")
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

func (r *LineReader) splitCSV(defaultMarkers bool) error {
	r.fields = r.fields[:0]
	r.decoded = r.decoded[:0]
	start, begin := 0, 0
	quoted, sawQuote := false, false
	for i := 0; i <= len(r.line); i++ {
		if i < len(r.line) {
			b := r.line[i]
			if quoted {
				if b == r.options.Escape && i+1 < len(r.line) && (r.line[i+1] == r.options.Escape || r.line[i+1] == r.options.Quote) {
					i++
					r.decoded = append(r.decoded, r.line[i])
					continue
				}
				if b == r.options.Quote {
					quoted = false
					continue
				}
				r.decoded = append(r.decoded, b)
				continue
			}
			if b == r.options.Quote {
				quoted = true
				sawQuote = true
				continue
			}
			if b != r.options.Delimiter {
				r.decoded = append(r.decoded, b)
				continue
			}
		}
		if quoted {
			return LineError("unterminated CSV quoted field", "")
		}
		raw := r.line[start:i]
		isNull := !sawQuote && string(raw) == r.options.Null
		isDefault := defaultMarkers && r.options.HasDefault && string(raw) == r.options.Default
		if isDefault && len(r.fields) < len(r.targets) {
			if target := r.targets[len(r.fields)]; !target.HasDefault {
				d := LineError("unexpected default marker in COPY data", "").(*sqlstate.Error)
				d.Detail = fmt.Sprintf("Column \"%s\" has no default value.", target.Name)
				return d
			}
		}
		r.fields = append(r.fields, Field{start: begin, end: len(r.decoded), quoted: sawQuote, isNull: isNull, Default: isDefault})
		start = i + 1
		begin = len(r.decoded)
		sawQuote = false
	}
	for i := range r.fields {
		field := &r.fields[i]
		field.Decoded = r.decoded[field.start:field.end]
	}
	if len(r.targets) == 0 && len(r.line) == 0 {
		r.fields = r.fields[:0]
	}
	return nil
}

// AppendCSV appends text as a CSV field, quoting it when forced, when it
// equals the NULL string, when it is a lone \. (single-column output) or
// when it contains the delimiter, quote or a line break.
func AppendCSV(dst []byte, text string, o Options, force, single bool) []byte {
	quote := force || text == o.Null || single && text == `\.` || strings.IndexByte(text, o.Delimiter) >= 0 || strings.IndexByte(text, o.Quote) >= 0 || strings.ContainsAny(text, "\r\n")
	if !quote {
		return append(dst, text...)
	}
	dst = append(dst, o.Quote)
	for i := range len(text) {
		b := text[i]
		if b == o.Quote || b == o.Escape {
			dst = append(dst, o.Escape)
		}
		dst = append(dst, b)
	}
	return append(dst, o.Quote)
}
