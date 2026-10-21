package copyfmt

import (
	"bufio"
	"fmt"
	"io"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"unicode/utf8"
)

// Field is one split field of the current record. Decoded is valid until
// the next Split; Default marks a DEFAULT marker. Null input is read through
// LineReader.FieldNull, which applies the CSV FORCE flags.
type Field struct {
	Decoded                 []byte
	start, end              int
	quoted, isNull, Default bool
}

// Target describes one COPY input field for header checks and DEFAULT
// markers, so the line codec does not read the statement binding.
type Target struct {
	Name       string
	HasDefault bool // a DEFAULT marker is allowed for this column
}

// LineReader is the text and CSV codec: it frames records, splits and
// decodes fields and checks the header line. It reuses its buffers across
// records and knows nothing of the statement executing the COPY.
type LineReader struct {
	options                      Options
	reader                       *bufio.Reader
	line, decoded                []byte
	fields                       []Field
	targets                      []Target
	number                       int64
	eol                          byte
	ended, headerDone, lineValid bool
}

// NewLineReader reads text or CSV records (by options.Format) from reader.
// targets are built once per statement.
func NewLineReader(options Options, reader *bufio.Reader, targets []Target) LineReader {
	return LineReader{options: options, reader: reader, targets: targets}
}

// Fields returns the fields of the last Split, valid until the next Split.
func (r *LineReader) Fields() []Field { return r.fields }

// Number is the current line number, for the "COPY t, line N" context.
func (r *LineReader) Number() int64 { return r.number }

// Line returns the raw current record, valid until the next ReadLine.
func (r *LineReader) Line() []byte { return r.line }

// LineValid reports whether the current record was read whole and passed
// text-input validation, so it may be quoted in a diagnostic.
func (r *LineReader) LineValid() bool { return r.lineValid }

// LineError is a 22P04 COPY data error with an optional hint.
func LineError(message, hint string) error {
	d := sqlError("22P04", message).(*sqlstate.Error)
	d.Hint = hint
	return d
}

// ReadLine reads the next record. It returns present=false at the end of
// data, which is the end of input or, in text format, the \. marker.
func (r *LineReader) ReadLine() (present bool, err error) {
	if r.options.Format == CSV {
		return r.readCSVLine()
	}
	if r.ended {
		return false, nil
	}
	r.line = r.line[:0]
	r.lineValid = false
	r.number++
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
		if b == '\\' {
			next, nextErr := r.reader.ReadByte()
			if nextErr != nil {
				if nextErr != io.EOF {
					return false, nextErr
				}
				r.ended = true
				present = true
				break
			}
			if next == '.' {
				end, endErr := r.reader.ReadByte()
				if endErr != nil && endErr != io.EOF {
					return false, endErr
				}
				style := byte(0)
				if end == '\n' {
					style = 'n'
				} else if end == '\r' {
					style = 'r'
					if peek, peekErr := r.reader.Peek(1); peekErr == nil && peek[0] == '\n' {
						r.reader.ReadByte()
						style = 'c'
					}
				}
				if len(r.line) != 0 || style == 0 {
					return false, LineError("end-of-copy marker is not alone on its line", "")
				}
				if r.eol != 0 && style != r.eol {
					return false, LineError("end-of-copy marker does not match previous newline style", "")
				}
				r.ended = true
				return false, nil
			}
			r.line = append(r.line, b, next)
			continue
		}
		if b == '\r' || b == '\n' {
			style := byte('n')
			if b == '\r' {
				style = 'r'
				if peek, peekErr := r.reader.Peek(1); peekErr == nil && peek[0] == '\n' {
					r.reader.ReadByte()
					style = 'c'
				} else if peekErr != nil && peekErr != io.EOF {
					return false, peekErr
				}
			}
			if r.eol != 0 && r.eol != style {
				if b == '\r' {
					return false, LineError("literal carriage return found in data", `Use "\r" to represent carriage return.`)
				}
				return false, LineError("literal newline found in data", `Use "\n" to represent newline.`)
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
func hexDigit(b byte) (byte, bool) {
	switch {
	case b >= '0' && b <= '9':
		return b - '0', true
	case b >= 'a' && b <= 'f':
		return b - 'a' + 10, true
	case b >= 'A' && b <= 'F':
		return b - 'A' + 10, true
	}
	return 0, false
}

// decodeField appends raw with text-format backslash escapes decoded.
func decodeField(dst, raw []byte) []byte {
	for i := 0; i < len(raw); i++ {
		b := raw[i]
		if b != '\\' {
			dst = append(dst, b)
			continue
		}
		i++
		if i == len(raw) {
			break
		}
		b = raw[i]
		switch b {
		case 'b':
			b = '\b'
		case 'f':
			b = '\f'
		case 'n':
			b = '\n'
		case 'r':
			b = '\r'
		case 't':
			b = '\t'
		case 'v':
			b = '\v'
		case 'x':
			if i+1 < len(raw) {
				if h, ok := hexDigit(raw[i+1]); ok {
					i++
					b = h
					if i+1 < len(raw) {
						if h, ok := hexDigit(raw[i+1]); ok {
							i++
							b = b*16 + h
						}
					}
				}
			}
		default:
			if b >= '0' && b <= '7' {
				b -= '0'
				for n := 1; n < 3 && i+1 < len(raw) && raw[i+1] >= '0' && raw[i+1] <= '7'; n++ {
					i++
					b = b*8 + raw[i] - '0'
				}
			}
		}
		dst = append(dst, b)
	}
	return dst
}

// Split splits and decodes the current record into Fields. With
// defaultMarkers, a field equal to the DEFAULT string is a DEFAULT marker.
func (r *LineReader) Split(defaultMarkers bool) error {
	if r.options.Format == CSV {
		return r.splitCSV(defaultMarkers)
	}
	r.fields = r.fields[:0]
	r.decoded = r.decoded[:0]
	start := 0
	for i := 0; i <= len(r.line); i++ {
		if i < len(r.line) && r.line[i] == '\\' {
			i++
			continue
		}
		if i < len(r.line) && r.line[i] != r.options.Delimiter {
			continue
		}
		raw := r.line[start:i]
		begin := len(r.decoded)
		isNull := string(raw) == r.options.Null
		isDefault := defaultMarkers && r.options.HasDefault && string(raw) == r.options.Default
		if isDefault && len(r.fields) < len(r.targets) {
			if target := r.targets[len(r.fields)]; !target.HasDefault {
				d := LineError("unexpected default marker in COPY data", "").(*sqlstate.Error)
				d.Detail = fmt.Sprintf("Column \"%s\" has no default value.", target.Name)
				return d
			}
		}
		if !isNull && !isDefault {
			r.decoded = decodeField(r.decoded, raw)
		}
		// Offsets rather than slices survive growth of the reusable decode buffer.
		r.fields = append(r.fields, Field{start: begin, end: len(r.decoded), isNull: isNull, Default: isDefault})
		start = i + 1
	}
	for i := range r.fields {
		field := &r.fields[i]
		field.Decoded = r.decoded[field.start:field.end]
		if err := sqltype.ValidateTextInput(string(r.fields[i].Decoded)); err != nil {
			return err
		}
	}
	if len(r.targets) == 0 && len(r.line) == 0 {
		r.fields = r.fields[:0]
	}
	return nil
}

// Header consumes the HEADER line once, before the first record, and checks
// it against the target names under HEADER MATCH.
func (r *LineReader) Header() error {
	if r.headerDone {
		return nil
	}
	r.headerDone = true
	if r.options.Header == 0 {
		return nil
	}
	_, err := r.ReadLine()
	if err != nil {
		return err
	}
	if r.options.Header != 2 {
		return nil
	}
	if err = r.Split(false); err != nil {
		return err
	}
	if len(r.fields) != len(r.targets) {
		return LineError(fmt.Sprintf("wrong number of fields in header line: got %d, expected %d", len(r.fields), len(r.targets)), "")
	}
	for i, field := range r.fields {
		name := r.targets[i].Name
		if field.isNull {
			return LineError(fmt.Sprintf("column name mismatch in header line field %d: got null value (\"%s\"), expected \"%s\"", i+1, r.options.Null, name), "")
		}
		if string(field.Decoded) != name {
			return LineError(fmt.Sprintf("column name mismatch in header line field %d: got \"%s\", expected \"%s\"", i+1, string(field.Decoded), name), "")
		}
	}
	return nil
}

// FieldNull applies the CSV FORCE_NOT_NULL and FORCE_NULL flags to field i.
func (r *LineReader) FieldNull(i int) bool {
	field := r.fields[i]
	if r.options.Format != CSV {
		return field.isNull
	}
	if field.isNull {
		return !Forced(r.options.NotNullFields, i)
	}
	return field.quoted && Forced(r.options.NullFields, i) && string(field.Decoded) == r.options.Null
}

// ContextValue truncates a value quoted in a COPY diagnostic context.
func ContextValue(text string) string {
	if len(text) <= 100 {
		return text
	}
	end := 100
	for end > 0 && !utf8.RuneStart(text[end]) {
		end--
	}
	return text[:end] + "..."
}

// AppendText appends text in COPY text format, escaping the delimiter.
func AppendText(dst []byte, text string, delimiter byte) []byte {
	for i := range len(text) {
		b := text[i]
		switch b {
		case '\\':
			dst = append(dst, '\\', '\\')
		case '\b':
			dst = append(dst, '\\', 'b')
		case '\f':
			dst = append(dst, '\\', 'f')
		case '\n':
			dst = append(dst, '\\', 'n')
		case '\r':
			dst = append(dst, '\\', 'r')
		case '\t':
			dst = append(dst, '\\', 't')
		case '\v':
			dst = append(dst, '\\', 'v')
		default:
			if b == delimiter {
				dst = append(dst, '\\')
			}
			dst = append(dst, b)
		}
	}
	return dst
}
