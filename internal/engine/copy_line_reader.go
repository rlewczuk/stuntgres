package engine

import (
	"bufio"
	"fmt"
	"io"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"unicode/utf8"
)

type copyField struct {
	decoded                   []byte
	start, end                int
	quoted, isNull, isDefault bool
}

// copyTarget describes one COPY input field for header checks and DEFAULT
// markers, so the line codec does not read the statement binding.
type copyTarget struct {
	name       string
	hasDefault bool
}

// copyLineReader is the text and CSV codec: it frames records, splits and
// decodes fields and checks the header line. It reuses its buffers across
// records and knows nothing of the statement executing the COPY.
type copyLineReader struct {
	options                      copyOptions
	reader                       *bufio.Reader
	line, decoded                []byte
	fields                       []copyField
	targets                      []copyTarget
	number                       int64
	eol                          byte
	ended, headerDone, lineValid bool
}

func (r *copyLineReader) lineError(message string, hint string) error {
	d := sqlError("22P04", message).(*sqlstate.Error)
	d.Hint = hint
	return d
}
func (r *copyLineReader) readLine() (present bool, err error) {
	if r.options.format == copyFormatCSV {
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
					return false, r.lineError("end-of-copy marker is not alone on its line", "")
				}
				if r.eol != 0 && style != r.eol {
					return false, r.lineError("end-of-copy marker does not match previous newline style", "")
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
					return false, r.lineError("literal carriage return found in data", `Use "\r" to represent carriage return.`)
				}
				return false, r.lineError("literal newline found in data", `Use "\n" to represent newline.`)
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
func copyHex(b byte) (byte, bool) {
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
func decodeCopyField(dst, raw []byte) []byte {
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
				if h, ok := copyHex(raw[i+1]); ok {
					i++
					b = h
					if i+1 < len(raw) {
						if h, ok := copyHex(raw[i+1]); ok {
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
func (r *copyLineReader) split(defaultMarkers bool) error {
	if r.options.format == copyFormatCSV {
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
		if i < len(r.line) && r.line[i] != r.options.delimiter {
			continue
		}
		raw := r.line[start:i]
		begin := len(r.decoded)
		isNull := string(raw) == r.options.null
		isDefault := defaultMarkers && r.options.hasDefault && string(raw) == r.options.def
		if isDefault && len(r.fields) < len(r.targets) {
			if target := r.targets[len(r.fields)]; !target.hasDefault {
				d := r.lineError("unexpected default marker in COPY data", "").(*sqlstate.Error)
				d.Detail = fmt.Sprintf("Column \"%s\" has no default value.", target.name)
				return d
			}
		}
		if !isNull && !isDefault {
			r.decoded = decodeCopyField(r.decoded, raw)
		}
		// Offsets rather than slices survive growth of the reusable decode buffer.
		r.fields = append(r.fields, copyField{start: begin, end: len(r.decoded), isNull: isNull, isDefault: isDefault})
		start = i + 1
	}
	for i := range r.fields {
		field := &r.fields[i]
		field.decoded = r.decoded[field.start:field.end]
		if err := sqltype.ValidateTextInput(string(r.fields[i].decoded)); err != nil {
			return err
		}
	}
	if len(r.targets) == 0 && len(r.line) == 0 {
		r.fields = r.fields[:0]
	}
	return nil
}

// header consumes the HEADER line once, before the first record, and checks
// it against the target names under HEADER MATCH.
func (r *copyLineReader) header() error {
	if r.headerDone {
		return nil
	}
	r.headerDone = true
	if r.options.header == 0 {
		return nil
	}
	_, err := r.readLine()
	if err != nil {
		return err
	}
	if r.options.header != 2 {
		return nil
	}
	if err = r.split(false); err != nil {
		return err
	}
	if len(r.fields) != len(r.targets) {
		return r.lineError(fmt.Sprintf("wrong number of fields in header line: got %d, expected %d", len(r.fields), len(r.targets)), "")
	}
	for i, field := range r.fields {
		name := r.targets[i].name
		if field.isNull {
			return r.lineError(fmt.Sprintf("column name mismatch in header line field %d: got null value (\"%s\"), expected \"%s\"", i+1, r.options.null, name), "")
		}
		if string(field.decoded) != name {
			return r.lineError(fmt.Sprintf("column name mismatch in header line field %d: got \"%s\", expected \"%s\"", i+1, string(field.decoded), name), "")
		}
	}
	return nil
}

// fieldNull applies the CSV FORCE_NOT_NULL and FORCE_NULL flags to field i.
func (r *copyLineReader) fieldNull(i int) bool {
	field := r.fields[i]
	if r.options.format != copyFormatCSV {
		return field.isNull
	}
	if field.isNull {
		return !copyForced(r.options.notNullFields, i)
	}
	return field.quoted && copyForced(r.options.nullFields, i) && string(field.decoded) == r.options.null
}
func copyContextValue(text string) string {
	if len(text) <= 100 {
		return text
	}
	end := 100
	for end > 0 && !utf8.RuneStart(text[end]) {
		end--
	}
	return text[:end] + "..."
}
func appendCopyText(dst []byte, text string, delimiter byte) []byte {
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
